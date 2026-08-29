package agent

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"licode/internal/ai"
	"licode/internal/session"
)

// mockClient 是一个可编程的 LLMClient，用于测试 Agent 循环与调度器。
type mockClient struct {
	model string
	// calls 是一个脚本：每次 ChatStream 弹出一步。
	calls []func(req mockReq) []mockStep
	idx   atomic.Int32
}

type mockReq struct {
	system   string
	messages []string // roles
	tools    []string // tool names
}

type mockStep struct {
	content  string
	toolName string
	args     string
}

func (m *mockClient) Provider() string { return "mock" }
func (m *mockClient) Model() string    { return m.model }

func (m *mockClient) Chat(ctx context.Context, req ChatRequest) (string, error) { return "", nil }

func (m *mockClient) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	i := int(m.idx.Add(1) - 1)
	if i >= len(m.calls) {
		return nil
	}
	script := m.calls[i]
	for _, s := range script {
		if s.content != "" {
			if err := onEvent(StreamEvent{Content: s.content}); err != nil {
				return err
			}
		}
		if s.toolName != "" {
			if err := onEvent(StreamEvent{ToolCall: &ToolCall{
				ID: "call_x", Type: "function",
				Function: FunctionCall{Name: s.toolName, Arguments: s.args},
			}}); err != nil {
				return err
			}
		}
	}
	return onEvent(StreamEvent{Done: true})
}

// buildMockAgent 创建带 mockClient 的 Agent，内置工具。
func buildMockAgent(script []func(req mockReq) []mockStep) *Agent {
	mc := &mockClient{model: "mock-model", calls: script}
	return NewAgent(mc, "测试提示词")
}

func TestAgentToolLoop(t *testing.T) {
	// 第一步：要求调用 write_file；第二步：给最终回答。
	ag := buildMockAgent([]func(req mockReq) []mockStep{
		func(req mockReq) []mockStep {
			return []mockStep{{toolName: "write_file", args: `{"path":"/tmp/licode_test.txt","content":"hello"}`}}
		},
		func(req mockReq) []mockStep {
			return []mockStep{{content: "已写入"}}
		},
	})

	var got strings.Builder
	err := ag.Run(context.Background(), "写个文件", func(e Event) {
		if e.Type == EventText {
			got.WriteString(e.Content)
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got.String(), "已写入") {
		t.Fatalf("expected final answer, got %q", got.String())
	}
	// 工具确实执行了：文件应存在
	if !fileExists("/tmp/licode_test.txt") {
		t.Fatal("write_file tool did not actually write the file")
	}
	// 会话中应包含 tool 消息（回填给模型）
	msgs := ag.Session.Messages()
	foundTool := false
	for _, m := range msgs {
		if m.Role == "tool" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatal("session missing tool result message")
	}
}

func TestAgentErrorPropagation(t *testing.T) {
	// 第一步返回一个不存在的工具 -> 应生成 TOOL ERROR 回填并继续
	ag := buildMockAgent([]func(req mockReq) []mockStep{
		func(req mockReq) []mockStep {
			return []mockStep{{toolName: "no_such_tool", args: `{}`}}
		},
		func(req mockReq) []mockStep {
			return []mockStep{{content: "完成"}}
		},
	})
	err := ag.Run(context.Background(), "x", func(e Event) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := ag.Session.Messages()
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "TOOL ERROR") {
			return
		}
	}
	t.Fatal("expected TOOL ERROR in session")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestSessionTruncation(t *testing.T) {
	s := session.NewSession(50)
	s.Add(ai.Message{Role: ai.RoleUser, Content: strings.Repeat("a", 80)})
	s.Add(ai.Message{Role: ai.RoleAssistant, Content: strings.Repeat("b", 80)})
	s.Add(ai.Message{Role: ai.RoleUser, Content: "最近一条"})
	msgs := s.MessagesForLLM("")
	if len(msgs) == 0 {
		t.Fatal("empty messages")
	}
	if msgs[len(msgs)-1].Content != "最近一条" {
		t.Fatal("tail message must survive truncation")
	}
	// 最旧的消息应被丢弃
	if strings.HasPrefix(msgs[0].Content, "aaa") {
		t.Fatal("oldest message should be trimmed")
	}
}