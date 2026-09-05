package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"licode/internal/audit"
	"licode/internal/settings"
	"licode/internal/websocket"
)

// auditLogDir 返回审计日志目录（~/.licode/logs/audit）。
func auditLogDir() string {
	return filepath.Join(settings.LogsDir(), "audit")
}

// auditSummary 是网页审计抽屉/弹层使用的概要结构。
type auditSummary struct {
	TaskID   string `json:"task_id"`
	Root     string `json:"root"`
	Status   string `json:"status"`
	Scanned  int    `json:"scanned_files"`
	Critical int    `json:"critical"`
	High     int    `json:"high"`
	Medium   int    `json:"medium"`
	Low      int    `json:"low"`
}

// summarize 从报告中统计各严重级别数量。
func summarize(r *audit.Report) *auditSummary {
	s := &auditSummary{
		TaskID:  r.TaskID,
		Root:    r.Root,
		Status:  r.Status,
		Scanned: r.Scanned,
	}
	for _, it := range r.Issues {
		switch it.Severity {
		case audit.SevCritical:
			s.Critical++
		case audit.SevHigh:
			s.High++
		case audit.SevMedium:
			s.Medium++
		case audit.SevLow:
			s.Low++
		}
	}
	return s
}

// registerAuditRoutes 注册代码审计相关 HTTP 路由（认证由调用方在 ServeMux 上保证）。
func registerAuditRoutes(mux *http.ServeMux, st *serverState, ws *workspaceState, hub *websocket.Hub) {
	mux.HandleFunc("/api/audit/status", func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		s := st.settings.Snapshot()
		st.mu.RUnlock()
		enabled := true
		if s.AuditEnabled != nil {
			enabled = *s.AuditEnabled
		}
		running, latest, latestID := st.audit.Status("")
		out := map[string]any{
			"enabled":   enabled,
			"running":   running,
			"latest":    latestID,
			"summary":   nil,
			"scan_dirs": s.AuditScanDirs,
			"exclude":   s.AuditExclude,
		}
		if latest != nil {
			out["summary"] = summarize(latest)
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("/api/audit/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
			return
		}
		var req struct {
			ScanDirs []string `json:"scan_dirs"`
			Exclude  []string `json:"exclude"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		taskID, err := startAuditRun(st, ws, req.ScanDirs, req.Exclude, hub)
		if err != nil {
			code := http.StatusBadRequest
			if err == errAuditDisabled {
				code = http.StatusForbidden
			} else if err == errAuditRunning {
				code = http.StatusConflict
			}
			writeJSON(w, code, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"task_id": taskID})
	})

	mux.HandleFunc("/api/audit/result", func(w http.ResponseWriter, r *http.Request) {
		taskID := r.URL.Query().Get("task_id")
		t := st.audit.Get(taskID)
		if t == nil {
			t = st.audit.Latest()
		}
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有可用的审计报告"})
			return
		}
		writeJSON(w, http.StatusOK, t.Report())
	})

	mux.HandleFunc("/api/audit/fix", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
			return
		}
		var req struct {
			TaskID   string   `json:"task_id"`
			IssueIDs []string `json:"issue_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
			return
		}
		if len(req.IssueIDs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请先勾选要修复的问题"})
			return
		}
		t := st.audit.Get(req.TaskID)
		if t == nil {
			t = st.audit.Latest()
		}
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有可用的审计报告"})
			return
		}
		report := t.Report()
		if report.Status != audit.StatusDone {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "审计仍在进行中，请稍候"})
			return
		}

		st.mu.RLock()
		client := st.client
		st.mu.RUnlock()

		confirm := r.URL.Query().Get("confirm") == "true"
		if confirm {
			res, err := st.audit.Confirm(ws.Root(), report, req.IssueIDs)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"applied":     true,
				"files":       res.Files,
				"backed_up":   res.BackedUp,
				"backup_path": res.BackupPath,
				"patch":       res.Patch,
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		diffs, err := st.audit.Preview(ctx, client, ws.Root(), report, req.IssueIDs)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preview": diffs, "files": sortedKeys(diffs)})
	})
}

// errAuditDisabled / errAuditRunning 区分可返回的错误码（JSON API 与 HTMX 片段共用）。
var (
	errAuditDisabled = errors.New("审计已禁用，请先在设置中开启")
	errAuditRunning  = errors.New("已有审计任务在运行")
)

// startAuditRun 启动一次审计：校验设置 → 组装 Options → 后台运行，
// 完成后落盘日志并广播前端刷新。由 /api/audit/start 与 HTMX 片段 /fragment/audit/start 共用。
func startAuditRun(st *serverState, ws *workspaceState, scanDirs, exclude []string, hub *websocket.Hub) (string, error) {
	st.mu.RLock()
	s := st.settings.Snapshot()
	client := st.client
	st.mu.RUnlock()
	enabled := true
	if s.AuditEnabled != nil {
		enabled = *s.AuditEnabled
	}
	if !enabled {
		return "", errAuditDisabled
	}

	opts := audit.Options{Client: client}
	if len(scanDirs) > 0 {
		opts.ScanDirs = scanDirs
	} else {
		opts.ScanDirs = s.AuditScanDirs
	}
	if len(exclude) > 0 {
		opts.Exclude = exclude
	} else {
		opts.Exclude = s.AuditExclude
	}

	if running, _, _ := st.audit.Status(""); running {
		return "", errAuditRunning
	}
	task, err := st.audit.Start(context.Background(), ws.Root(), opts)
	if err != nil {
		return "", err
	}

	// 完成时：落盘审计 JSON 日志 + 广播通知前端刷新
	go func() {
		<-task.Done()
		rep := task.Report()
		saveAuditLog(rep)
		hub.Broadcast(websocket.ServerEvent{
			Type:    websocket.EvtAuditLog,
			Content: formatAuditBroadcast(rep),
		})
	}()
	return task.ID(), nil
}

// saveAuditLog 把审计报告 JSON 写入日志目录。
func saveAuditLog(rep *audit.Report) {
	if rep == nil {
		return
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(auditLogDir(), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(auditLogDir(), rep.TaskID+".json"), data, 0o600)
}

// formatAuditBroadcast 生成审计完成后的广播摘要 JSON 文本。
func formatAuditBroadcast(rep *audit.Report) string {
	if rep == nil {
		return ""
	}
	b, err := json.Marshal(summarize(rep))
	if err != nil {
		return ""
	}
	return string(b)
}

// sortedKeys 对 map 的键排序输出，保证响应稳定。
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
