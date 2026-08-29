package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	"net/http"

	"licode/internal/agent"
	"licode/internal/settings"
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

设置（提供商/模型/密钥等）可在 TUI 内按 s 键打开设置界面修改，
无需配置文件。`,
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

	m := newUIModel(&uiConfig{
		mode:     "本地",
		settings: s,
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
	state string // running | done | error
}

type viewMsg struct {
	role string // user | assistant | tool | error
	text string
	tool *toolView
}

type askState struct {
	ch     chan bool
	askID  string
	tool   string
	remote bool
}

type uiModel struct {
	width, height int

	mode     string
	provider string
	model    string

	settings settings.Settings // 本地模式的设置
	agent    *agent.Agent      // 本地模式的 Agent
	remote   *thinClient
	wsURL    string
	authUser string
	authPass string

	// 远程缓存
	remoteSettings *settings.Settings

	msgs      []viewMsg
	input     string
	cursor    int
	streaming bool
	status    string

	focus  string // chat | files
	files  []string
	sel    int
	offset int // 从底部回滚的行数

	// 设置界面
	settingsScreen bool
	settingsSel    int
	settingsEdit   bool
	settingsBuf    string

	// 工具确认
	asking *askState

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
		focus:    "chat",
		events:   make(chan agent.Event, 512),
		quit:     make(chan struct{}),
	}
	if cfg.agent != nil {
		m.provider = cfg.settings.Provider
		m.model = cfg.settings.Model
	}
	return m
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
	case wsproto.EvtSettings:
		return agent.Event{Type: agent.EventSettings, Settings: evt.Settings}
	case wsproto.EvtAsk:
		return agent.Event{Type: agent.EventAsk, ToolName: evt.ToolName, ToolArgs: evt.ToolArgs, AskID: evt.AskID}
	default:
		return agent.Event{Type: agent.EventStatus, Content: evt.Content}
	}
}

// ---------------------------------------------------------------------------
// bubbletea 实现
// ---------------------------------------------------------------------------

func (m *uiModel) Init() tea.Cmd {
	if m.agent != nil {
		m.loadFiles()
	}
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
	case agent.EventSettings:
		var s settings.Settings
		if b, err := json.Marshal(e.Settings); err == nil {
			if json.Unmarshal(b, &s) == nil {
				m.remoteSettings = &s
				m.settings = s
				if m.mode == "远程" {
					m.status = "已同步服务器设置"
				}
			}
		}
	case agent.EventAsk:
		m.streaming = true
		if m.asking == nil {
			m.asking = &askState{
				askID:  e.AskID,
				tool:   e.ToolName,
				remote: m.remote != nil,
			}
		}
		m.status = "是否允许执行工具 " + e.ToolName + "？ y=允许  n=拒绝"
	}
}

func (m *uiModel) submit() {
	text := strings.TrimSpace(m.input)
	if text == "" || m.streaming || m.settingsScreen || m.asking != nil {
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

// applyLocalSettings 本地模式：把当前设置应用到客户端与 Agent。
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
	m.status = fmt.Sprintf("设置已应用: %s/%s", m.settings.Provider, m.settings.Model)
}

// askFunc 构造本地模式的工具确认回调。
func (m *uiModel) askFunc() func(ctx context.Context, tool, args string) (bool, error) {
	return func(ctx context.Context, tool, args string) (bool, error) {
		ch := make(chan bool, 1)
		m.asking = &askState{ch: ch, tool: tool}
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

func (m *uiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// 工具确认：y / n
	if m.asking != nil {
		switch msg.String() {
		case "y", "Y":
			if m.asking.remote {
				go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeAskReply, AskID: m.asking.askID, AskApprove: true})
			} else if m.asking.ch != nil {
				m.asking.ch <- true
			}
			m.clearAsk()
		case "n", "N":
			if m.asking.remote {
				go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeAskReply, AskID: m.asking.askID, AskApprove: false})
			} else if m.asking.ch != nil {
				m.asking.ch <- false
			}
			m.clearAsk()
		}
		return nil
	}

	// 设置编辑状态
	if m.settingsScreen && m.settingsEdit {
		return m.handleSettingsEdit(msg)
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
	case "s":
		if !m.streaming && !m.settingsScreen && m.asking == nil {
			m.openSettings()
		} else if m.settingsScreen {
			m.settingsScreen = false
			m.settingsEdit = false
			m.status = ""
		}
		return nil
	case "esc":
		if m.settingsScreen {
			m.settingsScreen = false
			m.settingsEdit = false
			m.status = ""
			return nil
		}
		if m.streaming {
			m.cancel()
			m.streaming = false
			m.status = "已取消"
		}
		return nil
	case "tab":
		if m.width >= 70 && !m.settingsScreen {
			if m.focus == "chat" {
				m.focus = "files"
			} else {
				m.focus = "chat"
			}
		}
		return nil
	}

	if m.settingsScreen {
		return m.handleSettingsKey(msg)
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
	}
	return nil
}

func (m *uiModel) clearAsk() {
	m.asking = nil
	m.status = ""
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
// 设置界面
// ---------------------------------------------------------------------------

// settingsFields 描述设置界面的字段顺序与类型。
type settingsField struct {
	label string
	kind  string // select | bool | text
}

var settingsFields = []settingsField{
	{"提供商 provider", "select"},
	{"模型 model", "text"},
	{"API 地址 base_url", "text"},
	{"API 密钥 api_key", "text"},
	{"温度 temperature", "text"},
	{"最大输出 tokens", "text"},
	{"最大迭代次数", "text"},
	{"子代理", "bool"},
	{"需确认的工具 ask_tools", "text"},
	{"禁用的工具 deny_tools", "text"},
}

func (m *uiModel) openSettings() {
	m.settingsScreen = true
	m.settingsSel = 0
	if m.remote != nil {
		go m.remote.send(wsproto.ClientMessage{Type: wsproto.TypeSettingsGet})
		m.status = "正在获取服务器设置…"
	} else {
		m.status = "设置界面（↑↓ 选择 · Enter 编辑/切换 · Esc 退出）"
	}
}

func (m *uiModel) curSettings() *settings.Settings {
	return &m.settings
}

func (m *uiModel) fieldValue(idx int) string {
	f := settingsFields[idx]
	s := m.curSettings()
	switch f.label {
	case "提供商 provider":
		return s.Provider
	case "模型 model":
		return s.Model
	case "API 地址 base_url":
		return s.BaseURL
	case "API 密钥 api_key":
		if m.settingsEdit && idx == m.settingsSel {
			return s.APIKey
		}
		if s.APIKey == "" {
			return "(未设置)"
		}
		if len(s.APIKey) > 8 {
			return s.APIKey[:4] + "****" + s.APIKey[len(s.APIKey)-4:]
		}
		return "****"
	case "温度 temperature":
		return strconv.FormatFloat(s.Temperature, 'g', -1, 64)
	case "最大输出 tokens":
		return strconv.Itoa(s.MaxTokens)
	case "最大迭代次数":
		return strconv.Itoa(s.MaxIterations)
	case "子代理":
		if s.SubAgents {
			return "开启"
		}
		return "关闭"
	case "需确认的工具 ask_tools":
		return strings.Join(s.AskTools, ", ")
	case "禁用的工具 deny_tools":
		return strings.Join(s.DenyTools, ", ")
	}
	return ""
}

func (m *uiModel) handleSettingsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "shift+tab":
		if m.settingsSel > 0 {
			m.settingsSel--
		}
	case "down", "tab":
		if m.settingsSel < len(settingsFields)-1 {
			m.settingsSel++
		}
	case "enter":
		f := settingsFields[m.settingsSel]
		s := m.curSettings()
		switch f.kind {
		case "select":
			// 循环切换提供商
			for i, p := range settings.ProviderChoices {
				if s.Provider == p {
					s.Provider = settings.ProviderChoices[(i+1)%len(settings.ProviderChoices)]
					break
				}
			}
			m.afterSettingsChange()
		case "bool":
			s.SubAgents = !s.SubAgents
			m.afterSettingsChange()
		case "text":
			m.settingsEdit = true
			m.settingsBuf = m.fieldValue(m.settingsSel)
		}
	case "esc":
		m.settingsScreen = false
		m.settingsEdit = false
		m.status = ""
	case "ctrl+c":
		m.settingsScreen = false
		m.settingsEdit = false
	}
	return nil
}

func (m *uiModel) handleSettingsEdit(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyRunes:
		ins := []rune(string(msg.Runes))
		m.settingsBuf += string(ins)
	case tea.KeyBackspace:
		if len(m.settingsBuf) > 0 {
			m.settingsBuf = m.settingsBuf[:len(m.settingsBuf)-1]
		}
	case tea.KeyEnter:
		m.commitSettingsEdit()
	case tea.KeyEsc:
		m.settingsEdit = false
	case tea.KeyCtrlC:
		m.settingsEdit = false
	}
	return nil
}

func (m *uiModel) commitSettingsEdit() {
	idx := m.settingsSel
	label := settingsFields[idx].label
	s := m.curSettings()
	val := strings.TrimSpace(m.settingsBuf)
	var err error
	switch label {
	case "模型 model":
		s.Model = val
	case "API 地址 base_url":
		s.BaseURL = strings.TrimRight(val, "/")
	case "API 密钥 api_key":
		s.APIKey = val
	case "温度 temperature":
		var f float64
		if f, err = strconv.ParseFloat(val, 64); err == nil {
			s.Temperature = f
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
	case "需确认的工具 ask_tools":
		s.AskTools = settings.ParseToolList(val)
	case "禁用的工具 deny_tools":
		s.DenyTools = settings.ParseToolList(val)
	}
	m.settingsEdit = false
	if err != nil {
		m.status = "输入无效: " + err.Error()
		return
	}
	m.afterSettingsChange()
}

// afterSettingsChange 本地模式立即应用设置；远程模式推送到服务器。
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
	if m.width == 0 {
		return "加载中…"
	}
	if m.settingsScreen {
		return m.renderSettings()
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
		"Enter 发送 · Tab 文件栏 · s 设置 · /clear 清空 · Esc 取消 · Ctrl+C 退出"))
	return sb.String()
}

func (m *uiModel) renderSettings() string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Bold(true).Render(" 设置"))
	sb.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(
		"↑↓ 选择 · Enter 编辑/切换 · Esc 返回"))
	sb.WriteString("\n" + strings.Repeat("─", m.width) + "\n")

	avail := m.height - 5
	start := 0
	if m.settingsSel >= avail-1 {
		start = m.settingsSel - avail + 2
	}
	for i := start; i < len(settingsFields) && i < start+avail; i++ {
		f := settingsFields[i]
		value := m.fieldValue(i)
		line := "  " + f.label + ": " + value
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
		if i == m.settingsSel {
			style = style.Background(lipgloss.Color("#21262d")).Bold(true).Foreground(lipgloss.Color("#58a6ff"))
			line = "▸" + strings.TrimPrefix(line, " ")
		}
		if m.settingsEdit && i == m.settingsSel {
			line = "  " + f.label + ": " + m.settingsBuf + lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Render("▌")
		}
		sb.WriteString(style.Render(line) + "\n")
	}
	if m.remote == nil {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(
			"  修改后立即生效。提供商可用: openai | claude | ollama | gemini"))
	} else {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(
			"  修改后实时推送到服务器，推理使用服务器设置。"))
	}
	return sb.String()
}

func (m *uiModel) statusText() string {
	if m.asking != nil {
		return m.status
	}
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
			lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render("  输入问题开始对话；按 s 打开设置；Tab 浏览文件。"),
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
