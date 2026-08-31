package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// MCPServer 描述一个 MCP 服务器。
// Type 为 "http" 时走远程 Streamable HTTP；否则走 stdio 本地进程。
type MCPServer struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

// IsHTTP 判断该服务器是否为远程 HTTP 类型。
func (s MCPServer) IsHTTP() bool {
	return strings.EqualFold(strings.TrimSpace(s.Type), "http")
}

// 包级跟踪所有 mcp 进程，便于统一关闭。
var (
	mcpMu      sync.Mutex
	mcpClients []*mcpClient
	mcpHTTP    []*httpMCPClient
)

// mcpClient 是一个最小 JSON-RPC (stdio) MCP 客户端。
type mcpClient struct {
	server  MCPServer
	cmd     *exec.Cmd
	stdin   *bufio.Writer
	mu      sync.Mutex
	nextID  int
	pending map[int]chan json.RawMessage
}

type jsonrpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newMCPClient(s MCPServer) (*mcpClient, error) {
	if s.Command == "" {
		return nil, fmt.Errorf("mcp %s: command is empty", s.Name)
	}
	for _, arg := range s.Args {
		if strings.Contains(arg, ";") || strings.Contains(arg, "|") || strings.Contains(arg, "&") {
			return nil, fmt.Errorf("mcp %s: args contain disallowed characters", s.Name)
		}
	}
	cmd := exec.Command(s.Command, s.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmdCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s 启动失败: %w", s.Name, err)
	}
	c := &mcpClient{
		server:  s,
		cmd:     cmd,
		stdin:   bufio.NewWriter(stdin),
		pending: map[int]chan json.RawMessage{},
	}
	go c.readLoop(cmdCtx, stdout)
	mcpMu.Lock()
	mcpClients = append(mcpClients, c)
	mcpMu.Unlock()

	if _, err := c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "licode", "version": "0.1"},
	}); err != nil {
		c.close()
		return nil, err
	}
	_ = c.notify("notifications/initialized", map[string]any{})
	return c, nil
}

func (c *mcpClient) readLoop(ctx context.Context, stdout interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !sc.Scan() {
			return
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg jsonrpcMsg
		if json.Unmarshal([]byte(line), &msg) != nil || msg.ID == nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[*msg.ID]
		if ok {
			delete(c.pending, *msg.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- json.RawMessage(line)
		}
	}
}

func (c *mcpClient) request(id int, method string, params any, notify bool) error {
	req := jsonrpcMsg{JSONRPC: "2.0", Method: method}
	if !notify {
		req.ID = &id
	}
	if params != nil {
		b, _ := json.Marshal(params)
		req.Params = b
	}
	b, _ := json.Marshal(req)
	c.mu.Lock()
	_, err := c.stdin.Write(b)
	if err == nil {
		err = c.stdin.Flush()
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("mcp %s.%s write: %w", c.server.Name, method, err)
	}
	return nil
}

func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.request(id, method, params, false); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case raw := <-ch:
		var msg jsonrpcMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("mcp %s.%s: %s", c.server.Name, method, msg.Error.Message)
		}
		return msg.Result, nil
	case <-time.After(15 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s.%s 超时", c.server.Name, method)
	}
}

func (c *mcpClient) notify(method string, params any) error {
	return c.request(0, method, params, true)
}

func (c *mcpClient) close() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
}

// CloseMCPClients 关闭所有已启动的 MCP 进程。
func CloseMCPClients() {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	for _, c := range mcpClients {
		c.close()
	}
	for _, c := range mcpHTTP {
		c.close()
	}
}

// httpMCPClient 是 MCP Streamable HTTP 远程客户端（无状态 JSON-RPC over POST）。
type httpMCPClient struct {
	server MCPServer
	base   *http.Client
	nextID int
}

func newHTTPMCPClient(s MCPServer) (*httpMCPClient, error) {
	if s.URL == "" {
		return nil, fmt.Errorf("mcp %s: http url is empty", s.Name)
	}
	if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
		return nil, fmt.Errorf("mcp %s: url must start with http(s)://", s.Name)
	}
	return &httpMCPClient{
		server: s,
		base:   &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *httpMCPClient) call(method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	req := jsonrpcMsg{JSONRPC: "2.0", Method: method}
	req.ID = &id
	if params != nil {
		b, _ := json.Marshal(params)
		req.Params = b
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", c.server.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("User-Agent", "licode/0.1")
	resp, err := c.base.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp %s.%s 请求失败: %w", c.server.Name, method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp %s.%s 返回 %d: %s", c.server.Name, method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	msg, err := extractRPC(raw, id)
	if err != nil {
		return nil, fmt.Errorf("mcp %s.%s: %w", c.server.Name, method, err)
	}
	if msg.Error != nil {
		return nil, fmt.Errorf("mcp %s.%s: %s", c.server.Name, method, msg.Error.Message)
	}
	if msg.Result == nil {
		return json.RawMessage("null"), nil
	}
	return msg.Result, nil
}

func (c *httpMCPClient) close() {}

// extractRPC 从 HTTP 响应（可能为纯 JSON 或 SSE 流）中提取指定 id 的 JSON-RPC 消息。
func extractRPC(body []byte, wantID int) (*jsonrpcMsg, error) {
	trim := bytes.TrimSpace(body)
	if len(trim) > 0 && trim[0] == '{' {
		var m jsonrpcMsg
		if err := json.Unmarshal(trim, &m); err != nil {
			return nil, fmt.Errorf("invalid json: %w", err)
		}
		if m.ID != nil && *m.ID != wantID {
			return nil, fmt.Errorf("unexpected id %d (want %d)", *m.ID, wantID)
		}
		return &m, nil
	}
	// SSE：按空行分隔事件，从每个事件里拼出 data: 行，再解析 JSON，取 id 匹配者。
	events := bytes.Split(body, []byte("\n\n"))
	for _, ev := range events {
		line := strings.TrimSpace(string(ev))
		if line == "" {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(ev))
		var data strings.Builder
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "data:") {
				data.WriteString(strings.TrimPrefix(l, "data:"))
				data.WriteString("\n")
			}
		}
		d := strings.TrimSpace(data.String())
		if d == "" {
			continue
		}
		var m jsonrpcMsg
		if err := json.Unmarshal([]byte(d), &m); err != nil {
			continue
		}
		if m.ID != nil && *m.ID == wantID {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("no response with id %d in response body", wantID)
}

// RegisterMCPServers 连接所有 MCP 服务器并注册其工具（前缀 mcp__<服务器>__<工具>）。
func RegisterMCPServers(r *Registry, servers []MCPServer) error {
	for _, s := range servers {
		if s.IsHTTP() {
			if s.URL == "" {
				continue
			}
			client, err := newHTTPMCPClient(s)
			if err != nil {
				return err
			}
			if err := registerServerTools(r, s, client); err != nil {
				return err
			}
			mcpMu.Lock()
			mcpHTTP = append(mcpHTTP, client)
			mcpMu.Unlock()
			continue
		}
		if s.Command == "" {
			continue
		}
		client, err := newMCPClient(s)
		if err != nil {
			return err
		}
		if err := registerServerTools(r, s, client); err != nil {
			client.close()
			return err
		}
		mcpMu.Lock()
		mcpClients = append(mcpClients, client)
		mcpMu.Unlock()
	}
	return nil
}

type mcpCaller interface {
	call(method string, params any) (json.RawMessage, error)
}

func registerServerTools(r *Registry, s MCPServer, client mcpCaller) error {
	res, err := client.call("tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var list struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		return err
	}
	for _, t := range list.Tools {
		toolName := "mcp__" + s.Name + "__" + t.Name
		server := s
		tool := t
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		_ = r.Register(Tool{
			Name:        toolName,
			Description: "MCP 工具 " + server.Name + "/" + tool.Name + "：" + tool.Description,
			Schema:      map[string]any{"type": "object"},
			Run: func(ctx context.Context, args map[string]any) (string, error) {
				res, err := client.call("tools/call", map[string]any{
					"name": tool.Name, "arguments": args,
				})
				if err != nil {
					return "", err
				}
				var out struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				}
				_ = json.Unmarshal(res, &out)
				var sb strings.Builder
				for _, c := range out.Content {
					if c.Text != "" {
						sb.WriteString(c.Text)
						sb.WriteString("\n")
					}
				}
				if sb.Len() == 0 {
					return string(res), nil
				}
				return strings.TrimSpace(sb.String()), nil
			},
		})
	}
	return nil
}
