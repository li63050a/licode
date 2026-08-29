package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"licode/internal/agent"
	"licode/internal/ai"
	"licode/internal/session"
	"licode/internal/settings"
	"licode/internal/web"
	"licode/internal/websocket"
)

// ServeOptions holds resolved configuration for the serve command.
type ServeOptions struct {
	Addr       string
	Provider   string
	BaseURL    string
	APIKey     string
	Model      string
	NoSubAgents bool
	Username   string
	Password   string
}

// NewServeCommand 返回 serve 子命令。
func NewServeCommand() *cobra.Command { return newServeCmd() }

func newServeCmd() *cobra.Command {
	opts := &ServeOptions{}
	c := &cobra.Command{
		Use:   "serve",
		Short: "启动 Web 服务器（浏览器 + 远程 TUI 均可连接）",
		Long: `启动 licode 的 HTTP + WebSocket 服务器。

浏览器访问 http://<host>:<port> 使用网页界面；
另一台设备用 licode tui --remote ws://<host>:<port>/ws 远程连接。

所有 AI 推理都在本服务器执行。设置（提供商/模型/密钥等）可在网页端或
远程 TUI 的设置界面中实时修改，无需配置文件。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(opts)
		},
	}
	f := c.Flags()
	f.StringVarP(&opts.Addr, "addr", "a", ":8080", "监听地址")
	f.StringVar(&opts.Provider, "provider", "", "AI 提供商: openai | claude | ollama | gemini")
	f.StringVar(&opts.BaseURL, "base-url", "", "提供商 API 地址")
	f.StringVar(&opts.APIKey, "api-key", "", "API 密钥")
	f.StringVar(&opts.Model, "model", "", "模型名")
	f.BoolVar(&opts.NoSubAgents, "no-subagents", false, "禁用子代理编排")
	f.StringVar(&opts.Username, "username", "", "访问用户名（默认 licode；环境变量 LICODE_USERNAME）")
	f.StringVar(&opts.Password, "password", "", "访问密码（环境变量 LICODE_PASSWORD）；未设置则不启用认证")
	return c
}

// serverState 持有可变的全局设置与当前客户端。
type serverState struct {
	mu       sync.RWMutex
	settings settings.Settings
	client   ai.LLMClient
}

// connState 保存每个连接独立的会话与待确认的工具调用。
type connState struct {
	mu      sync.Mutex
	session *session.Session
	pending map[string]chan bool
	askSeq  atomic.Int64
	busy    bool
}

func newConnState() *connState {
	return &connState{
		session: session.NewSession(0),
		pending: map[string]chan bool{},
	}
}

func runServe(opts *ServeOptions) error {
	st := &serverState{}
	st.settings = settings.Defaults()
	st.settings.ApplyFlags(opts.Provider, opts.BaseURL, opts.APIKey, opts.Model, opts.NoSubAgents)
	client, err := st.settings.NewClient()
	if err != nil {
		return err
	}
	st.client = client

	hub := websocket.NewHub()
	hub.OnConnect(func(ctx context.Context, c *websocket.Client) {
		log.Printf("客户端已连接（当前 %d 个）", hub.Count())
		cs := newConnState()

		c.OnUserMessage(func(ctx context.Context, msg websocket.ClientMessage) {
			switch msg.Type {
			case websocket.TypeSettingsGet:
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSettings, Settings: st.settings.Snapshot(),
				})

			case websocket.TypeSettingsSet:
				if err := applyServerSettings(st, msg); err != nil {
					c.SendEvent(websocket.ServerEvent{Type: websocket.EvtError, Error: err.Error()})
				}
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSettings, Settings: st.settings.Snapshot(),
				})

			case websocket.TypeAskReply:
				cs.mu.Lock()
				ch, ok := cs.pending[msg.AskID]
				if ok {
					delete(cs.pending, msg.AskID)
				}
				cs.mu.Unlock()
				if ok {
					ch <- msg.AskApprove
				}

			case websocket.TypeMessage:
				if msg.Content == "/clear" {
					cs.session.Clear()
					c.SendEvent(websocket.ServerEvent{Type: websocket.EvtDone})
					return
				}
				cs.mu.Lock()
				if cs.busy {
					cs.mu.Unlock()
					c.SendEvent(websocket.ServerEvent{Type: websocket.EvtError, Error: "上一条消息仍在处理中，请稍候"})
					return
				}
				cs.busy = true
				cs.mu.Unlock()
				defer func() {
					cs.mu.Lock()
					cs.busy = false
					cs.mu.Unlock()
				}()
				runServerAgent(ctx, st, cs, c, msg.Content)
			}
		})
	})

	sub, err := fs.Sub(web.FS, "static")
	if err != nil {
		return err
	}

	authUser, authPass, authEnabled := ResolveAuth(opts.Username, opts.Password)

	fileServer := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r, authUser, authPass, authEnabled) {
			unauthorized(w)
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r, authUser, authPass, authEnabled) {
			unauthorized(w)
			return
		}
		hub.ServeWS(w, r)
	})

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	host := opts.Addr
	if strings.HasPrefix(host, ":") {
		host = "0.0.0.0" + host
	}
	url := "http://" + host + "/"
	wsURL := "ws://" + strings.TrimPrefix(host, "0.0.0.0") + "/ws"
	log.Printf("licode serve 已启动: %s", url)
	log.Printf("provider=%s model=%s （网页端: %s | 远程 TUI: licode tui --remote %s）",
		st.settings.Provider, st.settings.Model, url, wsURL)
	if authEnabled {
		log.Printf("认证已启用：用户名 %s（请用用户名密码访问网页/远程）", authUser)
	} else {
		log.Printf("认证未启用（可用 --password 或环境变量 %s 开启）", EnvPassword)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("正在关闭…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	err = srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// applyServerSettings 校验并应用新的设置，重建客户端。
func applyServerSettings(st *serverState, msg websocket.ClientMessage) error {
	data, err := json.Marshal(msg.Settings)
	if err != nil {
		return fmt.Errorf("设置格式错误: %w", err)
	}
	var s settings.Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("设置格式错误: %w", err)
	}
	if err := s.Validate(); err != nil {
		return fmt.Errorf("设置无效: %v", err)
	}
	client, err := s.NewClient()
	if err != nil {
		return err
	}
	st.mu.Lock()
	st.settings = s.Snapshot()
	st.client = client
	st.mu.Unlock()
	return nil
}

// runServerAgent 在当前设置下运行一次 Agent，流式回传事件。
func runServerAgent(ctx context.Context, st *serverState, cs *connState, c *websocket.Client, content string) {
	st.mu.RLock()
	s := st.settings.Snapshot()
	client := st.client
	st.mu.RUnlock()

	ag := s.BuildAgent(client)
	ag.Session = cs.session
	ag.Ask = func(ctx context.Context, toolName, args string) (bool, error) {
		askID := fmt.Sprintf("ask-%d", cs.askSeq.Add(1))
		ch := make(chan bool, 1)
		cs.mu.Lock()
		cs.pending[askID] = ch
		cs.mu.Unlock()
		c.SendEvent(websocket.ServerEvent{
			Type: websocket.EvtAsk, ToolName: toolName, ToolArgs: args, AskID: askID,
		})
		select {
		case ok := <-ch:
			return ok, nil
		case <-ctx.Done():
			cs.mu.Lock()
			delete(cs.pending, askID)
			cs.mu.Unlock()
			return false, ctx.Err()
		}
	}

	err := ag.Run(ctx, content, func(e agent.Event) {
		c.SendEvent(websocket.ServerEvent{
			Type:     mapEventType(e.Type),
			Content:  e.Content,
			ToolName: e.ToolName,
			ToolArgs: e.ToolArgs,
			ToolOut:  e.ToolOut,
			Error:    e.Error,
		})
	})
	if err != nil && ctx.Err() == nil {
		c.SendEvent(websocket.ServerEvent{Type: websocket.EvtError, Error: err.Error()})
	}
}

func mapEventType(t agent.EventType) string {
	switch t {
	case agent.EventText:
		return websocket.EvtDelta
	case agent.EventToolStart:
		return websocket.EvtToolStart
	case agent.EventToolDone:
		return websocket.EvtToolDone
	case agent.EventDone:
		return websocket.EvtDone
	case agent.EventError:
		return websocket.EvtError
	case agent.EventStatus:
		return websocket.EvtStatus
	case agent.EventAsk:
		return websocket.EvtAsk
	default:
		return websocket.EvtStatus
	}
}