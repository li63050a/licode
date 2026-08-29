package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ClaudeProvider implements the Anthropic Messages API (streaming SSE).
type ClaudeProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func (p *ClaudeProvider) Provider() string { return "claude" }
func (p *ClaudeProvider) Model() string    { return p.model }

func (p *ClaudeProvider) httpClient() *http.Client {
	if p.client == nil {
		p.client = &http.Client{}
	}
	return p.client
}

// ---- wire types -----------------------------------------------------------

type anthropicContent struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      json.RawMessage `json:"content,omitempty"`
}

type anthropicMsg struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicReq struct {
	Model       string            `json:"model"`
	System      string            `json:"system,omitempty"`
	Messages    []anthropicMsg    `json:"messages"`
	Tools       []anthropicTool   `json:"tools,omitempty"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature *float64          `json:"temperature,omitempty"`
	Stream      bool              `json:"stream"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func toAnthropicMsg(m Message) anthropicMsg {
	switch m.Role {
	case RoleTool:
		// Tool results are sent as user messages with tool_result blocks.
		return anthropicMsg{Role: RoleUser, Content: []anthropicContent{{
			Type:      "tool_result",
			ToolUseID: m.ToolCallID,
			Content:   json.RawMessage(strToJSON(m.Content)),
		}}}
	case RoleAssistant:
		var content []anthropicContent
		if m.Content != "" {
			content = append(content, anthropicContent{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			content = append(content, anthropicContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
		return anthropicMsg{Role: RoleAssistant, Content: content}
	default:
		return anthropicMsg{Role: RoleUser, Content: []anthropicContent{{Type: "text", Text: m.Content}}}
	}
}

// strToJSON wraps a plain string into a JSON string literal so it can be used
// as a tool_result content payload (Anthropic accepts a string directly, but
// a JSON string literal is safer).
func strToJSON(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

func (p *ClaudeProvider) buildBody(req ChatRequest) ([]byte, error) {
	msgs := make([]anthropicMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, toAnthropicMsg(m))
	}
	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	body := anthropicReq{
		Model:     req.Model,
		System:    req.System,
		Messages:  msgs,
		Tools:     tools,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = 4096
	}
	if req.Temperature != 0 {
		t := req.Temperature
		body.Temperature = &t
	}
	return json.Marshal(body)
}

func (p *ClaudeProvider) do(ctx context.Context, req ChatRequest) (*http.Response, error) {
	payload, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("claude request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("claude %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// Chat performs a non-streaming completion.
func (p *ClaudeProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	resp, err := p.do(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("claude decode: %w", err)
	}
	if payload.Error != nil {
		return "", fmt.Errorf("claude error: %s", payload.Error.Message)
	}
	var sb strings.Builder
	for _, c := range payload.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

// claudeToolBlock tracks an in-flight tool_use block by content index.
type claudeToolBlock struct {
	id   string
	name string
	json []byte
}

// ChatStream streams text deltas and completed tool calls.
func (p *ClaudeProvider) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	resp, err := p.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	toolUse := map[int]*claudeToolBlock{}

	br := bufio.NewReaderSize(resp.Body, 64<<10)
	var evt bytes.Buffer
	var evtName string
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			switch {
			case len(trimmed) == 0 && evt.Len() > 0:
				if err := p.handleEvent(evtName, evt.Bytes(), toolUse, onEvent); err != nil {
					return err
				}
				evt.Reset()
				evtName = ""
			case bytes.HasPrefix(trimmed, []byte("event:")):
				evtName = strings.TrimSpace(string(trimmed[6:]))
			case bytes.HasPrefix(trimmed, []byte("data:")):
				evt.Write(bytes.TrimSpace(trimmed[5:]))
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return fmt.Errorf("claude stream: %w", rerr)
			}
			break
		}
	}
	if evt.Len() > 0 {
		if err := p.handleEvent(evtName, evt.Bytes(), toolUse, onEvent); err != nil {
			return err
		}
	}
	return onEvent(StreamEvent{Done: true})
}

func (p *ClaudeProvider) handleEvent(name string, data []byte, toolUse map[int]*claudeToolBlock, onEvent func(StreamEvent) error) error {
	payload := bytes.TrimSpace(data)
	if len(payload) == 0 {
		return nil
	}
	switch name {
	case "error":
		var e struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(payload, &e); err == nil && e.Error.Message != "" {
			return fmt.Errorf("claude error: %s", e.Error.Message)
		}
		return fmt.Errorf("claude stream error: %s", truncate(string(payload), 300))
	case "content_block_start":
		var ev struct {
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if ev.ContentBlock.Type == "tool_use" {
			toolUse[ev.Index] = &claudeToolBlock{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		}
	case "content_block_delta":
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type         string `json:"type"`
				Text         string `json:"text"`
				PartialJSON  string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				if err := onEvent(StreamEvent{Content: ev.Delta.Text}); err != nil {
					return err
				}
			}
		case "input_json_delta":
			if tb, ok := toolUse[ev.Index]; ok {
				tb.json = append(tb.json, ev.Delta.PartialJSON...)
			}
		}
	case "content_block_stop":
		var ev struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if tb, ok := toolUse[ev.Index]; ok && tb.name != "" {
			call := &ToolCall{ID: tb.id, Type: "tool_use"}
			call.Function.Name = tb.name
			call.Function.Arguments = string(tb.json)
			delete(toolUse, ev.Index)
			if err := onEvent(StreamEvent{ToolCall: call}); err != nil {
				return err
			}
		}
	}
	return nil
}