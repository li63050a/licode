package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// MCPServer 描述一个 MCP 服务器（stdio 进程）。
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// 包级跟踪所有 mcp 进程，便于统一关闭。
var (
	mcpMu      sync.Mutex
	mcpClients []*mcpClient
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
	cmd := exec.Command(s.Command, s.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s 启动失败: %w", s.Name, err)
	}
	c := &mcpClient{
		server:  s,
		cmd:     cmd,
		stdin:   bufio.NewWriter(stdin),
		pending: map[int]chan json.RawMessage{},
	}
	go c.readLoop(stdout)
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

func (c *mcpClient) readLoop(stdout interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
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
	_, err := c.stdin.Write(append(b, '\n'))
	if err == nil {
		err = c.stdin.Flush()
	}
	c.mu.Unlock()
	return err
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
}

// RegisterMCPServers 连接所有 MCP 服务器并注册其工具（前缀 mcp__<服务器>__<工具>）。
func RegisterMCPServers(r *Registry, servers []MCPServer) error {
	for _, s := range servers {
		if s.Command == "" {
			continue
		}
		client, err := newMCPClient(s)
		if err != nil {
			return err
		}
		res, err := client.call("tools/list", map[string]any{})
		if err != nil {
			client.close()
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
			client.close()
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
	}
	return nil
}
