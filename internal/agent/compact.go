package agent

import (
	"context"
	"strings"

	"licode/internal/ai"
)

// compactIfNeeded 在上下文即将超限时，用 LLM 把较早的对话压缩成摘要，
// 并修剪掉被摘要覆盖的旧消息（opencode 的 compaction 行为）。
func (a *Agent) compactIfNeeded(ctx context.Context) {
	if a.Client == nil || a.Session.Dropped(a.System) <= 0 {
		return
	}
	msgs := a.Session.Messages()
	if len(msgs) < 4 {
		return
	}
	// 保留最近 40% 作为可继续引用的上下文，其余交给摘要。
	cut := len(msgs) * 3 / 5
	if cut >= len(msgs)-2 {
		cut = len(msgs) - 2
	}
	head := msgs[:cut]

	var sb strings.Builder
	sb.WriteString("请把下面这段旧对话压缩成简洁的简体中文摘要。必须保留：用户的需求、已经做出的修改与涉及文件、关键结论、尚未完成的事项。不要遗漏重要信息：\n\n")
	for _, m := range head {
		role := m.Role
		if role == ai.RoleTool {
			role = "工具结果(" + m.ToolName + ")"
		}
		sb.WriteString(role + ": " + m.Content + "\n")
	}
	sum, err := a.Client.Chat(ctx, ai.ChatRequest{
		Model:     a.Model,
		System:    "你是对话压缩器，只输出摘要本身，不要额外说明。",
		Messages:  []ai.Message{{Role: ai.RoleUser, Content: sb.String()}},
		MaxTokens: 1024,
	})
	if err != nil {
		return
	}
	if strings.TrimSpace(sum) == "" {
		return
	}
	a.Session.SetSummary(strings.TrimSpace(sum))
	a.Session.TrimHead(cut)
}
