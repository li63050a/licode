// Package agent implements the main coding agent: a tool-calling loop that
// streams events to any UI (TUI, WebSocket, web page). It also provides a
// lightweight sub-agent system with DAG dependency scheduling.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"licode/internal/ai"
	"licode/internal/session"
)

// EventType enumerates events emitted by an Agent while running.
type EventType string

const (
	// EventText streams assistant text deltas.
	EventText EventType = "text"
	// EventToolStart is emitted before a tool executes.
	EventToolStart EventType = "tool_start"
	// EventToolDone is emitted after a tool returns.
	EventToolDone EventType = "tool_done"
	// EventDone signals a completed reply.
	EventDone EventType = "done"
	// EventError signals a fatal error.
	EventError EventType = "error"
	// EventStatus reports transient status like iteration count.
	EventStatus EventType = "status"
	// EventAsk asks the user to approve a tool call (permission=ask).
	EventAsk EventType = "ask"
	// EventSettings carries updated runtime settings (from a remote server).
	EventSettings EventType = "settings"
	// EventSessions carries the remote session list.
	EventSessions EventType = "sessions"
)

// Event is a UI-agnostic stream event.
type Event struct {
	Type      EventType `json:"type"`
	Content   string    `json:"content,omitempty"`
	ToolName  string    `json:"toolName,omitempty"`
	ToolArgs  string    `json:"toolArgs,omitempty"`
	ToolOut   string    `json:"toolOut,omitempty"`
	Error     string    `json:"error,omitempty"`
	Settings  any       `json:"settings,omitempty"`
	AskID     string    `json:"askId,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
}

// DefaultMainPrompt 是主 Agent 的系统提示词。
const DefaultMainPrompt = `你叫 licode，是一个运行在终端里的 AI 编程助手。

请始终使用简体中文回复用户，代码、命令、文件路径等保持原文。

必须使用 Markdown 格式输出：标题、列表、加粗、行内代码、代码块、表格、
引用等。代码块用三反引号包裹并标注语言。结构化内容尽量用表格或列表呈现，
确保在 Web 端能正常渲染。

帮助用户理解与修改代码。当用户要求你在代码库中做某事时，优先使用你的
可用工具（读写文件、搜索代码、执行 shell 命令）获取真实信息，而不是凭空猜测。

规则：
- 简洁。先读后改。
- 需要实现功能时，先简要说明思路，再用工具实际修改，最后总结改动内容和
  验证方法。
- 对于可以拆解成多个相互独立子任务的复杂任务（如「探索+规划+实现」），
  优先用 dispatch_subagents 一次性提交多个任务（可带 depends_on），让独立
  任务并行执行，加速完成，最后汇总结果。
- 需要构建或测试时使用 shell 命令。
- 绝不声称自己做了某件事，除非你确实通过工具完成了它。
- 需要更多信息时，提出一个聚焦的问题。`

// Tool is a registered callable function.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	schemaBytes []byte
	Run         func(ctx context.Context, args map[string]any) (string, error)
}

// Registry is a concurrency-safe tool set.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.Name == "" {
		return errors.New("tool name required")
	}
	t.schemaBytes, _ = json.Marshal(t.Schema)
	r.tools[t.Name] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// List returns OpenAI-style tool definitions for the model.
func (r *Registry) List() []ai.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ai.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, ai.Tool{
			Type: "function",
			Function: ai.FunctionSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.schemaBytes,
			},
		})
	}
	return out
}

// Execute runs a tool by name with JSON-encoded arguments.
func (r *Registry) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	var args map[string]any
	if len(bytes.TrimSpace(argsJSON)) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("tool %s: bad arguments: %w", name, err)
		}
	}
	return t.Run(ctx, args)
}

// Agent is the main orchestration loop. It calls the LLM, executes any tool
// calls, feeds results back, and repeats until the model replies without
// tools or MaxIterations is hit.
type Agent struct {
	Name          string
	System        string
	Client        ai.LLMClient
	Model         string
	Tools         *Registry
	Session       *session.Session
	SubAgents     []SubAgentSpec
	MaxIterations int
	MaxTokens     int
	Temperature   float64
	// Permissions 工具名 -> allow/ask/deny；"*" 为默认模式。
	Permissions map[string]string
	// Ask 在 permission=ask 时被调用，返回 true 表示允许执行。
	Ask func(ctx context.Context, toolName, args string) (bool, error)
	// Compaction 上下文超限时用 LLM 压缩旧对话。
	Compaction bool
}

func NewAgent(client ai.LLMClient, system string) *Agent {
	a := &Agent{
		Name:          "main",
		System:        system,
		Client:        client,
		Model:         client.Model(),
		Tools:         NewRegistry(),
		Session:       session.NewSession(0),
		MaxIterations: 16,
		MaxTokens:     4096,
	}
	RegisterDefaultTools(a.Tools)
	return a
}

// Run executes a user request, streaming events through onEvent.
// It returns after the reply completes or an error occurs.
func (a *Agent) Run(ctx context.Context, input string, onEvent func(Event)) error {
	a.Session.Add(ai.Message{Role: ai.RoleUser, Content: input})

	for iter := 1; iter <= a.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		onEvent(Event{Type: EventStatus, Content: fmt.Sprintf("思考中 (%d)", iter)})

		if a.Compaction {
			a.compactIfNeeded(ctx)
		}
		msgs := a.Session.MessagesForLLM(a.System)
		req := ai.ChatRequest{
			Model:       a.Model,
			System:      a.System,
			Messages:    msgs,
			Tools:       a.Tools.List(),
			MaxTokens:   a.MaxTokens,
			Temperature: a.Temperature,
		}

		var asst ai.Message
		asst.Role = ai.RoleAssistant
		done := false
		callErr := a.Client.ChatStream(ctx, req, func(evt ai.StreamEvent) error {
			switch {
			case evt.Content != "":
				asst.Content += evt.Content
				onEvent(Event{Type: EventText, Content: evt.Content})
			case evt.ToolCall != nil:
				asst.ToolCalls = append(asst.ToolCalls, *evt.ToolCall)
			case evt.Done:
				done = true
			case evt.Error != nil:
				return evt.Error
			}
			return nil
		})
		if callErr != nil {
			onEvent(Event{Type: EventError, Error: callErr.Error()})
			return callErr
		}
		_ = done

		a.Session.Add(asst)

		if len(asst.ToolCalls) == 0 {
			onEvent(Event{Type: EventDone})
			return nil
		}

		// Execute tool calls sequentially (order from the model).
		for _, tc := range asst.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			onEvent(Event{Type: EventToolStart, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments})
			out, terr := a.runTool(ctx, tc)
			if terr != nil {
				out = fmt.Sprintf("TOOL ERROR: %v", terr)
			}
			onEvent(Event{Type: EventToolDone, ToolName: tc.Function.Name, ToolOut: out})
			a.Session.Add(ai.Message{Role: ai.RoleTool, ToolCallID: tc.ID, ToolName: tc.Function.Name, Content: out})
		}
	}
	return errors.New("max iterations reached without a final answer")
}

// permissionMode 返回工具的执行模式：allow / ask / deny。
func (a *Agent) permissionMode(tool string) string {
	if len(a.Permissions) == 0 {
		return "allow"
	}
	if m, ok := a.Permissions[tool]; ok {
		return m
	}
	if m, ok := a.Permissions["*"]; ok {
		return m
	}
	return "allow"
}

// runTool 执行单个工具，先做权限检查。
func (a *Agent) runTool(ctx context.Context, tc ai.ToolCall) (string, error) {
	switch a.permissionMode(tc.Function.Name) {
	case "deny":
		return "已拒绝执行 " + tc.Function.Name + "（权限配置为禁止）", nil
	case "ask":
		if a.Ask != nil {
			ok, aerr := a.Ask(ctx, tc.Function.Name, tc.Function.Arguments)
			if aerr != nil {
				return "", aerr
			}
			if !ok {
				return "用户拒绝执行工具 " + tc.Function.Name, nil
			}
		}
		// Ask 未设置时视为允许。
	}
	return a.Tools.Execute(ctx, tc.Function.Name, []byte(tc.Function.Arguments))
}
