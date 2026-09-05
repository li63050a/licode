// Package agent 提供 MCP (Model Context Protocol) 客户端管理。
//
// 支持两种传输：
//   - stdio: 本地子进程，通过 stdin/stdout 以 Content-Length 帧交换 JSON-RPC
//   - http:  远程 MCP 服务器，通过 POST 交换 JSON-RPC（支持 Streamable HTTP / SSE）
//
// 与旧实现的区别：无全局可变状态、Content-Length 帧解析、连接复用、真实 schema 保留。
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
	"strconv"
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

func (s MCPServer) IsHTTP() bool {
	return strings.EqualFold(strings.TrimSpace(s.Type), "http")
}

// MCPManager 管理一组 MCP 连接，可复用、可并发安全关闭。
type MCPManager struct {
	mu    sync.Mutex
	conns []mcpConn
}

// NewMCPManager 创建一个空的 MCP 管理器。
func NewMCPManager() *MCPManager {
	m := &MCPManager{conns: make([]mcpConn, 0, 4)}
	registerManager(m)
	return m
}

// Register 连接所有 MCP 服务器并将其工具注册到 registry。
// 工具名格式：mcp__<服务器名>__<工具名>。
func (m *MCPManager) Register(registry *Registry, servers []MCPServer) error {
	for _, s := range servers {
		if s.IsHTTP() {
			if strings.TrimSpace(s.URL) == "" {
				continue
			}
			conn, err := newHTTPConn(s)
			if err != nil {
				m.Close()
				return fmt.Errorf("mcp %s: %w", s.Name, err)
			}
			m.add(conn)
			if err := registerTools(registry, s, conn); err != nil {
				m.Close()
				return err
			}
			continue
		}
		if strings.TrimSpace(s.Command) == "" {
			continue
		}
		conn, err := newStdioConn(s)
		if err != nil {
			m.Close()
			return fmt.Errorf("mcp %s: %w", s.Name, err)
		}
		m.add(conn)
		if err := registerTools(registry, s, conn); err != nil {
			m.Close()
			return err
		}
	}
	return nil
}

func (m *MCPManager) add(c mcpConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns = append(m.conns, c)
}

// Close 关闭所有 MCP 连接。并发安全，可多次调用。
func (m *MCPManager) Close() {
	m.mu.Lock()
	conns := m.conns
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
	unregisterManager(m)
}

// mcpConn 是 MCP 客户端连接的统一抽象。
type mcpConn interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	close()
}

// ---- JSON-RPC 消息 ----

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

func jsonrpcError(name, method string, msg jsonrpcMsg) error {
	if msg.Error == nil {
		return nil
	}
	return fmt.Errorf("mcp %s.%s: jsonrpc error %d: %s", name, method, msg.Error.Code, msg.Error.Message)
}

// ---- stdio 连接 ----

type stdioConn struct {
	server  MCPServer
	cmd     *exec.Cmd
	mu      sync.Mutex
	pending map[int]chan json.RawMessage
	nextID  int
	stdin   *bufio.Writer
	stdout  *bufio.Reader
	closeCh chan struct{}
}

func newStdioConn(s MCPServer) (*stdioConn, error) {
	cmd := exec.Command(s.Command, s.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动失败: %w", err)
	}
	c := &stdioConn{
		server:  s,
		cmd:     cmd,
		stdin:   bufio.NewWriter(stdin),
		stdout:  bufio.NewReader(stdout),
		pending: make(map[int]chan json.RawMessage),
		closeCh: make(chan struct{}),
	}
	go c.readLoop()
	if err := c.init(); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *stdioConn) init() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "licode", "version": "0.1"},
	}); err != nil {
		return err
	}
	_ = c.notify("notifications/initialized", map[string]any{})
	return nil
}

// readLoop 持续从 stdout 读取 Content-Length 帧或 NDJSON，分发到 pending。
func (c *stdioConn) readLoop() {
	for {
		select {
		case <-c.closeCh:
			return
		default:
		}
		payload, err := readMessage(c.stdout)
		if err != nil {
			return
		}
		var msg jsonrpcMsg
		if json.Unmarshal(payload, &msg) != nil || msg.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*msg.ID]
		delete(c.pending, *msg.ID)
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- payload:
			default:
			}
		}
	}
}

// readMessage 同时支持两种格式：
//   - Content-Length: N\r\n\r\n{json}（MCP 标准）
//   - {json}\n（NDJSON，部分 MCP server 使用）
func readMessage(r *bufio.Reader) ([]byte, error) {
	peek, err := r.Peek(1)
	if err != nil {
		return nil, err
	}
	if peek[0] != 'C' {
		// NDJSON: 一行一个 JSON
		line, err := r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		return bytes.TrimSpace(line), nil
	}
	// Content-Length 帧
	var header strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		header.WriteString(line)
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	h := header.String()
	idx := strings.Index(h, "Content-Length:")
	if idx < 0 {
		return nil, fmt.Errorf("缺少 Content-Length")
	}
	n, err := strconv.Atoi(strings.TrimSpace(h[idx+len("Content-Length:"):]))
	if err != nil || n <= 0 || n > 8<<20 {
		return nil, fmt.Errorf("无效 Content-Length: %w", err)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(body), nil
}

func (c *stdioConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(id, method, params, false); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s.%s write: %w", c.server.Name, method, err)
	}

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case raw := <-ch:
		var msg jsonrpcMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}
	if err := jsonrpcError(c.server.Name, method, msg); err != nil {
			return nil, err
		}
		return msg.Result, nil
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s.%s 超时", c.server.Name, method)
	case <-c.closeCh:
		return nil, fmt.Errorf("mcp %s 已关闭", c.server.Name)
	}
}

func (c *stdioConn) notify(method string, params any) error {
	return c.write(0, method, params, true)
}

func (c *stdioConn) write(id int, method string, params any, notify bool) error {
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
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	if _, err := c.stdin.Write(b); err != nil {
		return err
	}
	return c.stdin.Flush()
}

func (c *stdioConn) close() {
	select {
	case <-c.closeCh:
		return
	default:
		close(c.closeCh)
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
}

// ---- HTTP 连接 ----

type httpConn struct {
	server  MCPServer
	baseURL string
	client  *http.Client
	nextID  int
}

func newHTTPConn(s MCPServer) (*httpConn, error) {
	url := strings.TrimSpace(s.URL)
	if url == "" {
		return nil, fmt.Errorf("url 为空")
	}
	return &httpConn{
		server:  s,
		baseURL: url,
		client:  &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *httpConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	req := jsonrpcMsg{JSONRPC: "2.0", Method: method, ID: &id}
	if params != nil {
		b, _ := json.Marshal(params)
		req.Params = b
	}
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp %s.%s 请求失败: %w", c.server.Name, method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp %s.%s 返回 %d: %s", c.server.Name, method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	msg, err := extractRPC(raw, id)
	if err != nil {
		return nil, fmt.Errorf("mcp %s.%s: %w", c.server.Name, method, err)
	}
	if err := jsonrpcError(c.server.Name, method, *msg); err != nil {
		return nil, err
	}
	if msg.Result == nil {
		return json.RawMessage("null"), nil
	}
	return msg.Result, nil
}

func (c *httpConn) close() {}

// extractRPC 从 HTTP 响应（纯 JSON 或 SSE 流）中提取指定 id 的 JSON-RPC 消息。
func extractRPC(body []byte, wantID int) (*jsonrpcMsg, error) {
	trim := bytes.TrimSpace(body)
	if len(trim) > 0 && trim[0] == '{' {
		var m jsonrpcMsg
		if err := json.Unmarshal(trim, &m); err != nil {
			return nil, fmt.Errorf("invalid json: %w", err)
		}
		return &m, nil
	}
	events := bytes.Split(body, []byte("\n\n"))
	for _, ev := range events {
		var data strings.Builder
		sc := bufio.NewScanner(bytes.NewReader(ev))
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
	return nil, fmt.Errorf("no response with id %d", wantID)
}

// ---- 工具注册 ----

func registerTools(r *Registry, s MCPServer, client mcpConn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := client.call(ctx, "tools/list", map[string]any{})
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
		var schema map[string]any
		if err := json.Unmarshal(t.InputSchema, &schema); err != nil || len(schema) == 0 {
			schema = map[string]any{"type": "object"}
		}
		tool := t
		server := s
		_ = r.Register(Tool{
			Name:        toolName,
			Description: "MCP 工具 " + server.Name + "/" + tool.Name + "：" + tool.Description,
			Schema:      schema,
			Run: func(ctx context.Context, args map[string]any) (string, error) {
				callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				defer cancel()
				res, err := client.call(callCtx, "tools/call", map[string]any{
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

// RegisterMCPServers 是兼容旧 API 的便捷函数。
func RegisterMCPServers(r *Registry, servers []MCPServer) (*MCPManager, error) {
	mgr := NewMCPManager()
	if err := mgr.Register(r, servers); err != nil {
		return nil, err
	}
	return mgr, nil
}

// ---- 全局管理器注册（用于 CloseMCPClients） ----

var (
	mgrMu   sync.Mutex
	allMgrs []*MCPManager
)

func registerManager(m *MCPManager) {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	allMgrs = append(allMgrs, m)
}

func unregisterManager(m *MCPManager) {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	for i, mm := range allMgrs {
		if mm == m {
			allMgrs = append(allMgrs[:i], allMgrs[i+1:]...)
			return
		}
	}
}

// CloseMCPClients 关闭所有已创建的 MCP 管理器（兼容旧 API）。
func CloseMCPClients() {
	mgrMu.Lock()
	mgrs := make([]*MCPManager, len(allMgrs))
	copy(mgrs, allMgrs)
	mgrMu.Unlock()
	for _, m := range mgrs {
		m.Close()
	}
}

// Presets 是内置的常见 MCP 服务器预设，用户可直接添加而无需手写配置。
var Presets = map[string]MCPServer{
	"filesystem": {
		Name:    "filesystem",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/"},
	},
	"git": {
		Name:    "git",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-git"},
	},
	"github": {
		Name:    "github",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-github"},
	},
	"postgres": {
		Name:    "postgres",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-postgres"},
	},
	"sqlite": {
		Name:    "sqlite",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-sqlite"},
	},
	"memory": {
		Name:    "memory",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-memory"},
	},
	"puppeteer": {
		Name:    "puppeteer",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-puppeteer"},
	},
	"brave-search": {
		Name:    "brave-search",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-brave-search"},
	},
	"fetch": {
		Name:    "fetch",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-fetch"},
	},
}
