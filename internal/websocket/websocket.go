// Package websocket implements the connection hub used by both the web UI
// and the remote TUI. Each connected client owns an agent + session on the
// server; user messages trigger agent runs whose events are streamed back
// over the socket.
package websocket

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Message types (client -> server).
const (
	TypeMessage        = "message" // {content}
	TypePing           = "ping"
	TypeSettingsGet    = "settings_get"
	TypeSettingsSet    = "settings_set"
	TypeAskReply       = "ask_reply"
	TypeInterrupt      = "interrupt"
	TypeSessionsGet    = "sessions_get"
	TypeSessionNew     = "session_new"
	TypeSessionSwitch  = "session_switch"  // {session_id}
	TypeSessionRename  = "session_rename"  // {session_id, content}
	TypeSessionDelete  = "session_delete"  // {session_id}
	TypeSessionBranch  = "session_branch"  // {session_id, index, content}
	TypeSessionHistory = "session_history" // {session_id} 请求某会话的完整历史消息
	// 审计完成后，前端把摘要通过该类型提交，服务端追加为一条助手消息。
	TypeAuditLog = "audit_log" // {content: 摘要文本}
)

// Event types (server -> client), mirroring agent.Event.
const (
	EvtDelta     = "delta"
	EvtToolStart = "tool_start"
	EvtToolDone  = "tool_done"
	EvtDone      = "done"
	EvtError     = "error"
	EvtStatus    = "status"
	EvtSettings  = "settings"
	EvtAsk       = "ask"
	EvtSessions  = "sessions"
	EvtStats     = "stats"
	// EvtHistory 回放某个会话的完整历史消息（存放于 ~/.licode/sessions/*.json）。
	EvtHistory = "history"
	// EvtAuditLog 把审计结果/修复摘要推送到所有页面（含后台监听）。
	EvtAuditLog = "audit_log"
)

// Broadcast 向所有已连接客户端发送事件（审计完成通知等）。
func (h *Hub) Broadcast(ev ServerEvent) {
	h.mu.Lock()
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.SendEvent(ev)
	}
}

// ServerEvent is a JSON event streamed to clients.
type ServerEvent struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	ToolArgs  string `json:"toolArgs,omitempty"`
	ToolOut   string `json:"toolOut,omitempty"`
	Error     string `json:"error,omitempty"`
	Settings  any    `json:"settings,omitempty"`
	Sessions  any    `json:"sessions,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Stats     any    `json:"stats,omitempty"`
	// Messages 携带会话历史（EvtHistory）。
	Messages any `json:"messages,omitempty"`
	// AskID 标识一次待确认的工具调用。
	AskID string `json:"askId,omitempty"`
}

// ClientMessage is a request sent from a client.
type ClientMessage struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	// System 可选：来自当前"角色"的系统提示词覆盖。非空时前置到默认系统提示词。
	System   string `json:"system,omitempty"`
	Settings any    `json:"settings,omitempty"`
	// AskReply 对应 AskID 的确认结果。
	AskID      string `json:"askId,omitempty"`
	AskApprove bool   `json:"askApprove,omitempty"`
	AskAlways  bool   `json:"askAlways,omitempty"`
	SessionID    string        `json:"sessionId,omitempty"`
	Index        int           `json:"index,omitempty"` // {session_branch} 分支点消息序号
	Attachments  []Attachment  `json:"attachments,omitempty"`
}

// Attachment 是多模态附件（图片/文件），base64 编码。
type Attachment struct {
	Type     string `json:"type"`      // "image" 或 "file"
	MIMEType string `json:"mime_type"` // 如 "image/png"
	Data     string `json:"data"`      // base64 编码
	Filename string `json:"filename,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// licode 为本地自托管工具，可能经代理/局域网 IP/反代访问，
		// 此时 Origin 与服务端看到的 Host 常不一致。放宽源校验以避免
		// WebSocket 被误拒（表现为前端一直"已断开"、无法新建对话）。
		return true
	},
}

// Handler is called with each client that connects. The Hub does not know
// about agents; the server wires them in via this callback.
type Handler func(ctx context.Context, c *Client)

// Hub tracks connected clients and dispatches connections to a Handler.
type Hub struct {
	mu      sync.Mutex
	clients map[*Client]struct{}
	onConn  Handler
}

func NewHub() *Hub {
	return &Hub{clients: map[*Client]struct{}{}}
}

// OnConnect registers the per-connection handler.
func (h *Hub) OnConnect(fn Handler) {
	h.onConn = fn
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// ServeWS upgrades an HTTP request and runs the client lifecycle.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	c := NewClient(h, conn)
	h.register(c)

	ctx, cancel := context.WithCancel(r.Context())
	go c.writePump(ctx)
	go c.processMessages(ctx)

	if h.onConn != nil {
		go h.onConn(ctx, c)
	}
	c.readPump(ctx)

	cancel()
	c.cancel()
	h.unregister(c)
}

// Client represents one WebSocket connection. It has its own send queue so
// slow clients never block the agent goroutine.
type Client struct {
	hub           *Hub
	conn          *websocket.Conn
	send          chan []byte
	mu            sync.Mutex
	onUserMessage func(ctx context.Context, msg ClientMessage)
	msgQueue      chan ClientMessage
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		msgQueue: make(chan ClientMessage, 50),
		ctx:      ctx,
		cancel:   cancel,
	}
	return c
}

// SendEvent marshals and queues an event for delivery.
func (c *Client) SendEvent(evt ServerEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		// Client is backed up; drop rather than block the agent.
	}
}

// SendRaw queues pre-marshaled bytes.
func (c *Client) SendRaw(data []byte) {
	select {
	case c.send <- data:
	default:
	}
}

func (c *Client) writePump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.TextMessage, data)
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (c *Client) readPump(ctx context.Context) {
	defer c.conn.Close()
	defer c.cancel()
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		select {
		case c.msgQueue <- msg:
		default:
			c.send <- mustMarshal(ServerEvent{
				Type:  EvtError,
				Error: "消息队列已满，请稍候",
			})
		}
	}
}

func (c *Client) processMessages(ctx context.Context) {
	for msg := range c.msgQueue {
		if c.onUserMessage != nil {
			c.onUserMessage(ctx, msg)
		}
	}
}

func mustMarshal(evt ServerEvent) []byte {
	data, _ := json.Marshal(evt)
	return data
}

// OnUserMessage lets the server attach a handler for every client message.
func (c *Client) OnUserMessage(fn func(ctx context.Context, msg ClientMessage)) {
	c.onUserMessage = fn
}
