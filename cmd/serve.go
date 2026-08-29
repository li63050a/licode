// Package cmd holds the cobra subcommands: tui and serve.
package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"licode/internal/agent"
	"licode/internal/ai"
	"licode/internal/web"
	"licode/internal/websocket"
)

// ServeOptions holds resolved configuration for the serve command.
type ServeOptions struct {
	Addr       string
	ConfigPath string
	Provider   string
	BaseURL    string
	APIKey     string
	Model      string
	NoSubAgents bool
}

func newServeCmd() *cobra.Command {
	opts := &ServeOptions{}
	c := &cobra.Command{
		Use:   "serve",
		Short: "Start the licode web server with a WebSocket endpoint",
		Long: `Start licode's HTTP + WebSocket server.

Clients can connect from a browser (http://<host>:<port>) or from a remote
TUI thin client (licode tui --remote ws://<host>:<port>/ws).

All AI inference happens on this server; each connection owns an agent and a
session, so every client gets an isolated conversation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(opts)
		},
	}
	f := c.Flags()
	f.StringVarP(&opts.Addr, "addr", "a", ":8080", "listen address")
	f.StringVar(&opts.ConfigPath, "config", "", "path to config file")
	f.StringVar(&opts.Provider, "provider", "", "AI provider: openai | claude | ollama")
	f.StringVar(&opts.BaseURL, "base-url", "", "provider API base URL")
	f.StringVar(&opts.APIKey, "api-key", "", "provider API key")
	f.StringVar(&opts.Model, "model", "", "model name")
	f.BoolVar(&opts.NoSubAgents, "no-subagents", false, "disable sub-agent orchestration")
	return c
}

// NewServeCommand 返回 serve 子命令。
func NewServeCommand() *cobra.Command { return newServeCmd() }

func runServe(opts *ServeOptions) error {
	cfg, err := ai.LoadConfig(ai.Config{
		Provider: opts.Provider,
		BaseURL:  opts.BaseURL,
		APIKey:   opts.APIKey,
		Model:    opts.Model,
	}, opts.ConfigPath)
	if err != nil {
		return err
	}
	client, err := ai.New(cfg)
	if err != nil {
		return err
	}

	hub := websocket.NewHub()
	hub.OnConnect(func(ctx context.Context, c *websocket.Client) {
		log.Printf("client connected (%d total)", hub.Count())
		ag := agent.NewAgent(client, agent.DefaultMainPrompt)
		if !opts.NoSubAgents {
			ag.RegisterSubAgents(agent.DefaultSubAgentSpecs(client))
		}
		var mu sync.Mutex
		c.OnUserMessage(func(ctx context.Context, content string) {
			mu.Lock()
			defer mu.Unlock()
			if content == "/clear" {
				ag.Session.Clear()
				c.SendEvent(websocket.ServerEvent{Type: websocket.EvtDone})
				return
			}
			err := ag.Run(ctx, content, func(e agent.Event) {
				evt := websocket.ServerEvent{
					Type:     mapEventType(e.Type),
					Content:  e.Content,
					ToolName: e.ToolName,
					ToolArgs: e.ToolArgs,
					ToolOut:  e.ToolOut,
					Error:    e.Error,
				}
				c.SendEvent(evt)
			})
			if err != nil && ctx.Err() == nil {
				c.SendEvent(websocket.ServerEvent{Type: websocket.EvtError, Error: err.Error()})
			}
		})
	})

	sub, err := fs.Sub(web.FS, "static")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/ws", hub.ServeWS)

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
	log.Printf("licode serve listening on %s", url)
	log.Printf("provider=%s model=%s  (web UI: %s | remote TUI: licode tui --remote %s)", cfg.Provider, cfg.Model, url, wsURL)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("shutting down…")
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
	default:
		return websocket.EvtStatus
	}
}