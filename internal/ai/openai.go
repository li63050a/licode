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

// OpenAIProvider implements the OpenAI Chat Completions API (also compatible
// with any OpenAI-compatible endpoint such as vLLM, LM Studio, OpenRouter).
type OpenAIProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func (p *OpenAIProvider) Provider() string { return "openai" }
func (p *OpenAIProvider) Model() string    { return p.model }

func (p *OpenAIProvider) httpClient() *http.Client {
	if p.client == nil {
		p.client = &http.Client{}
	}
	return p.client
}

// ---- wire types -----------------------------------------------------------

type openaiMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []aiToolCallReq `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type aiToolCallReq struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type openaiChatReq struct {
	Model       string       `json:"model"`
	Messages    []openaiMsg  `json:"messages"`
	Tools       []Tool       `json:"tools,omitempty"`
	Stream      bool         `json:"stream"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

type openaiDelta struct {
	Content   string          `json:"content,omitempty"`
	ToolCalls []aiToolCallReq `json:"tool_calls,omitempty"`
}

type openaiChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	Message      openaiDelta `json:"message"`
	FinishReason *string     `json:"finish_reason"`
}

type openaiChunk struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (p *OpenAIProvider) endpoint() string {
	if strings.HasSuffix(p.baseURL, "/chat/completions") {
		return p.baseURL
	}
	return p.baseURL + "/chat/completions"
}

func (p *OpenAIProvider) buildBody(req ChatRequest, stream bool) ([]byte, error) {
	msgs := make([]openaiMsg, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openaiMsg{Role: RoleSystem, Content: req.System})
	}
	for _, m := range req.Messages {
		om := openaiMsg{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, aiToolCallReq{
				ID:   tc.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
		msgs = append(msgs, om)
	}
	body := openaiChatReq{Model: req.Model, Messages: msgs, Tools: req.Tools, Stream: stream}
	if req.MaxTokens > 0 {
		body.MaxTokens = req.MaxTokens
	}
	if req.Temperature != 0 {
		t := req.Temperature
		body.Temperature = &t
	}
	return json.Marshal(body)
}

func (p *OpenAIProvider) do(ctx context.Context, req ChatRequest, stream bool) (*http.Response, error) {
	payload, err := p.buildBody(req, stream)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

func (p *OpenAIProvider) decodeError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(b)))
}

// Chat performs a non-streaming completion.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	resp, err := p.do(ctx, req, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", p.decodeError(resp)
	}
	var chunk openaiChunk
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&chunk); err != nil {
		return "", fmt.Errorf("openai decode: %w", err)
	}
	if chunk.Error != nil {
		return "", fmt.Errorf("openai error: %s", chunk.Error.Message)
	}
	var sb strings.Builder
	for _, c := range chunk.Choices {
		sb.WriteString(c.Message.Content)
	}
	return sb.String(), nil
}

// ChatStream streams token deltas and completed tool calls.
func (p *OpenAIProvider) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	resp, err := p.do(ctx, req, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Fallback: some OpenAI-compatible servers ignore stream=true and return
	// a plain completion body instead of SSE.
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		var chunk openaiChunk
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&chunk); err != nil {
			return fmt.Errorf("openai decode: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("openai error: %s", chunk.Error.Message)
		}
		for _, c := range chunk.Choices {
			if c.Message.Content != "" {
				if err := onEvent(StreamEvent{Content: c.Message.Content}); err != nil {
					return err
				}
			}
			for _, tc := range c.Message.ToolCalls {
				call := &ToolCall{ID: tc.ID, Type: "function"}
				call.Function.Name = tc.Function.Name
				call.Function.Arguments = tc.Function.Arguments
				if err := onEvent(StreamEvent{ToolCall: call}); err != nil {
					return err
				}
			}
		}
		return onEvent(StreamEvent{Done: true})
	}

	acc := map[int]*ToolCall{} // index -> accumulated tool call
	br := bufio.NewReaderSize(resp.Body, 64<<10)
	var evt bytes.Buffer
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			switch {
			case len(trimmed) == 0 && evt.Len() > 0:
				if err := p.handleEvent(evt.Bytes(), acc, onEvent); err != nil {
					return err
				}
				evt.Reset()
			case bytes.HasPrefix(trimmed, []byte("data:")):
				evt.Write(bytes.TrimSpace(trimmed[5:]))
				evt.WriteByte('\n')
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return fmt.Errorf("openai stream: %w", rerr)
			}
			break
		}
	}
	// Flush any trailing event without a terminating blank line.
	if evt.Len() > 0 {
		if err := p.handleEvent(evt.Bytes(), acc, onEvent); err != nil {
			return err
		}
	}
	return onEvent(StreamEvent{Done: true})
}

func (p *OpenAIProvider) handleEvent(data []byte, acc map[int]*ToolCall, onEvent func(StreamEvent) error) error {
	payload := bytes.TrimSpace(data)
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil
	}
	var chunk openaiChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return fmt.Errorf("openai parse chunk: %w (%s)", err, truncate(string(payload), 300))
	}
	if chunk.Error != nil {
		return fmt.Errorf("openai error: %s", chunk.Error.Message)
	}
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" {
			if err := onEvent(StreamEvent{Content: ch.Delta.Content}); err != nil {
				return err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			cur, ok := acc[tc.Index]
			if !ok {
				cur = &ToolCall{Type: "function"}
				acc[tc.Index] = cur
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				cur.Function.Arguments += tc.Function.Arguments
			}
		}
		// A finish_reason marks the end of this reply; emit accumulated
		// tool calls before finishing.
		if ch.FinishReason != nil && len(acc) > 0 {
			for _, call := range sortedToolCalls(acc) {
				if call.Function.Name != "" {
					if err := onEvent(StreamEvent{ToolCall: call}); err != nil {
						return err
					}
				}
			}
			acc = map[int]*ToolCall{}
		}
	}
	return nil
}

func sortedToolCalls(acc map[int]*ToolCall) []*ToolCall {
	out := make([]*ToolCall, 0, len(acc))
	for i := 0; i < len(acc); i++ {
		if c, ok := acc[i]; ok {
			out = append(out, c)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}