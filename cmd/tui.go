package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	"licode/internal/agent"
	"licode/internal/ai"
	wsproto "licode/internal/websocket"
)

// ---------------------------------------------------------------------------
// 命令行定义
// ---------------------------------------------------------------------------

type tuiOptions struct {
	Remote      string
	Provider    string
	BaseURL     string
	APIKey      string
	Model       string
	ConfigPath  string
	NoSubAgents bool
}

func newTUICmd() *cobra.Command {
	opts := &tuiOptions{}
	c := &cobra.Command{
		Use:   "tui",
		Short: "启动终端界面（本地或 --remote 远程瘦客户端）",
		Long: `启动 licode 的终端界面。

默认在本地运行 Agent（需要配置 AI 提供商）。
使用 --remote 连接运行中的 licode serve 服务器，此时本机只负责渲染界面，
所有 AI 推理都在服务器执行，通过 WebSocket 转发流式结果。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(opts)
		},
	}
	f := c.Flags()
	f.StringVar(&opts.Remote, "remote", "", "远程服务器地址，如 ws://192.168.1.10:8080/ws")
	f.StringVar(&opts.ConfigPath, "config", "", "配置文件路径")
	f.StringVar(&opts.Provider, "provider", "", "AI 提供商: openai | claude | ollama")
	f.StringVar(&opts.BaseURL, "base-url", "", "提供商 API 地址")
	f.StringVar(&opts.APIKey, "api-key", "", "API 密钥")
	f.StringVar(&opts.Model, "model", "", "模型名")
	f.BoolVar(&opts.NoSubAgents, "no-subagents", false, "禁用子代理编排")
	return c
}

// NewTUICommand 返回 tui 子命令。
func NewTUICommand() *cobra.Command { return newTUICmd() }

func runTUI(opts *tuiOptions) error {
	if opts.Remote != "" {
		return runRemoteTUI(opts)
	}

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

	ag := agent.NewAgent(client, agent.DefaultMainPrompt)
	if !opts.NoSubAgents {
		ag.RegisterSubAgents(agent.DefaultSubAgentSpecs(client))
	}

	m := newUIModel(&uiConfig{
		mode:     "本地",
		provider: cfg.Provider,
		model:    cfg.Model,
		agent:    ag,
	})
	return m.start()
}

// ---------------------------------------------------------------------------
// 远程瘦客户端
// ---------------------------------------------------------------------------

type thinClient struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (c *thinClient) dial(url string) error {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

func (c *thinClient) send(msg wsproto.ClientMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("未连接")
	}
	return c.conn.WriteJSON(msg)
}

func (c *thinClient) readLoop() ([]byte, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, errors.New("未连接")
	}
	_, data, err := conn.ReadMessage()
	return data, err
}

func (c *thinClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func runRemoteTUI(opts *tuiOptions) error {
	client := &thinClient{}
	host := ""
	if idx := strings.LastIndex(opts.Remote, "//"); idx >= 0 {
		host = opts.Remote[idx+2:]
		if slash := strings.Index(host, "/"); slash > 0 {
			host = host[:slash]
		}
	}
	m := newUIModel(&uiConfig{
		mode:      "远程",
		provider:  host,
		model:     "服务器",
		remote:    client,
		remoteURL: opts.Remote,
	})
	return m.start()
}

// ---------------------------------------------------------------------------
// 界面状态
// ---------------------------------------------------------------------------

type uiConfig struct {
	mode      string
	provider  string
	model     string
	agent     *agent.Agent
	remote    *thinClient
	remoteURL string
}

type toolView struct {
	name  string
	args  string
	out   string
	state string // running | done | error
}

type viewMsg struct {
	role string // user | assistant | tool | error
	text string
	tool *toolView
}

type uiModel struct {
	width, height int

	mode     string
	provider string
	model    string

	agent  *agent.Agent
	remote *thinClient
	wsURL  string

	msgs      []viewMsg
	input     string
	cursor    int
	streaming bool
	status    string

	focus  string // chat | files
	files  []string
	sel    int
	offset int // 从底部回滚的行数

	cancel   context.CancelFunc
	events   chan agent.Event
	quit     chan struct{}
	quitting bool
}

func newUIModel(cfg *uiConfig) *uiModel {
	return &uiModel{
		mode:     cfg.mode,
		provider: cfg.provider,
		model:    cfg.model,
		agent:    cfg.agent,
		remote:   cfg.remote,
		wsURL:    cfg.remoteURL,
		focus:    "chat",
		events:   make(chan agent.Event, 512),
		quit:     make(chan struct{}),
	}
}

func (m *uiModel) start() error {
	if m.remote != nil {
		go m.remoteLoop()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// remoteLoop 维持远程连接并转发事件到 events 通道。
func (m *uiModel) remoteLoop() {
	sendStatus := func(s string) {
		select {
		case m.events <- agent.Event{Type: agent.EventStatus, Content: s}:
		case <-m.quit:
		}
	}
	for {
		select {
		case <-m.quit:
			return
		default:
		}
		if err := m.remote.dial(m.wsURL); err != nil {
			sendStatus("连接失败，2 秒后重试…")
			select {
			case <-m.quit:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		sendStatus("已连接到 " + m.wsURL)
		for {
			data, err := m.remote.readLoop()
			if err != nil {
				select {
				case <-m.quit:
					return
				default:
				}
				sendStatus("连接断开，2 秒后重连…")
				break
			}
			var evt wsproto.ServerEvent
			if json.Unmarshal(data, &evt) != nil {
				continue
			}
			select {
			case m.events <- serverToAgentEvent(evt):
			case <-m.quit:
				return
			}
		}
	}
}

// serverToAgentEvent 把服务端事件转成 agent.Event。
func serverToAgentEvent(evt wsproto.ServerEvent) agent.Event {
	switch evt.Type {
	case wsproto.EvtDelta:
		return agent.Event{Type: agent.EventText, Content: evt.Content}
	case wsproto.EvtToolStart:
		return agent.Event{Type: agent.EventToolStart, ToolName: evt.ToolName, ToolArgs: evt.ToolArgs}
	case wsproto.EvtToolDone:
		return agent.Event{Type: agent.EventToolDone, ToolName: evt.ToolName, ToolOut: evt.ToolOut}
	case wsproto.EvtDone:
		return agent.Event{Type: agent.EventDone}
	case wsproto.EvtError:
		return agent.Event{Type: agent.EventError, Error: evt.Error}
	default:
		return agent.Event{Type: agent.EventStatus, Content: evt.Content}
	}
}

// ---------------------------------------------------------------------------
// bubbletea 实现
// ---------------------------------------------------------------------------

func (m *uiModel) Init() tea.Cmd {
	m.loadFiles()
	return nil
}

func (m *uiModel) loadFiles() {
	entries, err := os.ReadDir(".")
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	m.files = names
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width < 70 {
			m.focus = "chat"
		}
	case tea.KeyMsg:
		cmd = m.handleKey(msg)
	}
	for {
		select {
		case e := <-m.events:
			m.handleEvent(e)
		default:
			return m, cmd
		}
	}
}

func (m *uiModel) handleEvent(e agent.Event) {
	switch e.Type {
	case agent.EventText:
		if len(m.msgs) == 0 || m.msgs[len(m.msgs)-1].role != "assistant" {
			m.msgs = append(m.msgs, viewMsg{role: "assistant"})
		}
		m.msgs[len(m.msgs)-1].text += e.Content
	case agent.EventToolStart:
		m.status = "运行工具: " + e.ToolName
		m.msgs = append(m.msgs, viewMsg{role: "tool", tool: &toolView{
			name: e.ToolName, args: e.ToolArgs, state: "running",
		}})
	case agent.EventToolDone:
		for i := len(m.msgs) - 1; i >= 0; i-- {
			t := m.msgs[i].tool
			if t != nil && t.state == "running" {
				t.state = "done"
				t.out = e.ToolOut
				break
			}
		}
		m.status = ""
	case agent.EventDone:
		m.streaming = false
		m.status = ""
	case agent.EventError:
		m.streaming = false
		m.status = ""
		m.msgs = append(m.msgs, viewMsg{role: "error", text: "错误: " + e.Error})
	case agent.EventStatus:
		m.status = e.Content
	}
}

func (m *uiModel) submit() {
	text := strings.TrimSpace(m.input)
	if text == "" || m.streaming {
		return
	}
	if text == "/clear" {
		m.msgs = nil
		m.input = ""
		m.cursor = 0
		m.offset = 0
		if m.agent != nil {
			m.agent.Session.Clear()
		}
		m.status = "会话已清空"
		return
	}
	m.msgs = append(m.msgs, viewMsg{role: "user", text: text})
	m.input = ""
	m.cursor = 0
	m.streaming = true
	m.offset = 0

	if m.remote != nil {
		go func() {
			_ = m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeMessage, Content: text})
		}()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go func() {
		err := m.agent.Run(ctx, text, func(e agent.Event) {
			select {
			case m.events <- e:
			case <-ctx.Done():
			}
		})
		if err != nil && ctx.Err() == nil {
			select {
			case m.events <- agent.Event{Type: agent.EventError, Error: err.Error()}:
			default:
			}
		}
	}()
}

func (m *uiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		if m.streaming {
			m.cancel()
			m.streaming = false
			m.status = "已取消"
			return nil
		}
		return m.quitApp()
	case "tab":
		if m.width >= 70 {
			if m.focus == "chat" {
				m.focus = "files"
			} else {
				m.focus = "chat"
			}
		}
		return nil
	}

	if m.focus == "files" {
		return m.handleFilesKey(msg)
	}

	switch msg.Type {
	case tea.KeyRunes:
		runes := []rune(m.input)
		pos := clamp(m.cursor, 0, len(runes))
		ins := []rune(string(msg.Runes))
		runes = append(runes[:pos], append(ins, runes[pos:]...)...)
		m.input = string(runes)
		m.cursor = pos + len(ins)
	case tea.KeyBackspace:
		runes := []rune(m.input)
		if m.cursor > 0 {
			pos := clamp(m.cursor, 0, len(runes))
			runes = append(runes[:pos-1], runes[pos:]...)
			m.input = string(runes)
			m.cursor--
		}
	case tea.KeyEnter:
		m.submit()
	case tea.KeyLeft:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyRight:
		if m.cursor < runeLen(m.input) {
			m.cursor++
		}
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = runeLen(m.input)
	case tea.KeyCtrlU:
		m.input = ""
		m.cursor = 0
	case tea.KeyPgUp:
		m.offset += 12
	case tea.KeyPgDown:
		m.offset -= 12
		if m.offset < 0 {
			m.offset = 0
		}
	case tea.KeyEsc:
		if m.streaming {
			m.cancel()
			m.streaming = false
			m.status = "已取消"
		}
	}
	return nil
}

func (m *uiModel) quitApp() tea.Cmd {
	if m.quitting {
		return nil
	}
	m.quitting = true
	close(m.quit)
	if m.cancel != nil {
		m.cancel()
	}
	if m.remote != nil {
		m.remote.close()
	}
	return tea.Quit
}

func (m *uiModel) handleFilesKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "shift+tab":
		if m.sel > 0 {
			m.sel--
		}
	case "down":
		if m.sel < len(m.files)-1 {
			m.sel++
		}
	case "enter":
		if m.sel >= 0 && m.sel < len(m.files) {
			sel := strings.TrimSuffix(m.files[m.sel], "/")
			if m.input == "" {
				m.input = sel
			} else {
				m.input = m.input + " " + sel
			}
			m.cursor = runeLen(m.input)
		}
		m.focus = "chat"
	case "ctrl+c", "esc", "q":
		m.focus = "chat"
	}
	return nil
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

func (m *uiModel) View() string {
	if m.width == 0 {
		return "加载中…"
	}
	sideW := 0
	if m.width >= 70 {
		sideW = 26
	}
	mainW := m.width - sideW - 1
	if mainW < 30 {
		mainW = 30
	}

	var sb strings.Builder

	conn := "●"
	connColor := lipgloss.Color("#3fb950")
	if m.mode == "远程" {
		conn = "◌"
		connColor = lipgloss.Color("#d29922")
	}
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Bold(true).Render(" licode ")
	meta := fmt.Sprintf("%s %s · %s/%s",
		lipgloss.NewStyle().Foreground(connColor).Render(conn),
		m.mode, m.provider, m.model)
	sb.WriteString(title + lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(meta) + "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(m.statusText()))
	sb.WriteString("\n" + strings.Repeat("─", m.width) + "\n")

	bodyLines := m.height - 4
	if bodyLines < 5 {
		bodyLines = 5
	}
	mainLines := m.renderMessages(mainW)
	start := len(mainLines) - bodyLines - m.offset
	if start < 0 {
		start = 0
	}
	body := strings.Join(mainLines[start:], "\n")
	if start > 0 {
		body = "↑ 更多历史 ↑\n" + body
	}
	mainPane := lipgloss.NewStyle().Width(mainW).Render(body)

	if sideW > 0 {
		side := m.renderSidebar(sideW, bodyLines)
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, mainPane, lipgloss.NewStyle().Foreground(lipgloss.Color("#21262d")).Render("│"), side))
	} else {
		sb.WriteString(mainPane)
	}
	sb.WriteString("\n")

	sb.WriteString("> " + m.renderInput(m.width-2) + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(
		"Enter 发送 · Tab 切换文件栏 · /clear 清空 · Esc 取消 · Ctrl+C 退出"))
	return sb.String()
}

func (m *uiModel) statusText() string {
	if m.streaming && m.status == "" {
		return "思考中…"
	}
	return m.status
}

func (m *uiModel) renderSidebar(w, bodyLines int) string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render("  文件"))
	sb.WriteString("\n")
	if m.focus == "files" {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Italic(true).Render("  ↑↓选择 Enter引用 Tab返回"))
		sb.WriteString("\n")
	}
	entries := m.files
	start := 0
	if m.sel >= bodyLines-1 {
		start = m.sel - bodyLines + 2
	}
	end := minInt(len(entries), start+bodyLines)
	if start < 0 {
		start = 0
	}
	for i := start; i < end; i++ {
		name := entries[i]
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
		if strings.HasSuffix(name, "/") {
			style = style.Foreground(lipgloss.Color("#58a6ff"))
		}
		if m.focus == "files" && i == m.sel {
			style = style.Background(lipgloss.Color("#21262d")).Bold(true)
			sb.WriteString("▸" + style.Render(name))
		} else {
			sb.WriteString(" " + style.Render(name))
		}
		sb.WriteString("\n")
	}
	if len(entries) == 0 {
		sb.WriteString("  (空目录)")
	}
	return lipgloss.NewStyle().Width(w).MaxHeight(bodyLines).Render(strings.TrimSuffix(sb.String(), "\n"))
}

func (m *uiModel) renderMessages(w int) []string {
	var lines []string
	for i, msg := range m.msgs {
		switch msg.role {
		case "user":
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Bold(true).Render("┃ 你"))
			for _, l := range wrapText(msg.text, w) {
				lines = append(lines, "  "+l)
			}
		case "assistant":
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950")).Bold(true).Render("◆ licode"))
			text := msg.text
			if m.streaming && i == len(m.msgs)-1 {
				text += lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Render("▌")
			}
			for _, l := range wrapText(text, w) {
				lines = append(lines, "  "+l)
			}
		case "tool":
			lines = append(lines, m.renderToolLine(msg.tool))
			if msg.tool.state != "running" && msg.tool.out != "" {
				for i, l := range wrapText(collapseOutput(msg.tool.out), w) {
					if i >= 6 {
						lines = append(lines, "  …(输出过长，已折叠)")
						break
					}
					lines = append(lines, "  └ "+lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(l))
				}
			}
		case "error":
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Render("✕ "+msg.text))
		}
	}
	if len(lines) == 0 {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render("  欢迎使用 licode —— 终端里的 AI 编程助手。"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render("  输入问题开始对话，或按 Tab 在文件栏中选择要处理的文件。"),
		)
	}
	return lines
}

func (m *uiModel) renderToolLine(t *toolView) string {
	switch t.state {
	case "running":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#d29922")).Render("⚙ " + t.name + "  运行中…")
	case "done":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950")).Render("⚙ " + t.name + "  ✓ 完成")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Render("⚙ " + t.name + "  ✕ 失败")
	}
}

func collapseOutput(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func (m *uiModel) renderInput(w int) string {
	runes := []rune(m.input)
	pos := clamp(m.cursor, 0, len(runes))
	left := string(runes[:pos])
	right := string(runes[pos:])
	over := runewidth.StringWidth(left) + runewidth.StringWidth(right) - (w - 2)
	if over > 0 {
		ls := []rune(left)
		for over > 0 && len(ls) > 0 {
			over -= runewidth.RuneWidth(ls[0])
			ls = ls[1:]
		}
		left = "…" + string(ls)
	}
	cursor := lipgloss.NewStyle().Background(lipgloss.Color("#58a6ff")).Render(" ")
	return left + cursor + right
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

func wrapText(s string, w int) []string {
	if w < 10 {
		w = 10
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		var cur strings.Builder
		curW := 0
		for _, r := range para {
			width := runewidth.RuneWidth(r)
			if curW+width > w {
				out = append(out, cur.String())
				cur.Reset()
				curW = 0
			}
			cur.WriteRune(r)
			curW += width
		}
		out = append(out, cur.String())
	}
	return out
}

func runeLen(s string) int { return len([]rune(s)) }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}