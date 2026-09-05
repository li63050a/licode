// HTMX 片段路由：把页面局部（设置弹窗、文件树、审计面板）在服务端用 Go
// 模板渲染成 HTML，由 /static/htmx.min.js 拉取并替换到页面。片段只读取与
// 既有 JSON API 相同的数据与逻辑，不改变任何 /api/* 行为。
package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"licode/internal/settings"
	"licode/internal/web"
	"licode/internal/websocket"
)

// registerFragmentRoutes 注册 /fragment/* 路由（需登录认证，由调用方传入 authState）。
func registerFragmentRoutes(mux *http.ServeMux, a *authState, st *serverState, ws *workspaceState, hub *websocket.Hub) {
	mux.HandleFunc("/fragment/settings", func(w http.ResponseWriter, r *http.Request) {
		if !a.require(w, r) {
			return
		}
		st.mu.RLock()
		s := st.settings.Snapshot()
		st.mu.RUnlock()
		renderHTMLFragment(w, "frag_settings.html", settingsFormData(s))
	})

	mux.HandleFunc("/fragment/files", func(w http.ResponseWriter, r *http.Request) {
		if !a.require(w, r) {
			return
		}
		p := r.URL.Query().Get("path")
		entries, err := listDirEntries(ws, p)
		if err != nil {
			renderHTMLFragment(w, "frag_files.html", map[string]any{"Error": err.Error()})
			return
		}
		parent := ""
		trimmed := strings.Trim(p, "/")
		if trimmed != "" {
			parent = dirParent(trimmed)
		}
		type fentry struct {
			Name, Path string
			IsDir      bool
		}
		out := make([]fentry, 0, len(entries))
		for _, e := range entries {
			out = append(out, fentry{Name: e.Name, Path: e.Path, IsDir: e.IsDir})
		}
		renderHTMLFragment(w, "frag_files.html", map[string]any{
			"Entries": out, "Parent": parent, "HasParent": trimmed != "",
		})
	})

	mux.HandleFunc("/fragment/audit", func(w http.ResponseWriter, r *http.Request) {
		if !a.require(w, r) {
			return
		}
		sev := r.URL.Query().Get("sev")
		renderHTMLFragment(w, "frag_audit.html", auditFragmentData(st, sev))
	})

	mux.HandleFunc("/fragment/audit/start", func(w http.ResponseWriter, r *http.Request) {
		if !a.require(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
			return
		}
		sev := r.URL.Query().Get("sev")
		_ = r.ParseForm()
		var dirs []string
		if v := r.FormValue("scan_dirs"); v != "" {
			dirs = settings.ParseToolList(v)
		}
		if _, err := startAuditRun(st, ws, dirs, nil, hub); err != nil {
			// 出错时也渲染面板，把错误展示在状态行。
			data := auditFragmentData(st, sev)
			data["StatusText"] = "启动失败: " + err.Error()
			renderHTMLFragment(w, "frag_audit.html", data)
			return
		}
		renderHTMLFragment(w, "frag_audit.html", auditFragmentData(st, sev))
	})
}

// renderHTMLFragment 以 text/html 渲染一个模板片段。
func renderHTMLFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.RenderFragment(w, name, data); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
}

// settingsFormData 构造设置弹窗表单的模板数据（字段对应 templates/frag_settings.html）。
func settingsFormData(s settings.Settings) map[string]any {
	auditOn := true
	if s.AuditEnabled != nil {
		auditOn = *s.AuditEnabled
	}
	auditFix := true
	if s.AuditAutoFix != nil {
		auditFix = *s.AuditAutoFix
	}
	providers := make([]string, 0, len(settings.ProviderChoices)+len(s.Providers))
	seen := map[string]bool{}
	for _, p := range s.Providers {
		if !seen[p.Provider] {
			seen[p.Provider] = true
			providers = append(providers, p.Provider)
		}
	}
	for _, p := range settings.ProviderChoices {
		if !seen[p] {
			seen[p] = true
			providers = append(providers, p)
		}
	}
	mcpJSON, _ := json.MarshalIndent(s.MCPServers, "", "  ")
	provJSON, _ := json.MarshalIndent(s.Providers, "", "  ")
	return map[string]any{
		"Provider": s.Provider, "Providers": providers,
		"Model": s.Model, "APIKey": s.APIKey, "BaseURL": s.BaseURL,
		"Temperature": s.Temperature, "MaxTokens": s.MaxTokens, "MaxIterations": s.MaxIterations,
		"SubAgentsOn": s.SubAgents, "AutoAllowOn": s.AutoAllow,
		"ShellPath":    s.ShellPath,
		"RetryMax":     s.RetryMax,
		"SubTimeout":   s.SubTimeout,
		"MaxCtxTokens": s.MaxCtxTokens,
		"RedactOn":     s.RedactSecrets,
		"SandboxOn":    s.Sandbox,
		"SandboxImage": s.SandboxImage,
		"CacheOn":      s.CacheEnabled,
		"AutoRetryOn":  s.ToolAutoRetry,
		"RAGOn":        s.RAGEnabled,
		"RAGSource":    s.RAGSource,
		"AuditOn":      auditOn,
		"AuditFixOn":   auditFix,
		"AuditExclude": strings.Join(s.AuditExclude, ","),
		"MCPJSON":      string(mcpJSON),
		"ProvJSON":     string(provJSON),
	}
}

// fragAuditIssue 是审计片段里的一行问题。
type fragAuditIssue struct {
	ID          string
	File        string
	Line        int
	Severity    string
	Description string
	Suggestion  string
}

// auditFragmentData 构造审计面板的模板数据（对应 templates/frag_audit.html）。
func auditFragmentData(st *serverState, sev string) map[string]any {
	st.mu.RLock()
	s := st.settings.Snapshot()
	st.mu.RUnlock()
	enabled := true
	if s.AuditEnabled != nil {
		enabled = *s.AuditEnabled
	}
	if sev == "" {
		sev = "all"
	}
	running, latest, latestID := st.audit.Status("")
	if latest == nil {
		// Status 对已结束的任务返回 nil 报告；回退到 Latest() 拿最近一次完整报告。
		if lt := st.audit.Latest(); lt != nil {
			latest = lt.Report()
			if latestID == "" {
				latestID = latest.TaskID
			}
		}
	}

	issues := []fragAuditIssue{}
	hasSummary := false
	summary := struct{ Critical, High, Medium, Low int }{}
	statusText := "尚未运行审计。点击「开始审计」扫描当前工作目录。"
	allCount := 0
	runningText := false

	if latest != nil {
		allCount = len(latest.Issues)
		sum := summarize(latest)
		hasSummary = true
		summary = struct{ Critical, High, Medium, Low int }{sum.Critical, sum.High, sum.Medium, sum.Low}
		statusText = fmt.Sprintf("上次审计：已扫描 %d 个文件", latest.Scanned)
		if running {
			statusText = "⏳ 审计进行中…"
			runningText = true
		}
		for _, it := range latest.Issues {
			if sev != "all" && it.Severity != sev {
				continue
			}
			issues = append(issues, fragAuditIssue{
				ID: it.ID, File: it.File, Line: it.Line, Severity: it.Severity,
				Description: it.Description, Suggestion: it.Suggestion,
			})
		}
	}
	if running && latest == nil {
		statusText = "⏳ 审计进行中…"
		runningText = true
	}
	if !enabled {
		statusText = "审计已禁用，请先在设置中开启"
	}

	emptyText := "尚未运行审计。点击「开始审计」扫描当前工作目录。"
	if latest != nil {
		switch {
		case runningText:
			emptyText = "正在扫描工作目录，请稍候…"
		case allCount == 0:
			emptyText = "未发现问题 🎉"
		case len(issues) == 0:
			emptyText = "该级别暂无问题"
		default:
			emptyText = ""
		}
	}

	return map[string]any{
		"Enabled":    enabled,
		"Running":    running,
		"Sev":        sev,
		"TaskID":     latestID,
		"ScanDirs":   strings.Join(s.AuditScanDirs, ","),
		"StatusText": statusText,
		"EmptyText":  emptyText,
		"HasSummary": hasSummary,
		"Summary":    summary,
		"HasIssues":  len(issues) > 0,
		"Issues":     issues,
	}
}

// dirParent 返回相对路径的上一级（"a/b/c" → "a/b"，"a" → ""）。
func dirParent(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}
