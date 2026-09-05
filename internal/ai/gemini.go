package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"licode/internal/dnsclient"
)

// GeminiProvider implements Google Gemini 原生接口：
//
//	非流式: POST {base}/v1/models/{model}:generateContent
//	流式:   POST {base}/v1/models/{model}:streamGenerateContent?alt=sse
//
// 使用 Google 自己的 RPC 风格请求体（contents / parts / functionCall /
// functionResponse），与 OpenAI 的 chat/completions 完全不同。
type GeminiProvider struct {
	name      string
	baseURL   string
	apiKey    string
	model     string
	retry     int
	dns        *dnsclient.Config
	client    *http.Client
}

func (p *GeminiProvider) Provider() string { return p.name }
func (p *GeminiProvider) Model() string    { return p.model }

func (p *GeminiProvider) httpClient() *http.Client {
	if p.client == nil {
		cfg := Config{DNS: p.dns}
		p.client = cfg.NewLLMHTTPClient(60 * time.Second)
	}
	return p.client
}

// ---- wire types -----------------------------------------------------------

type geminiPart struct {
	Text             string          `json:"text,omitempty"`
	FunctionCall     *geminiFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResp `json:"functionResponse,omitempty"`
	InlineDate       map[string]any  `json:"inlineData,omitempty"`
}

type geminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFuncResp struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"` // user | model
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent               `json:"contents"`
	SystemInstruction *struct{ Parts []geminiPart } `json:"systemInstruction,omitempty"`
	Tools             []struct {
		FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
	} `json:"tools,omitempty"`
	GenerationConfig *struct {
		Temperature     *float64 `json:"temperature,omitempty"`
		MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	} `json:"generationConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      *geminiContent `json:"content"`
		FinishReason string         `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount         int `json:"promptTokenCount"`
		CandidatesTokenCount     int `json:"candidatesTokenCount"`
		CachedContentTokenCount  int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// toGeminiContent 把 ai.Message 转成 Gemini content。
func toGeminiContent(m Message) geminiContent {
	switch m.Role {
	case RoleAssistant:
		parts := []geminiPart{}
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			var args json.RawMessage
			if tc.Function.Arguments != "" {
				args = json.RawMessage(tc.Function.Arguments)
			} else {
				args = json.RawMessage("{}")
			}
			parts = append(parts, geminiPart{FunctionCall: &geminiFuncCall{
				Name: tc.Function.Name, Args: args,
			}})
		}
		return geminiContent{Role: "model", Parts: parts}
	case RoleTool:
		name := m.ToolName
		if name == "" {
			name = m.ToolCallID
		}
		// Gemini 要求函数响应作为 user 角色消息。
		return geminiContent{Role: "user", Parts: []geminiPart{{
			FunctionResponse: &geminiFuncResp{Name: name, Response: json.RawMessage(strToJSON(m.Content))},
		}}}
	default:
		parts := []geminiPart{}
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}
		for _, att := range m.Attachments {
			if att.Type == "image" {
				parts = append(parts, geminiPart{InlineDate: map[string]any{
					"mime_type": att.MIMEType,
					"data":      att.Data,
				}})
			} else {
				parts = append(parts, geminiPart{Text: "[文件: " + att.Filename + "]\n" + att.Data})
			}
		}
		return geminiContent{Role: "user", Parts: parts}
	}
}

func (p *GeminiProvider) buildBody(req ChatRequest) ([]byte, error) {
	body := geminiRequest{}
	for _, m := range req.Messages {
		body.Contents = append(body.Contents, toGeminiContent(m))
	}
	if req.System != "" {
		body.SystemInstruction = &struct{ Parts []geminiPart }{Parts: []geminiPart{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		var decls []geminiFunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			})
		}
		body.Tools = []struct {
			FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
		}{{FunctionDeclarations: decls}}
	}
	if req.Temperature != 0 || req.MaxTokens > 0 {
		gc := &struct {
			Temperature     *float64 `json:"temperature,omitempty"`
			MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
		}{}
		if req.Temperature != 0 {
			gc.Temperature = &req.Temperature
		}
		if req.MaxTokens > 0 {
			gc.MaxOutputTokens = req.MaxTokens
		}
		body.GenerationConfig = gc
	}
	return json.Marshal(body)
}

func (p *GeminiProvider) endpoint(model, suffix string) string {
	base := strings.TrimRight(p.baseURL, "/")
	// 兼容用户传入的完整版本化地址（如 https://generativelanguage.googleapis.com/v1beta）
	if strings.Contains(base, "/v1") || strings.Contains(base, "/v1beta") {
		return base + "/models/" + url.PathEscape(model) + suffix
	}
	return base + "/v1/models/" + url.PathEscape(model) + suffix
}

func (p *GeminiProvider) do(ctx context.Context, req ChatRequest, stream bool) (*http.Response, error) {
	var resp *http.Response
	err := WithRetry(p.retry, func() error {
		payload, err := p.buildBody(req)
		if err != nil {
			return err
		}
		model := req.Model
		if model == "" {
			model = p.model
		}
		ep := p.endpoint(model, ":generateContent")
		if stream {
			ep = p.endpoint(model, ":streamGenerateContent?alt=sse")
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if p.apiKey != "" {
			httpReq.Header.Set("x-goog-api-key", p.apiKey)
		}
		r, err := p.httpClient().Do(httpReq)
		if err != nil {
			return fmt.Errorf("gemini request: %w", err)
		}
		if r.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
			r.Body.Close()
			return &statusErr{status: "gemini " + r.Status, body: strings.TrimSpace(string(b))}
		}
		resp = r
		return nil
	})
	return resp, err
}

// Chat performs a non-streaming completion.
func (p *GeminiProvider) Chat(ctx context.Context, req ChatRequest) (string, error) {
	resp, err := p.do(ctx, req, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parseGeminiResponse(resp.Body)
}

// ChatStream streams text deltas and completed tool calls.
func (p *GeminiProvider) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	resp, err := p.do(ctx, req, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	br := bufio.NewReaderSize(resp.Body, 64<<10)
	var evt bytes.Buffer
	usage := &Usage{}
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			switch {
			case len(trimmed) == 0 && evt.Len() > 0:
				if err := p.handleSSE(evt.Bytes(), usage, onEvent); err != nil {
					return err
				}
				evt.Reset()
			case bytes.HasPrefix(trimmed, []byte("data:")):
				evt.Write(bytes.TrimSpace(trimmed[5:]))
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return fmt.Errorf("gemini stream: %w", rerr)
			}
			break
		}
	}
	if evt.Len() > 0 {
		if err := p.handleSSE(evt.Bytes(), usage, onEvent); err != nil {
			return err
		}
	}
	return onEvent(StreamEvent{Done: true, Usage: usage})
}

func (p *GeminiProvider) handleSSE(data []byte, usage *Usage, onEvent func(StreamEvent) error) error {
	payload := bytes.TrimSpace(data)
	if len(payload) == 0 || string(payload) == "[DONE]" {
		return nil
	}
	var resp geminiResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return fmt.Errorf("gemini parse: %w (%s)", err, truncate(string(payload), 300))
	}
	if resp.Error != nil {
		return fmt.Errorf("gemini error: %s", resp.Error.Message)
	}
	if resp.UsageMetadata != nil {
		usage.InputTokens = resp.UsageMetadata.PromptTokenCount
		usage.OutputTokens = resp.UsageMetadata.CandidatesTokenCount
		usage.CachedTokens = resp.UsageMetadata.CachedContentTokenCount
	}
	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				if err := onEvent(StreamEvent{Content: part.Text}); err != nil {
					return err
				}
			}
			if part.FunctionCall != nil {
				call := &ToolCall{ID: "gemini_" + part.FunctionCall.Name, Type: "function"}
				call.Function.Name = part.FunctionCall.Name
				if len(part.FunctionCall.Args) > 0 {
					call.Function.Arguments = string(part.FunctionCall.Args)
				} else {
					call.Function.Arguments = "{}"
				}
				if err := onEvent(StreamEvent{ToolCall: call}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// parseGeminiResponse 解析非流式 generateContent 响应。
func parseGeminiResponse(r io.Reader) (string, error) {
	var resp geminiResponse
	dec := json.NewDecoder(io.LimitReader(r, 16<<20))
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("gemini decode: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("gemini error: %s", resp.Error.Message)
	}
	var sb strings.Builder
	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			sb.WriteString(part.Text)
		}
	}
	return sb.String(), nil
}
