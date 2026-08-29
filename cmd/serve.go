package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"licode/internal/version"
	"licode/internal/web"
	"licode/internal/websocket"
)

// ServeOptions holds resolved configuration for the serve command.
type ServeOptions struct {
	Addr        string
	Host        string
	Port        int
	Provider    string
	BaseURL     string
	APIKey      string
	Model       string
	NoSubAgents bool
	Username    string
	Password    string
	HTTPS       bool
	TLSCert     string
	TLSKey      string
}

// NewServeCommand 返回根命令（licode 直接运行即启动服务器）。
func NewServeCommand() *cobra.Command { return newServeCmd() }

func newServeCmd() *cobra.Command {
	opts := &ServeOptions{}
	c := &cobra.Command{
		Use:   "licode",
		Short: "AI 编程助手（Web 界面）",
		Long: `licode —— AI 编程助手（Web 界面）。

直接运行 ./licode 即启动 Web 服务器，参数直接跟在其后，例如：
    ./licode --host 0.0.0.0 --port 8080
    ./licode --password 你的密码            （设置后启用登录，默认用户名 licode）
    ./licode --provider ollama

浏览器访问 http://<host>:<port> 即可使用，支持手机/电脑。
所有 AI 推理都在本服务器执行。设置可在网页端实时修改并写回
~/.licode/config.json，无需重启。`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(opts)
		},
	}
	f := c.Flags()
	f.StringVar(&opts.Addr, "addr", "", "监听地址（如 0.0.0.0:8080）；不填则用 --host/--port")
	f.StringVar(&opts.Host, "host", "127.0.0.1", "监听主机（默认 127.0.0.1，局域网/手机访问用 0.0.0.0）")
	f.IntVar(&opts.Port, "port", 8080, "监听端口")
	f.StringVar(&opts.Provider, "provider", "", "AI 提供商: openai | claude | ollama | gemini")
	f.StringVar(&opts.BaseURL, "base-url", "", "提供商 API 地址")
	f.StringVar(&opts.APIKey, "api-key", "", "API 密钥")
	f.StringVar(&opts.Model, "model", "", "模型名")
	f.BoolVar(&opts.NoSubAgents, "no-subagents", false, "禁用子代理编排")
	f.StringVar(&opts.Username, "username", "", "登录用户名（默认 licode；环境变量 LICODE_USERNAME）")
	f.StringVar(&opts.Password, "password", "", "登录密码（环境变量 LICODE_PASSWORD）；未设置则不启用登录")
	f.BoolVar(&opts.HTTPS, "https", false, "启用 HTTPS（未指定证书时自动生成自签名证书）")
	f.StringVar(&opts.TLSCert, "tls-cert", "", "TLS 证书文件路径（cert.pem）")
	f.StringVar(&opts.TLSKey, "tls-key", "", "TLS 私钥文件路径（key.pem）")
	return c
}

// listenAddr 计算实际监听地址：--addr 优先，否则 host:port。
func listenAddr(opts *ServeOptions) string {
	if opts.Addr != "" {
		return opts.Addr
	}
	return fmt.Sprintf("%s:%d", opts.Host, opts.Port)
}

// serverState 持有可变的全局设置与当前客户端。
type serverState struct {
	mu       sync.RWMutex
	settings settings.Settings
	client   ai.LLMClient
}

// connState 保存每个连接独立的会话（多对话）与待确认的工具调用。
type connState struct {
	mu       sync.Mutex
	sessions *session.Manager
	pending  map[string]chan bool
	askTool  map[string]string // askID -> 工具名
	askSeq   atomic.Int64
	busy     bool
}

func newConnState(sessionsDir string) *connState {
	return &connState{
		sessions: session.NewManager(sessionsDir, false),
		pending:  map[string]chan bool{},
		askTool:  map[string]string{},
	}
}

func runServe(opts *ServeOptions) error {
	// 首次使用自动生成 ~/.licode 数据目录，并启用日志文件
	_ = settings.EnsureDirs()
	if lf, err := settings.LogFile(); err == nil {
		defer lf.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	}

	// 版本计数递增（0.0.0.0 → … → 0.0.0.100 → 0.0.1.0）
	runVersion := version.Bump()
	log.Printf("licode 版本 %s", runVersion)

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
		cs := newConnState(settings.SessionsDir())

		c.OnUserMessage(func(ctx context.Context, msg websocket.ClientMessage) {
			switch msg.Type {
			case websocket.TypeSettingsGet:
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSettings, Settings: st.settings.Snapshot(),
				})

			case websocket.TypeSettingsSet:
				if err := applyServerSettings(st, msg); err != nil {
					c.SendEvent(websocket.ServerEvent{Type: websocket.EvtError, Error: err.Error()})
				} else {
					// 设置修改同步写回配置文件
					_ = st.settings.Save("")
				}
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSettings, Settings: st.settings.Snapshot(),
				})

			case websocket.TypeSessionsGet:
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSessions, Sessions: cs.sessions.List(), SessionID: cs.sessions.CurrentID(),
				})

			case websocket.TypeSessionNew:
				cs.sessions.New()
				_ = cs.sessions.SaveAll()
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSessions, Sessions: cs.sessions.List(), SessionID: cs.sessions.CurrentID(),
				})

			case websocket.TypeSessionSwitch:
				if cs.sessions.SetCurrent(msg.SessionID) {
					_ = cs.sessions.SaveAll()
					c.SendEvent(websocket.ServerEvent{
						Type: websocket.EvtSessions, Sessions: cs.sessions.List(), SessionID: cs.sessions.CurrentID(),
					})
				}

			case websocket.TypeSessionRename:
				cs.sessions.Rename(msg.SessionID, msg.Content)
				_ = cs.sessions.SaveAll()
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSessions, Sessions: cs.sessions.List(), SessionID: cs.sessions.CurrentID(),
				})

			case websocket.TypeSessionDelete:
				cs.sessions.Delete(msg.SessionID)
				_ = cs.sessions.SaveAll()
				c.SendEvent(websocket.ServerEvent{
					Type: websocket.EvtSessions, Sessions: cs.sessions.List(), SessionID: cs.sessions.CurrentID(),
				})

			case websocket.TypeAskReply:
				cs.mu.Lock()
				ch, ok := cs.pending[msg.AskID]
				if ok {
					delete(cs.pending, msg.AskID)
				}
				tool := cs.askTool[msg.AskID]
				delete(cs.askTool, msg.AskID)
				cs.mu.Unlock()
				if msg.AskAlways && tool != "" {
					// 当前对话始终允许该工具
					cs.sessions.Current().SetAlwaysAllowed(tool)
				}
				if ok {
					ch <- msg.AskApprove
				}

			case websocket.TypeMessage:
				if msg.Content == "/clear" {
					cs.sessions.Current().Clear()
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
				_ = cs.sessions.SaveAll()
			}
		})
	})

	sub, err := fs.Sub(web.FS, "static")
	if err != nil {
		return err
	}

	authUser, authPass, authEnabled := ResolveAuth(opts.Username, opts.Password)
	auth := newAuthState(authUser, authPass, authEnabled)
	wsState := newWorkspace()

	fileServer := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		auth.handleLogin(w, r)
	})
	// 文件浏览/编辑与工作目录 API
	mux.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		handleFiles(w, r, wsState)
	})
	mux.HandleFunc("/api/file", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		if r.Method == http.MethodGet {
			handleFile(w, r, wsState)
		} else {
			handleSaveFile(w, r, wsState)
		}
	})
	mux.HandleFunc("/api/mkdir", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		handleMkdir(w, r, wsState)
	})
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		handleDeleteFile(w, r, wsState)
	})
	mux.HandleFunc("/api/workspace", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		handleWorkspace(w, r, wsState)
	})
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"version": version.Current(),
			"counter": version.Parse(version.Current()),
		})
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		st.mu.RLock()
		cfg := st.settings.AIConfig()
		st.mu.RUnlock()
		// 支持参数覆盖（用于新增厂商前预览模型列表）
		q := r.URL.Query()
		if t := q.Get("type"); t != "" {
			cfg.Type = t
		}
		if b := q.Get("base"); b != "" {
			cfg.BaseURL = b
		}
		if k := q.Get("key"); k != "" {
			cfg.APIKey = k
		}
		if p := q.Get("provider"); p != "" {
			cfg.Provider = p
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		models, err := ai.ListModels(ctx, cfg)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": cfg.Provider,
			"type":     cfg.Type,
			"models":   models,
		})
	})
	mux.HandleFunc("/api/auth", func(w http.ResponseWriter, r *http.Request) {
		// 登录信息不需要认证即可查询（用于页面提示）
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":          auth.enabled,
			"username":         auth.user,
			"default_username": DefaultUsername,
		})
	})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !auth.require(w, r) {
			return
		}
		hub.ServeWS(w, r)
	})

	srv := &http.Server{
		Addr:              listenAddr(opts),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	host := listenAddr(opts)
	useTLS := opts.HTTPS || (opts.TLSCert != "" && opts.TLSKey != "")
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	// 显示地址：0.0.0.0 时给出本机可访问地址
	dispHost := host
	if strings.HasPrefix(dispHost, "0.0.0.0:") {
		dispHost = "127.0.0.1:" + strings.TrimPrefix(dispHost, "0.0.0.0:")
	}
	url := scheme + "://" + dispHost + "/"
	log.Printf("licode serve 已启动: %s", url)
	log.Printf("provider=%s model=%s", st.settings.Provider, st.settings.Model)
	if authEnabled {
		log.Printf("登录已启用：用户名 %s（浏览器打开后需登录）", authUser)
	} else {
		log.Printf("登录未启用（可用 --password 或环境变量 %s 开启）", EnvPassword)
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

	if useTLS {
		cert, key := opts.TLSCert, opts.TLSKey
		if cert == "" || key == "" {
			var err error
			cert, key, err = ensureSelfSignedCert()
			if err != nil {
				return fmt.Errorf("自动生成证书失败: %w", err)
			}
			log.Printf("已自动生成自签名证书：%s", cert)
		}
		err = srv.ListenAndServeTLS(cert, key)
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
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
	s.EnsureDefaults()
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

	sess := cs.sessions.Current()
	if s.TitleGen && sess.Title() == "新对话" {
		sess.SetTitle(autoTitle(content))
	}

	ag := s.BuildAgent(client)
	ag.Session = sess
	ag.Ask = func(ctx context.Context, toolName, args string) (bool, error) {
		// 自动允许，或本对话已"始终允许"该工具 → 不再询问
		if s.AutoAllow || sess.AlwaysAllowed(toolName) {
			return true, nil
		}
		askID := fmt.Sprintf("ask-%d", cs.askSeq.Add(1))
		ch := make(chan bool, 1)
		cs.mu.Lock()
		cs.pending[askID] = ch
		cs.askTool[askID] = toolName
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
			delete(cs.askTool, askID)
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
	// 推送会话统计（消息数 / 上下文 token 估算）
	c.SendEvent(websocket.ServerEvent{
		Type:  websocket.EvtStats,
		Stats: sessionStats(sess),
	})
	if err != nil && ctx.Err() == nil {
		c.SendEvent(websocket.ServerEvent{Type: websocket.EvtError, Error: err.Error()})
	}
}

// sessionStats 估算会话 token 用量。
func sessionStats(s *session.Session) map[string]any {
	messages := s.Messages()
	tokens := 0
	for _, m := range messages {
		tokens += session.EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			tokens += session.EstimateTokens(tc.Function.Arguments)
		}
	}
	return map[string]any{
		"messages": len(messages),
		"tokens":   tokens,
	}
}

// autoTitle 从第一条用户消息生成对话标题。
func autoTitle(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) > 18 {
		return string(runes[:18])
	}
	return string(runes)
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
