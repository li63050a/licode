package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"licode/internal/agent"
	"licode/internal/ai"
	"licode/internal/session"
	"licode/internal/settings"
)

// exportBundle 是"全部导出"的 JSON 格式。
type exportBundle struct {
	Version    string            `json:"version"`
	ExportedAt string            `json:"exported_at"`
	Settings   settings.Settings `json:"settings"`
	Sessions   []exportSession   `json:"sessions"`
	Skills     []agent.Skill     `json:"skills"`
}

type exportSession struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Summary  string       `json:"summary"`
	Count    int          `json:"count"`
	Messages []ai.Message `json:"messages"`
}

// exportDir 是导出文件存放目录。
func exportDir() string {
	return filepath.Join(settings.BaseDir(), "export")
}

func timestamp() string {
	return time.Now().Format("20060102-150405")
}

// exportAll 导出全部（设置 + 会话 + 技能）为 JSON。
func exportAll(path string, st settings.Settings, sessions *session.Manager) error {
	b := exportBundle{
		Version:    "1.0",
		ExportedAt: time.Now().Format(time.RFC3339),
		Settings:   st.Snapshot(),
		Skills:     agent.LoadSkills(agent.SkillDirs()...),
	}
	for _, info := range sessions.List() {
		s, ok := sessions.Get(info.ID)
		if !ok {
			continue
		}
		msgs := s.Messages()
		b.Sessions = append(b.Sessions, exportSession{
			ID:       s.ID(),
			Title:    s.Title(),
			Summary:  s.Summary(),
			Count:    len(msgs),
			Messages: msgs,
		})
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// exportSessionsMarkdown 把对话记录导出为 Markdown 文本。
func exportSessionsMarkdown(path string, sessions *session.Manager) error {
	var sb strings.Builder
	for _, info := range sessions.List() {
		s, ok := sessions.Get(info.ID)
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "# %s\n\n", s.Title())
		for _, m := range s.Messages() {
			switch m.Role {
			case "user":
				fmt.Fprintf(&sb, "## 你\n\n%s\n\n", m.Content)
			case "assistant":
				fmt.Fprintf(&sb, "## licode\n\n%s\n\n", m.Content)
			case "tool":
				fmt.Fprintf(&sb, "> 工具 %s\n\n```\n%s\n```\n\n", m.ToolName, m.Content)
			}
		}
		sb.WriteString("---\n\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// importBundle 从 JSON 导出包导入（设置 + 会话）。
func importBundle(path string, st *settings.Settings, sessions *session.Manager) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var b exportBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("不是有效的 licode 导出包: %w", err)
	}
	// 导入设置
	if err := b.Settings.Validate(); err == nil {
		*st = b.Settings.Snapshot()
		_ = st.Save("")
	}
	// 导入会话
	for _, es := range b.Sessions {
		s := session.NewSession(0)
		s.Restore(es.Title, es.Messages, es.Summary)
		sessions.NewFromSession(s)
	}
	return nil
}

// importSessionsJSON 从纯会话 JSON（数组或单对象）导入。
func importSessionsJSON(path string, sessions *session.Manager) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var recs []exportSession
	if err := json.Unmarshal(data, &recs); err != nil {
		var single exportSession
		if err2 := json.Unmarshal(data, &single); err2 != nil {
			return fmt.Errorf("无法解析会话文件: %v", err)
		}
		recs = []exportSession{single}
	}
	for _, es := range recs {
		s := session.NewSession(0)
		s.Restore(es.Title, es.Messages, es.Summary)
		sessions.NewFromSession(s)
	}
	return nil
}

// doTransfer 处理 /export 与 /import 命令。
func (m *uiModel) doTransfer(input string) {
	cmd := strings.Fields(input)
	if len(cmd) == 0 {
		return
	}
	_ = os.MkdirAll(exportDir(), 0o755)
	switch cmd[0] {
	case "/export":
		mode := ""
		if len(cmd) > 1 {
			mode = cmd[1]
		}
		if m.remote != nil {
			m.status = "远程模式下导出在服务器执行（暂不支持）"
			return
		}
		switch mode {
		case "md", "markdown":
			p := filepath.Join(exportDir(), "sessions-"+timestamp()+".md")
			if err := exportSessionsMarkdown(p, m.sessions); err != nil {
				m.status = "导出失败: " + err.Error()
				return
			}
			m.status = "已导出对话记录(md): " + p
		case "sessions":
			p := filepath.Join(exportDir(), "sessions-"+timestamp()+".json")
			if err := exportSessionsJSON(p, m.sessions); err != nil {
				m.status = "导出失败: " + err.Error()
				return
			}
			m.status = "已导出对话记录(json): " + p
		default: // 全部（json）
			p := filepath.Join(exportDir(), "licode-export-"+timestamp()+".json")
			if err := exportAll(p, m.settings, m.sessions); err != nil {
				m.status = "导出失败: " + err.Error()
				return
			}
			m.status = "已导出全部(json): " + p
		}
	case "/import":
		if len(cmd) < 2 {
			m.status = "用法: /import <文件路径>"
			return
		}
		p := strings.TrimSpace(strings.TrimPrefix(input, "/import"))
		if m.remote != nil {
			m.status = "远程模式下导入在服务器执行（暂不支持）"
			return
		}
		if strings.HasSuffix(strings.ToLower(p), ".json") {
			var err error
			if isBundleFile(p) {
				err = importBundle(p, &m.settings, m.sessions)
			} else {
				err = importSessionsJSON(p, m.sessions)
			}
			if err != nil {
				m.status = "导入失败: " + err.Error()
				return
			}
		} else {
			m.status = "目前仅支持导入 JSON 格式（完整导出包或会话记录）"
			return
		}
		m.agent.Session = m.sessions.Current()
		m.reloadMessages()
		m.status = "导入成功: " + p
	}
}

// isBundleFile 粗略判断是否为完整导出包（含 settings 字段）。
func isBundleFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var probe map[string]any
	return json.Unmarshal(data, &probe) == nil && probe["settings"] != nil
}

// exportSessionsJSON 仅导出会话记录为 JSON。
func exportSessionsJSON(path string, sessions *session.Manager) error {
	var recs []exportSession
	for _, info := range sessions.List() {
		s, ok := sessions.Get(info.ID)
		if !ok {
			continue
		}
		recs = append(recs, exportSession{
			ID: s.ID(), Title: s.Title(), Summary: s.Summary(),
			Count: len(s.Messages()), Messages: s.Messages(),
		})
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
