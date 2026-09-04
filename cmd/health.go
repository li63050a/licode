package cmd

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"licode/internal/rag"
)

// handleHealth 存活探针：进程在跑即返回 200。
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady 就绪探针：检查配置合法、LLM 服务商地址可连通、Docker 沙箱状态，
// 关停期间返回 503。
func handleReady(st *serverState) http.HandlerFunc {
	var (
		mu      sync.Mutex
		probeAt *time.Time
	)
	return func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		shuttingDown := st.shuttingDown
		s := st.settings.Snapshot()
		st.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		if shuttingDown {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting_down"})
			return
		}

		problems := []string{}

		// LLM 服务商地址连通性（TCP 探测，15s 缓存避免频繁探测）
		mu.Lock()
		cached := probeAt != nil && time.Since(*probeAt) < 15*time.Second
		mu.Unlock()
		if !cached {
			if base := s.BaseURL; base != "" {
				if !tcpProbeFromURL(base) {
					problems = append(problems, "llm_unreachable")
				}
			}
			mu.Lock()
			now := time.Now()
			probeAt = &now
			mu.Unlock()
		}

		// Docker 沙箱状态（仅启用沙箱时检查）
		if s.Sandbox {
			if !dockerHealthy() {
				problems = append(problems, "docker_unavailable")
			}
		}

		if len(problems) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready", "problems": problems,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// tcpProbeFromURL 从 base URL 提取 host:port 并做 TCP 探测。
func tcpProbeFromURL(base string) bool {
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := u.Host
	if host == "" {
		return false
	}
	// 未显式带端口时补默认端口
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	return tcpProbe(host)
}

// tcpProbe 对 host:port 做连接探测。
func tcpProbe(hostport string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// dockerHealthy 快速检测 docker 守护进程是否可用。
func dockerHealthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	return cmd.Run() == nil
}

// ragLookup 对当前项目源码做轻量 RAG 检索，返回相关片段文本（供注入系统提示词）。
// 索引懒构建并在 RAGSource 变化或热重载时重建；单次查询仅在内存里做词重叠打分。
func (st *serverState) ragLookup(query, source string, topFiles int) string {
	if topFiles <= 0 {
		topFiles = 5
	}
	if source == "" {
		if wd, err := os.Getwd(); err == nil {
			source = wd
		}
	}
	st.mu.RLock()
	idx := st.rag
	st.mu.RUnlock()
	if idx == nil || idx.Root() != source {
		idx = rag.NewIndex(source)
		st.mu.Lock()
		st.rag = idx
		st.mu.Unlock()
	}
	snips := idx.Query(query, topFiles)
	var sb strings.Builder
	for _, s := range snips {
		sb.WriteString(s.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}
