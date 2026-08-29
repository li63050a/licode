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
	TypeMessage       = "message" // {content}
	TypePing          = "ping"
	TypeSettingsGet   = "settings_get"
	TypeSettingsSet   = "settings_set"
	TypeAskReply      = "ask_reply"
	TypeSessionsGet   = "sessions_get"
	TypeSessionNew    = "session_new"
	TypeSessionSwitch = "session_switch" // {session_id}
	TypeSessionRename = "session_rename" // {session_id, content}
	TypeSessionDelete = "session_delete" // {session_id}
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
)

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
	// AskID 标识一次待确认的工具调用。
	AskID string `json:"askId,omitempty"`
}

// ClientMessage is a request sent from a client.
type ClientMessage struct {
	Type     string `json:"type"`
	Content  string `json:"content,omitempty"`
	Settings any    `json:"settings,omitempty"`
	// AskReply 对应 AskID 的确认结果。
	AskID      string `json:"askId,omitempty"`
	AskApprove bool   `json:"askApprove,omitempty"`
	AskAlways  bool   `json:"askAlways,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // accept both web page and TUI thin-client origins
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

	if h.onConn != nil {
		go h.onConn(ctx, c)
	}
	c.readPump(ctx)

	cancel()
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
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
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
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if c.onUserMessage != nil {
			go c.onUserMessage(ctx, msg)
		}
	}
}

// OnUserMessage lets the server attach a handler for every client message.
func (c *Client) OnUserMessage(fn func(ctx context.Context, msg ClientMessage)) {
	c.onUserMessage = fn
}
