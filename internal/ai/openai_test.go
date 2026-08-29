package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockOpenAI server implements a minimal OpenAI-compatible /chat/completions
// endpoint. scenario:
//   - "text":  streams two content deltas then [DONE]
//   - "tools": streams content then an incremental tool call then [DONE]
func mockOpenAIServer(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		var req openaiChatReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)

		writeChunk := func(delta openaiDelta, finish string) {
			payload, _ := json.Marshal(openaiChunk{Choices: []openaiChoice{{
				Index: 0, Delta: delta, FinishReason: &finish,
			}}})
			fmt.Fprintf(w, "data: %s\n\n", payload)
			fl.Flush()
		}

		switch scenario {
		case "text":
			writeChunk(openaiDelta{Content: "你好，"}, "")
			writeChunk(openaiDelta{Content: "世界"}, "")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "tools":
			writeChunk(openaiDelta{Content: "我需要查询。"}, "")
			// 分两次发送同一个工具调用的增量：第一次带名称与部分参数
			writeChunk(openaiDelta{ToolCalls: []aiToolCallReq{{
				Index: 0, ID: "call_1", Type: "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "read_file", Arguments: `{"path":"`},
			}}}, "")
			// 第二次仅追加参数剩余部分
			writeChunk(openaiDelta{ToolCalls: []aiToolCallReq{{
				Index: 0,
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Arguments: `go.mod"}`},
			}}}, "tool_calls")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
}

func TestOpenAIChatStreamText(t *testing.T) {
	srv := mockOpenAIServer(t, "text")
	defer srv.Close()
	p := &OpenAIProvider{baseURL: srv.URL + "/v1", apiKey: "test", model: "gpt-test"}

	var got string
	err := p.ChatStream(context.Background(), ChatRequest{Model: "gpt-test"}, func(e StreamEvent) error {
		if e.Content != "" {
			got += e.Content
		}
		if e.Done {
			// no-op
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got != "你好，世界" {
		t.Fatalf("expected 你好，世界, got %q", got)
	}
}

func TestOpenAIChatStreamToolCalls(t *testing.T) {
	srv := mockOpenAIServer(t, "tools")
	defer srv.Close()
	p := &OpenAIProvider{baseURL: srv.URL + "/v1", apiKey: "test", model: "gpt-test"}

	var calls []*ToolCall
	err := p.ChatStream(context.Background(), ChatRequest{Model: "gpt-test"}, func(e StreamEvent) error {
		if e.ToolCall != nil {
			calls = append(calls, e.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	c := calls[0]
	if c.Function.Name != "read_file" {
		t.Errorf("name = %q", c.Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments not valid json: %v", err)
	}
	if args["path"] != "go.mod" {
		t.Errorf("path = %v", args["path"])
	}
}

func TestNewFactory(t *testing.T) {
	for _, tc := range []struct{ provider, want string }{
		{"openai", "openai"},
		{"claude", "claude"},
		{"ollama", "ollama"},
	} {
		c, err := New(Config{Provider: tc.provider})
		if err != nil {
			t.Fatalf("New(%s): %v", tc.provider, err)
		}
		if c.Provider() != tc.want {
			t.Errorf("provider = %s", c.Provider())
		}
	}
	if _, err := New(Config{Provider: "bogus"}); err == nil {
		t.Fatal("expected error for bogus provider")
	}
	if !strings.Contains(Defaults["ollama"].BaseURL, "11434") {
		t.Errorf("ollama default base url: %s", Defaults["ollama"].BaseURL)
	}
}
