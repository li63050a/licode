// Package ai defines the unified LLM client interface, message types and
// the provider factory. All AI providers (OpenAI, Claude, Ollama) implement
// the LLMClient interface so callers can switch providers at runtime.
package ai

import (
	"context"
	"encoding/json"
)

// Role constants for messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// FunctionCall is a tool invocation the model wants executed.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded arguments
}

// ToolCall wraps a function call with a unique id.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// Message is a provider-agnostic conversation message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// ToolName 记录工具结果对应的函数名（Gemini functionResponse 需要）。
	ToolName string `json:"tool_name,omitempty"`
}

// FunctionSpec describes a callable tool's schema.
type FunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// Tool is an OpenAI-style tool definition sent to the model.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function FunctionSpec `json:"function"`
}

// ChatRequest is a request payload for any provider.
type ChatRequest struct {
	Model       string
	Messages    []Message
	System      string
	Tools       []Tool
	MaxTokens   int
	Temperature float64
}

// StreamEvent is an incremental result emitted during streaming.
// Only one field is set per event:
//   - Content:  a text delta to append
//   - ToolCall: a completed tool call (emitted once the arguments are final)
//   - Done:     the stream finished cleanly
//   - Error:    a fatal error aborted the stream
type StreamEvent struct {
	Content  string
	ToolCall *ToolCall
	Done     bool
	Error    error
}

// LLMClient is the unified interface implemented by every provider.
type LLMClient interface {
	// Provider returns the provider name ("openai", "claude", "ollama").
	Provider() string
	// Model returns the model name currently configured.
	Model() string
	// Chat performs a single non-streaming completion.
	Chat(ctx context.Context, req ChatRequest) (string, error)
	// ChatStream streams completion results into onEvent. The callback may
	// return an error to abort streaming early.
	ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error
}

// ensure providers implement the interface at compile time.
var (
	_ LLMClient = (*OpenAIProvider)(nil)
	_ LLMClient = (*ClaudeProvider)(nil)
	_ LLMClient = (*OllamaProvider)(nil)
	_ LLMClient = (*GeminiProvider)(nil)
)