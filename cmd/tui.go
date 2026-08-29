package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	"licode/internal/agent"
	"licode/internal/session"
	"licode/internal/settings"
	wsproto "licode/internal/websocket"
)

// ---------------------------------------------------------------------------
// 配色（opencode 风格：深色 + 橙色强调）
// ---------------------------------------------------------------------------

var (
	colAccent = lipgloss.Color("#e77f24") // opencode 强调橙
	colBg     = lipgloss.Color("#171717")
	colFg     = lipgloss.Color("#e5e5e5")
	colMuted  = lipgloss.Color("#8b8b8b")
	colBorder = lipgloss.Color("#2a2a2a")
	colGreen  = lipgloss.Color("#3fb950")
	colYellow = lipgloss.Color("#d29922")
	colRed    = lipgloss.Color("#f85149")
	colPurple = lipgloss.Color("#bc8cff")
	colBlue   = lipgloss.Color("#58a6ff")
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
	NoSubAgents bool
	Username    string
	Password    string
}

// NewTUICommand 返回 tui 子命令。
func NewTUICommand() *cobra.Command { return newTUICmd() }

func newTUICmd() *cobra.Command {
	opts := &tuiOptions{}
	c := &cobra.Command{
		Use:   "tui",
		Short: "启动终端界面（本地或 --remote 远程瘦客户端）",
		Long: `启动 licode 的终端界面。

默认在本地运行 Agent（需要配置 AI 提供商）。
使用 --remote 连接运行中的 licode serve 服务器，此时本机只负责渲染界面，
所有 AI 推理都在服务器执行，通过 WebSocket 转发流式结果。

快捷键：/ 命令面板 · tab 切换文件树 · ? 帮助 · s 设置 · esc 取消 · enter 发送`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(opts)
		},
	}
	f := c.Flags()
	f.StringVar(&opts.Remote, "remote", "", "远程服务器地址，如 ws://192.168.1.10:8080/ws")
	f.StringVar(&opts.Provider, "provider", "", "AI 提供商: openai | claude | ollama | gemini")
	f.StringVar(&opts.BaseURL, "base-url", "", "提供商 API 地址")
	f.StringVar(&opts.APIKey, "api-key", "", "API 密钥")
	f.StringVar(&opts.Model, "model", "", "模型名")
	f.BoolVar(&opts.NoSubAgents, "no-subagents", false, "禁用子代理编排")
	f.StringVar(&opts.Username, "username", "", "远程访问用户名（默认 licode；环境变量 LICODE_USERNAME）")
	f.StringVar(&opts.Password, "password", "", "远程访问密码（环境变量 LICODE_PASSWORD）")
	return c
}

func runTUI(opts *tuiOptions) error {
	if opts.Remote != "" {
		return runRemoteTUI(opts)
	}
	s := settings.Defaults()
	s.ApplyFlags(opts.Provider, opts.BaseURL, opts.APIKey, opts.Model, opts.NoSubAgents)
	client, err := s.NewClient()
	if err != nil {
		return err
	}
	ag := s.BuildAgent(client)
	m := newUIModel(&uiConfig{mode: "本地", settings: s, agent: ag})
	return m.start()
}

// ---------------------------------------------------------------------------
// 远程瘦客户端
// ---------------------------------------------------------------------------

type thinClient struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (c *thinClient) dial(url, username, password string) error {
	header := http.Header{}
	_, pass, enabled := ResolveAuth(username, password)
	if enabled {
		header.Set("Authorization", basicAuthValue(username, pass))
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
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
	username, _, _ := ResolveAuth(opts.Username, opts.Password)
	m := newUIModel(&uiConfig{
		mode:      "远程",
		provider:  host,
		model:     "服务器",
		remote:    client,
		remoteURL: opts.Remote,
		authUser:  username,
		authPass:  opts.Password,
	})
	return m.start()
}

// ---------------------------------------------------------------------------
// 文件树
// ---------------------------------------------------------------------------

const (
	maxTreeDepth = 4
	maxTreeFiles = 400
)

type treeNode struct {
	name     string
	path     string
	isDir    bool
	depth    int
	open     bool
	loaded   bool
	children []*treeNode
}

func newTree(root string) *treeNode {
	n := &treeNode{name: "./", path: root, isDir: true, depth: 0, open: true}
	n.loadChildren()
	return n
}

func (n *treeNode) loadChildren() {
	if n.loaded || !n.isDir {
		return
	}
	n.loaded = true
	entries, err := os.ReadDir(n.path)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if n.depth >= maxTreeDepth && e.IsDir() {
			continue
		}
		n.children = append(n.children, &treeNode{
			name:  e.Name(),
			path:  filepath.Join(n.path, e.Name()),
			isDir: e.IsDir(),
			depth: n.depth + 1,
		})
		if len(n.children) >= maxTreeFiles {
			break
		}
	}
	sort.Slice(n.children, func(i, j int) bool {
		if n.children[i].isDir != n.children[j].isDir {
			return n.children[i].isDir
		}
		return strings.ToLower(n.children[i].name) < strings.ToLower(n.children[j].name)
	})
}

func (n *treeNode) toggle() {
	if !n.isDir {
		return
	}
	n.open = !n.open
	if n.open {
		n.loadChildren()
	}
}

func (n *treeNode) visible() []*treeNode {
	var out []*treeNode
	var walk func(x *treeNode)
	walk = func(x *treeNode) {
		out = append(out, x)
		if x.isDir && x.open {
			for _, c := range x.children {
				walk(c)
			}
		}
	}
	walk(n)
	return out
}

// ---------------------------------------------------------------------------
// 界面状态
// ---------------------------------------------------------------------------

type uiConfig struct {
	mode      string
	provider  string
	model     string
	settings  settings.Settings
	agent     *agent.Agent
	remote    *thinClient
	remoteURL string
	authUser  string
	authPass  string
}

type toolView struct {
	name  string
	args  string
	out   string
	state string
}

type viewMsg struct {
	role string
	text string
	tool *toolView
}

type askState struct {
	ch     chan bool
	askID  string
	remote bool
}

// command 是 / 命令面板中的命令。
type command struct {
	name string
	desc string
	run  func(m *uiModel)
}

var commandList = []command{
	{"new", "新建对话", func(m *uiModel) { m.newSession() }},
	{"sessions", "会话列表", func(m *uiModel) { m.screen = "sessions"; m.sessionsSel = 0; m.requestSessions() }},
	{"init", "初始化项目 .licode 目录", func(m *uiModel) { m.initProjectDir() }},
	{"export", "一键导出（/export 全部 /export md 对话 /export sessions）", func(m *uiModel) { m.input = "/export "; m.cursor = runeLen(m.input) }},
	{"import", "一键导入（/import <文件路径>）", func(m *uiModel) { m.input = "/import "; m.cursor = runeLen(m.input) }},
	{"clear", "清空会话", func(m *uiModel) { m.clearChat() }},
	{"settings", "打开设置", func(m *uiModel) { m.openSettings() }},
	{"help", "快捷键帮助", func(m *uiModel) { m.screen = "help" }},
	{"files", "切换到文件树", func(m *uiModel) { m.focus = "files" }},
	{"exit", "退出", func(m *uiModel) { m.quitting = true }},
}

type uiModel struct {
	width, height         int
	mode, provider, model string

	settings settings.Settings
	agent    *agent.Agent
	remote   *thinClient
	wsURL    string
	authUser string
	authPass string

	screen string // chat | help
	focus  string // chat | files

	tree    *treeNode
	treeSel int

	msgs      []viewMsg
	input     string
	cursor    int
	streaming bool
	status    string
	offset    int

	cmdSel   int
	fileCmds []command // 从 .licode/commands 加载的自定义命令

	settingsSel  int
	settingsEdit bool
	settingsBuf  string

	// 多会话
	sessions      *session.Manager
	sessionsList  []session.Info
	remoteSession string
	sessionsSel   int

	asking *askState
	// 远程缓存的设置
	remoteSettings *settings.Settings

	cancel   context.CancelFunc
	events   chan agent.Event
	quit     chan struct{}
	quitting bool
}

func newUIModel(cfg *uiConfig) *uiModel {
	m := &uiModel{
		mode:     cfg.mode,
		provider: cfg.provider,
		model:    cfg.model,
		settings: cfg.settings,
		agent:    cfg.agent,
		remote:   cfg.remote,
		wsURL:    cfg.remoteURL,
		authUser: cfg.authUser,
		authPass: cfg.authPass,
		screen:   "chat",
		focus:    "chat",
		events:   make(chan agent.Event, 512),
		quit:     make(chan struct{}),
	}
	if cfg.agent != nil {
		m.provider = cfg.settings.Provider
		m.model = cfg.settings.Model
		m.tree = newTree(".")
		m.sessions = session.NewManager(settings.SessionsDir(), true)
		m.agent.Session = m.sessions.Current()
	}
	return m
}

func (m *uiModel) start() error {
	if m.agent != nil {
		// 首次使用自动生成 ~/.licode 数据目录，并启用日志文件
		if err := settings.EnsureDirs(); err == nil {
			if lf, err := settings.LogFile(); err == nil {
				defer lf.Close()
			}
		}
	}
	if m.remote != nil {
		go m.remoteLoop()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if m.sessions != nil {
		_ = m.sessions.SaveAll()
	}
	return err
}

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
		if err := m.remote.dial(m.wsURL, m.authUser, m.authPass); err != nil {
			sendStatus("连接失败（请检查用户名密码），2 秒后重试…")
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
	case wsproto.EvtSettings:
		return agent.Event{Type: agent.EventSettings, Settings: evt.Settings}
	case wsproto.EvtSessions:
		return agent.Event{Type: agent.EventSessions, Settings: evt.Sessions, SessionID: evt.SessionID}
	case wsproto.EvtAsk:
		return agent.Event{Type: agent.EventAsk, ToolName: evt.ToolName, ToolArgs: evt.ToolArgs, AskID: evt.AskID}
	default:
		return agent.Event{Type: agent.EventStatus, Content: evt.Content}
	}
}

// ---------------------------------------------------------------------------
// bubbletea 生命周期
// ---------------------------------------------------------------------------

func (m *uiModel) Init() tea.Cmd {
	if m.agent != nil {
		m.reloadMessages()
	}
	// 加载 .licode/commands 自定义命令
	for _, cf := range agent.LoadCommandFiles(agent.CommandDirs()...) {
		prompt := cf.Prompt
		m.fileCmds = append(m.fileCmds, command{
			name: cf.Name,
			desc: cf.Description,
			run:  func(m *uiModel) { m.submitCommand(prompt) },
		})
	}
	return nil
}

// initProjectDir 生成项目 .licode 目录（模仿 opencode 的 .opencode）。
func (m *uiModel) initProjectDir() {
	if _, err := os.Stat(".licode"); err == nil {
		m.status = ".licode 已存在"
		return
	}
	for _, d := range []string{".licode/agents", ".licode/commands", ".licode/skills", ".licode/modes", ".licode/tools"} {
		_ = os.MkdirAll(d, 0o755)
	}
	readme := `# .licode（项目级配置目录）

licode 会自动读取这里的配置，用法与 opencode 一致：

- agents/    自定义子代理定义（markdown + frontmatter: name / description / tools）
- commands/  自定义命令（markdown + frontmatter: name / description，正文为提示词模板）
- skills/    技能（markdown + frontmatter: name / description，正文为执行步骤）
- modes/     （预留）
- tools/     （预留）

用户级数据目录在 ~/.licode/（配置、MCP、对话记录、日志、缓存）。
`
	_ = os.WriteFile(".licode/README.md", []byte(readme), 0o644)
	m.status = "已生成项目 .licode 目录"
}

// submitCommand 直接发送一条命令/模板消息。
func (m *uiModel) submitCommand(text string) {
	if m.streaming {
		return
	}
	m.input = text
	m.submit()
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
		m.msgs = append(m.msgs, viewMsg{role: "tool", tool: &toolView{name: e.ToolName, args: e.ToolArgs, state: "running"}})
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
		if m.sessions != nil && m.settings.TitleGen {
			s := m.sessions.Current()
			if s.Title() == "新对话" && len(m.msgs) > 0 {
				s.SetTitle(autoTitle(firstUserText(m.msgs)))
			}
		}
	case agent.EventError:
		m.streaming = false
		m.status = ""
		m.msgs = append(m.msgs, viewMsg{role: "error", text: "错误: " + e.Error})
	case agent.EventStatus:
		m.status = e.Content
	case agent.EventSettings:
		var s settings.Settings
		if b, err := json.Marshal(e.Settings); err == nil && json.Unmarshal(b, &s) == nil {
			m.remoteSettings = &s
			m.settings = s
			if m.mode == "远程" {
				m.status = "已同步服务器设置"
			}
		}
	case agent.EventSessions:
		var list []session.Info
		if b, err := json.Marshal(e.Settings); err == nil && json.Unmarshal(b, &list) == nil {
			m.sessionsList = list
		}
		if e.SessionID != "" {
			m.remoteSession = e.SessionID
		}
	case agent.EventAsk:
		m.streaming = true
		if m.asking == nil {
			m.asking = &askState{askID: e.AskID, remote: m.remote != nil}
		}
		m.status = "是否允许执行工具 " + e.ToolName + "？ y=允许  n=拒绝"
	}
}

// ---------------------------------------------------------------------------
// 动作
// ---------------------------------------------------------------------------

func (m *uiModel) submit() {
	text := strings.TrimSpace(m.input)
	if text == "" || m.streaming || m.asking != nil || m.screen == "help" {
		return
	}
	m.input = ""
	m.cursor = 0
	// 导入 / 导出
	if strings.HasPrefix(text, "/export") || strings.HasPrefix(text, "/import") {
		m.doTransfer(text)
		return
	}
	m.offset = 0
	m.msgs = append(m.msgs, viewMsg{role: "user", text: text})
	m.streaming = true

	if m.remote != nil {
		go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeMessage, Content: text})
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
		if m.sessions != nil {
			_ = m.sessions.SaveAll()
		}
		if err != nil && ctx.Err() == nil {
			select {
			case m.events <- agent.Event{Type: agent.EventError, Error: err.Error()}:
			default:
			}
		}
	}()
}

func (m *uiModel) clearChat() {
	m.msgs = nil
	m.input = ""
	m.cursor = 0
	m.offset = 0
	if m.agent != nil {
		m.agent.Session.Clear()
	}
	m.status = "会话已清空"
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

func (m *uiModel) applyLocalSettings() {
	client, err := m.settings.NewClient()
	if err != nil {
		m.status = "设置无效: " + err.Error()
		return
	}
	oldSession := m.agent.Session
	ag := m.settings.BuildAgent(client)
	ag.Session = oldSession
	ag.Ask = m.askFunc()
	m.agent = ag
	m.provider = m.settings.Provider
	m.model = m.settings.Model
	if err := m.settings.Save(""); err != nil {
		m.status = fmt.Sprintf("设置已应用: %s/%s（配置文件保存失败: %v）", m.settings.Provider, m.settings.Model, err)
	} else {
		m.status = fmt.Sprintf("设置已应用并保存到 %s: %s/%s", settings.SavePath(), m.settings.Provider, m.settings.Model)
	}
}

// newSession 新建一个对话（本地直接切换，远程发协议）。
func (m *uiModel) newSession() {
	if m.streaming {
		return
	}
	if m.remote != nil {
		go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSessionNew})
		return
	}
	m.sessions.New()
	m.agent.Session = m.sessions.Current()
	m.reloadMessages()
	_ = m.sessions.SaveAll()
	m.status = "已新建对话"
}

// requestSessions 刷新会话列表（远程模式下向服务器请求）。
func (m *uiModel) requestSessions() {
	if m.remote != nil {
		go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSessionsGet})
	}
}

// sessionList 返回会话列表：本地直接用 manager，远程用缓存。
func (m *uiModel) sessionList() []session.Info {
	if m.remote != nil {
		return m.sessionsList
	}
	if m.sessions != nil {
		return m.sessions.List()
	}
	return nil
}

func (m *uiModel) handleSessionsKey(msg tea.KeyMsg) tea.Cmd {
	list := m.sessionList()
	switch msg.String() {
	case "up":
		if m.sessionsSel > 0 {
			m.sessionsSel--
		}
	case "down":
		if m.sessionsSel < len(list)-1 {
			m.sessionsSel++
		}
	case "enter":
		if m.sessionsSel >= 0 && m.sessionsSel < len(list) {
			id := list[m.sessionsSel].ID
			if m.remote != nil {
				go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSessionSwitch, SessionID: id})
			} else if m.sessions.SetCurrent(id) {
				if s, ok := m.sessions.Get(id); ok {
					m.agent.Session = s
				}
				m.reloadMessages()
				_ = m.sessions.SaveAll()
			}
		}
		m.screen = "chat"
	case "n":
		m.newSession()
	case "d":
		if m.sessionsSel >= 0 && m.sessionsSel < len(list) {
			id := list[m.sessionsSel].ID
			if m.remote != nil {
				go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSessionDelete, SessionID: id})
			} else if id != m.sessions.CurrentID() {
				m.sessions.Delete(id)
				_ = m.sessions.SaveAll()
			} else {
				m.status = "不能删除当前对话"
			}
		}
	case "esc", "tab", "q", "ctrl+c":
		m.screen = "chat"
	}
	return nil
}

// reloadMessages 把当前会话的历史消息重建为显示列表。
func (m *uiModel) reloadMessages() {
	if m.agent == nil {
		return
	}
	msgs := m.agent.Session.Messages()
	out := make([]viewMsg, 0, len(msgs))
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			out = append(out, viewMsg{role: "user", text: msg.Content})
		case "assistant":
			out = append(out, viewMsg{role: "assistant", text: msg.Content})
		case "tool":
			out = append(out, viewMsg{role: "tool", tool: &toolView{name: msg.ToolName, out: msg.Content, state: "done"}})
		}
	}
	m.msgs = out
}

func firstUserText(msgs []viewMsg) string {
	for _, msg := range msgs {
		if msg.role == "user" {
			return msg.text
		}
	}
	return "新对话"
}

func (m *uiModel) askFunc() func(ctx context.Context, tool, args string) (bool, error) {
	return func(ctx context.Context, tool, args string) (bool, error) {
		ch := make(chan bool, 1)
		m.asking = &askState{ch: ch, remote: false}
		select {
		case m.events <- agent.Event{Type: agent.EventAsk, ToolName: tool, ToolArgs: args}:
		case <-ctx.Done():
			m.asking = nil
			return false, ctx.Err()
		}
		select {
		case ok := <-ch:
			m.asking = nil
			return ok, nil
		case <-ctx.Done():
			m.asking = nil
			return false, ctx.Err()
		}
	}
}

// ---------------------------------------------------------------------------
// 按键
// ---------------------------------------------------------------------------

func (m *uiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// 工具确认
	if m.asking != nil {
		switch msg.String() {
		case "y", "Y":
			if m.asking.remote {
				go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeAskReply, AskID: m.asking.askID, AskApprove: true})
			} else if m.asking.ch != nil {
				m.asking.ch <- true
			}
			m.asking = nil
			m.status = ""
		case "n", "N":
			if m.asking.remote {
				go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeAskReply, AskID: m.asking.askID, AskApprove: false})
			} else if m.asking.ch != nil {
				m.asking.ch <- false
			}
			m.asking = nil
			m.status = ""
		}
		return nil
	}

	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		if m.streaming {
			m.cancel()
			m.streaming = false
			m.status = "已取消"
			return nil
		}
		return m.quitApp()
	case "?":
		if !m.streaming {
			if m.screen == "help" {
				m.screen = "chat"
			} else {
				m.screen = "help"
			}
		}
		return nil
	case "esc":
		if m.screen == "help" {
			m.screen = "chat"
			return nil
		}
		if m.focus == "files" {
			m.focus = "chat"
			return nil
		}
		if m.streaming {
			m.cancel()
			m.streaming = false
			m.status = "已取消"
		}
		return nil
	}

	// 命令面板 / 设置编辑 / 会话列表 / 文件树 各自处理
	if m.screen == "help" {
		return nil
	}
	if m.screen == "sessions" {
		return m.handleSessionsKey(msg)
	}
	if m.settingsEdit {
		return m.handleSettingsEdit(msg)
	}
	if m.showCommands() {
		return m.handleCommandKeys(msg)
	}
	if m.focus == "files" {
		return m.handleFilesKey(msg)
	}

	// 普通输入
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
	case tea.KeyTab:
		if m.width >= 70 {
			if m.focus == "chat" {
				m.focus = "files"
			} else {
				m.focus = "chat"
			}
		}
	}
	return nil
}

func (m *uiModel) showCommands() bool {
	return !m.streaming && strings.HasPrefix(m.input, "/") && m.screen == "chat" && m.focus == "chat"
}

func (m *uiModel) commandMatches() []command {
	query := strings.ToLower(strings.TrimPrefix(m.input, "/"))
	var out []command
	for _, c := range commandList {
		if query == "" || strings.Contains(c.name, query) {
			out = append(out, c)
		}
	}
	for _, c := range m.fileCmds {
		if query == "" || strings.Contains(c.name, query) {
			out = append(out, c)
		}
	}
	return out
}

func (m *uiModel) handleCommandKeys(msg tea.KeyMsg) tea.Cmd {
	cmds := m.commandMatches()
	switch msg.String() {
	case "up":
		if m.cmdSel > 0 {
			m.cmdSel--
		}
	case "down":
		if m.cmdSel < len(cmds)-1 {
			m.cmdSel++
		}
	case "enter":
		if len(cmds) > 0 {
			idx := clamp(m.cmdSel, 0, len(cmds)-1)
			cmds[idx].run(m)
			m.input = ""
			m.cursor = 0
			m.cmdSel = 0
		}
	case "tab", "esc":
		m.input = ""
		m.cursor = 0
		m.cmdSel = 0
		m.focus = "chat"
	}
	return nil
}

func (m *uiModel) handleFilesKey(msg tea.KeyMsg) tea.Cmd {
	if m.tree == nil {
		return nil
	}
	vis := m.tree.visible()
	switch msg.String() {
	case "up", "shift+tab":
		if m.treeSel > 0 {
			m.treeSel--
		}
	case "down":
		if m.treeSel < len(vis)-1 {
			m.treeSel++
		}
	case "left":
		if n := vis[clamp(m.treeSel, 0, len(vis)-1)]; n.isDir && n.open {
			n.open = false
		}
	case "right", "enter":
		n := vis[clamp(m.treeSel, 0, len(vis)-1)]
		if n.isDir {
			n.toggle()
		} else {
			if m.input == "" {
				m.input = n.path
			} else {
				m.input = m.input + " " + n.path
			}
			m.cursor = runeLen(m.input)
		}
	case "tab", "esc", "q":
		m.focus = "chat"
	}
	return nil
}

// ---------------------------------------------------------------------------
// 设置界面
// ---------------------------------------------------------------------------

type settingsField struct {
	label string
	kind  string
}

var settingsFields = []settingsField{
	{"提供商", "select"},
	{"模型", "text"},
	{"API 地址", "text"},
	{"API 密钥", "text"},
	{"温度", "text"},
	{"最大输出 tokens", "text"},
	{"最大迭代次数", "text"},
	{"子代理", "bool"},
	{"需确认的工具", "text"},
	{"禁用的工具", "text"},
}

func (m *uiModel) openSettings() {
	m.screen = "chat"
	m.settingsSel = 0
	if m.remote != nil {
		go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSettingsGet})
		m.status = "正在获取服务器设置…"
	} else {
		m.status = "设置界面：Enter 编辑 · ↑↓ 选择 · Esc 返回"
	}
}

func (m *uiModel) fieldValue(idx int) string {
	s := &m.settings
	f := settingsFields[idx]
	switch f.kind {
	case "select":
		return s.Provider
	case "bool":
		if s.SubAgents {
			return "开启"
		}
		return "关闭"
	case "text":
		switch f.label {
		case "模型":
			return s.Model
		case "API 地址":
			return s.BaseURL
		case "API 密钥":
			if s.APIKey == "" {
				return "(未设置)"
			}
			if len(s.APIKey) > 8 {
				return s.APIKey[:4] + "****" + s.APIKey[len(s.APIKey)-4:]
			}
			return "****"
		case "温度":
			return strconv.FormatFloat(s.Temperature, 'g', -1, 64)
		case "最大输出 tokens":
			return strconv.Itoa(s.MaxTokens)
		case "最大迭代次数":
			return strconv.Itoa(s.MaxIterations)
		case "需确认的工具":
			return strings.Join(s.AskTools, ", ")
		case "禁用的工具":
			return strings.Join(s.DenyTools, ", ")
		}
	}
	return ""
}

func (m *uiModel) handleSettingsEdit(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyRunes:
		m.settingsBuf += string(msg.Runes)
	case tea.KeyBackspace:
		if len(m.settingsBuf) > 0 {
			m.settingsBuf = m.settingsBuf[:len(m.settingsBuf)-1]
		}
	case tea.KeyEnter:
		m.commitSettingsEdit()
	case tea.KeyEsc, tea.KeyCtrlC:
		m.settingsEdit = false
	}
	return nil
}

func (m *uiModel) commitSettingsEdit() {
	idx := m.settingsSel
	f := settingsFields[idx]
	s := &m.settings
	val := strings.TrimSpace(m.settingsBuf)
	var err error
	switch f.label {
	case "模型":
		s.Model = val
	case "API 地址":
		s.BaseURL = strings.TrimRight(val, "/")
	case "API 密钥":
		s.APIKey = val
	case "温度":
		var fv float64
		if fv, err = strconv.ParseFloat(val, 64); err == nil {
			s.Temperature = fv
		}
	case "最大输出 tokens":
		var n int
		if n, err = strconv.Atoi(val); err == nil && n > 0 {
			s.MaxTokens = n
		}
	case "最大迭代次数":
		var n int
		if n, err = strconv.Atoi(val); err == nil && n > 0 {
			s.MaxIterations = n
		}
	case "需确认的工具":
		s.AskTools = settings.ParseToolList(val)
	case "禁用的工具":
		s.DenyTools = settings.ParseToolList(val)
	}
	m.settingsEdit = false
	if err != nil {
		m.status = "输入无效: " + err.Error()
		return
	}
	m.afterSettingsChange()
}

func (m *uiModel) afterSettingsChange() {
	if m.remote != nil {
		snap := m.settings.Snapshot()
		go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSettingsSet, Settings: &snap})
		m.status = "设置已发送到服务器"
		return
	}
	m.applyLocalSettings()
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

func (m *uiModel) View() string {
	w := m.width
	h := m.height
	if w == 0 {
		w, h = 100, 30
	}
	if m.screen == "help" {
		return m.renderHelp(w, h)
	}
	if m.screen == "sessions" {
		return m.renderSessions(w, h)
	}

	var sb strings.Builder
	sb.WriteString(m.renderHeader(w))
	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(colBorder).Render(strings.Repeat("─", w)) + "\n")

	// 主体：左侧文件树 + 中间消息区
	sideW := 0
	showTree := m.focus == "files" || (m.tree != nil && w >= 88)
	if showTree && m.tree != nil {
		sideW = 30
	}
	mainW := w - sideW - 1
	if mainW < 40 {
		mainW = 40
	}

	bodyLines := h - 4
	if bodyLines < 4 {
		bodyLines = 4
	}

	var body string
	if m.showCommands() {
		body = m.renderCommandPalette(mainW, bodyLines)
	} else {
		body = m.renderMessages(mainW, bodyLines)
	}
	mainPane := lipgloss.NewStyle().Width(mainW).Render(body)

	if sideW > 0 {
		side := m.renderTree(sideW, bodyLines)
		divider := lipgloss.NewStyle().Foreground(colBorder).Render("│")
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, mainPane, divider, side))
	} else {
		sb.WriteString(mainPane)
	}
	sb.WriteString("\n")

	sb.WriteString(m.renderInput(w) + "\n")
	sb.WriteString(m.renderStatusBar(w))
	return sb.String()
}

func (m *uiModel) renderHeader(w int) string {
	title := lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("licode")
	badge := lipgloss.NewStyle().Foreground(colMuted).Render(m.provider + "/" + m.model)
	mode := lipgloss.NewStyle().Foreground(colBlue).Render(m.mode)
	sessTitle := ""
	if m.agent != nil && m.agent.Session != nil {
		sessTitle = m.agent.Session.Title()
	}
	if m.remote != nil && m.remoteSession != "" {
		for _, s := range m.sessionsList {
			if s.ID == m.remoteSession {
				sessTitle = s.Title
				break
			}
		}
	}
	left := lipgloss.NewStyle().Render(title + "  " + badge + "  " + mode + "  " + lipgloss.NewStyle().Foreground(colFg).Render(sessTitle))
	right := lipgloss.NewStyle().Foreground(colMuted).Render("tab:文件  ?:帮助  /:命令")
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(lipgloss.JoinHorizontal(lipgloss.Center, left, lipgloss.NewStyle().Width(w-45).Align(lipgloss.Right).Render(right)))
}

func (m *uiModel) renderSessions(w, h int) string {
	list := m.sessionList()
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render(" 会话列表"))
	sb.WriteString("  " + lipgloss.NewStyle().Foreground(colMuted).Render("↑↓ 选择 · enter 切换 · n 新建 · d 删除 · esc 返回"))
	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(colBorder).Render(strings.Repeat("─", w)) + "\n\n")

	current := ""
	if m.remote != nil {
		current = m.remoteSession
	} else if m.sessions != nil {
		current = m.sessions.CurrentID()
	}
	for i, s := range list {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(colFg)
		if s.ID == current {
			marker = "▸ "
			style = style.Foreground(colAccent).Bold(true)
		}
		sb.WriteString("  " + marker + style.Render(s.Title) + lipgloss.NewStyle().Foreground(colMuted).Render(fmt.Sprintf("  (%d 条)", s.Count)) + "\n")
		if i == m.sessionsSel {
			_ = i
		}
	}
	if len(list) == 0 {
		sb.WriteString("  " + lipgloss.NewStyle().Foreground(colMuted).Render("（暂无会话，按 n 新建）"))
	}
	return lipgloss.NewStyle().Width(w).Height(h).Render(sb.String())
}

func (m *uiModel) renderTree(w, bodyLines int) string {
	if m.tree == nil {
		return ""
	}
	vis := m.tree.visible()
	start := 0
	if m.treeSel >= bodyLines-1 {
		start = m.treeSel - bodyLines + 2
	}
	end := minInt(len(vis), start+bodyLines)
	if start < 0 {
		start = 0
	}
	var sb strings.Builder
	for i := start; i < end; i++ {
		n := vis[i]
		indent := strings.Repeat("  ", n.depth)
		prefix := " "
		if n.isDir {
			if n.open {
				prefix = "▾"
			} else {
				prefix = "▸"
			}
		}
		style := lipgloss.NewStyle().Foreground(colFg)
		if n.isDir {
			style = style.Foreground(colAccent)
		}
		if m.focus == "files" && i == m.treeSel {
			style = style.Background(lipgloss.Color("#2a2a2a")).Bold(true).Foreground(colAccent)
			sb.WriteString("▸" + prefix + " " + style.Render(indent+n.name))
		} else {
			sb.WriteString(" " + prefix + " " + style.Render(indent+n.name))
		}
		sb.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(w).MaxHeight(bodyLines).Render(strings.TrimSuffix(sb.String(), "\n"))
}

func (m *uiModel) renderMessages(w, bodyLines int) string {
	var lines []string
	lines = append(lines, m.renderMsgLine("◆ licode", colGreen))
	if len(m.msgs) == 0 {
		lines = append(lines,
			"  "+lipgloss.NewStyle().Foreground(colMuted).Render("欢迎使用 licode —— 终端里的 AI 编程助手。"),
			"  "+lipgloss.NewStyle().Foreground(colMuted).Render("输入问题开始对话，或输入 / 查看命令。"),
		)
	}
	for i, msg := range m.msgs {
		switch msg.role {
		case "user":
			lines = append(lines, m.renderMsgLine("┃ 你", colBlue))
			for _, l := range wrapText(msg.text, w-2) {
				lines = append(lines, "  "+l)
			}
			if i == len(m.msgs)-1 && m.streaming {
				lines = append(lines, "  "+lipgloss.NewStyle().Foreground(colAccent).Render("▌"))
			}
		case "assistant":
			lines = append(lines, m.renderMsgLine("◆ licode", colGreen))
			text := msg.text
			if m.streaming && i == len(m.msgs)-1 {
				text += lipgloss.NewStyle().Foreground(colAccent).Render("▌")
			}
			for _, l := range wrapText(text, w-2) {
				lines = append(lines, "  "+l)
			}
		case "tool":
			lines = append(lines, m.renderToolChip(msg.tool, w))
			if msg.tool.state != "running" && msg.tool.out != "" {
				for i, l := range wrapText(collapseOutput(msg.tool.out), w-4) {
					if i >= 4 {
						lines = append(lines, "    …(输出已折叠)")
						break
					}
					lines = append(lines, "    "+lipgloss.NewStyle().Foreground(colMuted).Render(l))
				}
			}
		case "error":
			lines = append(lines, lipgloss.NewStyle().Foreground(colRed).Render("✕ "+msg.text))
		}
	}
	// 底部留一行空行分隔
	lines = append(lines, "")

	start := len(lines) - bodyLines - m.offset
	if start < 0 {
		start = 0
	}
	if start > 0 {
		lines = append([]string{"↑ 更多历史 ↑"}, lines[start:]...)
	}
	out := strings.Join(lines, "\n")
	return out
}

func (m *uiModel) renderMsgLine(text string, color lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(color).Bold(true).Render(text)
}

func (m *uiModel) renderToolChip(t *toolView, w int) string {
	var s string
	switch t.state {
	case "running":
		s = "⚙ " + t.name + "  运行中…"
		return lipgloss.NewStyle().Foreground(colYellow).Render(s)
	case "done":
		s = "⚙ " + t.name
		// 从工具输出里取第一行作为摘要
		first := firstLine(t.out)
		if first != "" {
			s += "  " + truncateWidth(first, w-20)
		}
		s += "  ✓"
		return lipgloss.NewStyle().Foreground(colGreen).Render(s)
	default:
		return lipgloss.NewStyle().Foreground(colRed).Render("⚙ " + t.name + "  ✕")
	}
}

func (m *uiModel) renderCommandPalette(w, bodyLines int) string {
	cmds := m.commandMatches()
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colMuted).Render("命令 (" + m.input + ")"))
	sb.WriteString("\n")
	for i, c := range cmds {
		line := "  " + c.name + "  " + lipgloss.NewStyle().Foreground(colMuted).Render(c.desc)
		if i == m.cmdSel {
			line = lipgloss.NewStyle().Background(lipgloss.Color("#2a2a2a")).Foreground(colAccent).Bold(true).Render("▸ "+c.name) + "  " + lipgloss.NewStyle().Foreground(colMuted).Render(c.desc)
		}
		sb.WriteString(line + "\n")
		if i >= bodyLines-3 {
			break
		}
	}
	return sb.String()
}

func (m *uiModel) renderInput(w int) string {
	placeholder := "提问…"
	inputStr := m.input
	cursor := lipgloss.NewStyle().Background(colAccent).Render(" ")
	var line string
	if inputStr == "" {
		line = lipgloss.NewStyle().Foreground(colMuted).Render(placeholder) + cursor
	} else {
		runes := []rune(inputStr)
		pos := clamp(m.cursor, 0, len(runes))
		left := string(runes[:pos])
		right := string(runes[pos:])
		line = left + cursor + right
	}
	prompt := lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("> ")
	modelBadge := lipgloss.NewStyle().Foreground(colMuted).Render(m.provider + "/" + m.model)
	return lipgloss.NewStyle().Width(w).Render(lipgloss.JoinHorizontal(lipgloss.Center, prompt, line, lipgloss.NewStyle().Width(w-30).Align(lipgloss.Right).Render(modelBadge)))
}

func (m *uiModel) renderStatusBar(w int) string {
	left := "esc: 取消  /: 命令  enter: 发送  tab: 文件  s: 设置  ?: 帮助"
	right := m.status
	if m.streaming && right == "" {
		right = "思考中…"
	}
	ls := lipgloss.NewStyle().Foreground(colMuted).Render(left)
	rs := lipgloss.NewStyle().Foreground(colMuted).Render(right)
	return lipgloss.NewStyle().Width(w).Render(lipgloss.JoinHorizontal(lipgloss.Center, ls, lipgloss.NewStyle().Width(w-40).Align(lipgloss.Right).Render(rs)))
}

func (m *uiModel) renderHelp(w, h int) string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render(" 快捷键"))
	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(colBorder).Render(strings.Repeat("─", w)) + "\n\n")
	rows := [][2]string{
		{"enter", "发送消息"},
		{"/", "打开命令面板"},
		{"tab", "切换 消息/文件树"},
		{"↑↓ →←", "文件树中移动 / 展开折叠目录"},
		{"enter(文件)", "把所选文件路径放入输入框"},
		{"s", "打开设置（提供商/模型/密钥等）"},
		{"esc", "取消当前输出 / 返回"},
		{"ctrl+c", "取消输出；再次按下退出"},
		{"/clear", "清空会话"},
		{"?", "本帮助页"},
	}
	for _, r := range rows {
		sb.WriteString("  " + lipgloss.NewStyle().Foreground(colAccent).Render(padRight(r[0], 16)) + lipgloss.NewStyle().Foreground(colFg).Render(r[1]) + "\n")
	}
	sb.WriteString("\n  " + lipgloss.NewStyle().Foreground(colMuted).Render("按 esc 或 ? 返回"))
	return lipgloss.NewStyle().Width(w).Height(h).Render(sb.String())
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

func padRight(s string, n int) string {
	width := runewidth.StringWidth(s)
	if width >= n {
		return s
	}
	return s + strings.Repeat(" ", n-width)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncateWidth(s string, n int) string {
	if runewidth.StringWidth(s) <= n {
		return s
	}
	var sb strings.Builder
	sw := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if sw+rw > n-1 {
			break
		}
		sb.WriteRune(r)
		sw += rw
	}
	return sb.String() + "…"
}

func collapseOutput(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

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
