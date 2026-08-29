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

// OllamaProvider talks to a local Ollama server (native /api/chat endpoint).
type OllamaProvider struct {
	baseURL string
	model   string
	client  *http.Client
}

func (p *OllamaProvider) Provider() string { return "ollama" }
func (p *OllamaProvider) Model() string    { return p.model }

func (p *OllamaProvider) httpClient() *http.Client {
	if p.client == nil {
		p.client = &http.Client{}
	}
	return p.client
}

// ---- wire types -----------------------------------------------------------

type ollamaToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type ollamaMsg struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaReq struct {
	Model    string            `json:"model"`
	Messages []ollamaMsg       `json:"messages"`
	Stream   bool              `json:"stream"`
	Tools    []ollamaToolSpec  `json:"tools,omitempty"`
	Options  map[string]any    `json:"options,omitempty"`
}

type ollamaToolSpec struct {
	Type     string          `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type ollamaStreamLine struct {
	Message struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	Done bool `json:"done"`
}

func toOllamaMsg(m Message) ollamaMsg {
	switch m.Role {
	case RoleTool:
		// Tool results: role "tool" with plain content (Ollama tolerates this
		// alongside the OpenAI-style tool_calls used in the previous turn).
		return ollamaMsg{Role: RoleTool, Content: m.Content}
	case RoleAssistant:
		om := ollamaMsg{Role: RoleAssistant, Content: m.Content}
		for _, tc := range m.ToolCalls {
			var args json.RawMessage
			if tc.Function.Arguments != "" {
				args = json.RawMessage(tc.Function.Arguments)
			} else {
				args = json.RawMessage("{}")
			}
			om.ToolCalls = append(om.ToolCalls, ollamaToolCall{Function: struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}{Name: tc.Function.Name, Arguments: args}})
		}
		return om
	default:
		return ollamaMsg{Role: RoleUser, Content: m.Content}
	}
}

func (p *OllamaProvider) buildBody(req ChatRequest) ([]byte, error) {
	msgs := make([]ollamaMsg, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, ollamaMsg{Role: RoleSystem, Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, toOllamaMsg(m))
	}
	tools := make([]ollamaToolSpec, 0, len(req.Tools))
	for _, t := range req.Tools {
		spec := ollamaToolSpec{Type: "function"}
		spec.Function.Name = t.Function.Name
		spec.Function.Description = t.Function.Description
		spec.Function.Parameters = t.Function.Parameters
		tools = append(tools, spec)
	}
	body := ollamaReq{Model: req.Model, Messages: msgs, Stream: true, Tools: tools}
	if req.Temperature != 0 {
		body.Options = map[string]any{"temperature": req.Temperature}
	}
	return json.Marshal(body)
}

func (p *OllamaProvider) do(ctx context.Context, req ChatRequest) (*http.Response, error) {
	payload, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// Chat performs a non-streaming completion.
func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.System = "" // /api/chat without stream keeps system in messages
	resp, err := p.do(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("ollama decode: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("ollama error: %s", payload.Error)
	}
	return payload.Message.Content, nil
}

// ChatStream streams text deltas and completed tool calls.
func (p *OllamaProvider) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	resp, err := p.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	br := bufio.NewReaderSize(resp.Body, 64<<10)
	var pending []*ToolCall
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				if rerr != nil {
					break
				}
				continue
			}
			var sl ollamaStreamLine
			if jerr := json.Unmarshal(trimmed, &sl); jerr != nil {
				// Could be a plain error object.
				var e struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(trimmed, &e) == nil && e.Error != "" {
					return fmt.Errorf("ollama error: %s", e.Error)
				}
				return fmt.Errorf("ollama parse line: %w (%s)", jerr, truncate(string(trimmed), 200))
			}
			if sl.Message.Content != "" {
				if err := onEvent(StreamEvent{Content: sl.Message.Content}); err != nil {
					return err
				}
			}
			for _, tc := range sl.Message.ToolCalls {
				call := &ToolCall{ID: "ollama_" + tc.Function.Name, Type: "function"}
				call.Function.Name = tc.Function.Name
				if len(tc.Function.Arguments) > 0 {
					call.Function.Arguments = string(tc.Function.Arguments)
				} else {
					call.Function.Arguments = "{}"
				}
				pending = append(pending, call)
			}
			if sl.Done {
				break
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return fmt.Errorf("ollama stream: %w", rerr)
			}
			break
		}
	}
	for _, call := range pending {
		if err := onEvent(StreamEvent{ToolCall: call}); err != nil {
			return err
		}
	}
	return onEvent(StreamEvent{Done: true})
}