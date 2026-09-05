package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"licode/internal/agent"
)

// Model 是 TUI 主模型。
type Model struct {
	backend *Backend
	width   int
	height  int

	// 聊天
	messages []agent.Event

	// 输入
	input     string
	history   []string
	histIndex int

	// 状态
	running bool
	busy    bool

	// 会话
	showSessions bool
	sessions     []sessionInfo
	sessSelected int

	// 事件通道（从 goroutine 接收）
	eventCh chan agent.Event
}

type sessionInfo struct {
	id    string
	title string
	count int
}

func NewModel(backend *Backend) *Model {
	return &Model{backend: backend, eventCh: make(chan agent.Event, 128)}
}

func (m *Model) Init() tea.Cmd {
	m.refreshSessions()
	return nil
}

func (m *Model) refreshSessions() {
	infos := m.backend.Sessions()
	m.sessions = make([]sessionInfo, len(infos))
	for i, s := range infos {
		m.sessions[i] = sessionInfo{id: s.ID, title: s.Title, count: s.Count}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case agent.Event:
		m.messages = append(m.messages, msg)
		if msg.Type == agent.EventDone || msg.Type == agent.EventError {
			m.busy = false
			m.running = false
		}
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showSessions {
		return m.handleSessionKeys(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		if m.running {
			m.backend.Interrupt()
			return m, nil
		}
		return m, tea.Quit
	case "tab":
		m.showSessions = true
		m.refreshSessions()
		return m, nil
	case "enter":
		return m.sendInput()
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "up":
		if len(m.history) > 0 && m.histIndex < len(m.history)-1 {
			if m.histIndex == -1 {
				m.histIndex = len(m.history) - 1
			} else if m.histIndex > 0 {
				m.histIndex--
			}
			m.input = m.history[m.histIndex]
		}
	case "down":
		if m.histIndex >= 0 {
			m.histIndex++
			if m.histIndex >= len(m.history) {
				m.histIndex = -1
				m.input = ""
			} else {
				m.input = m.history[m.histIndex]
			}
		}
	case "ctrl+u":
		m.input = ""
	default:
		if msg.Type == tea.KeyRunes && len(msg.String()) == 1 {
			m.input += msg.String()
		}
	}
	return m, nil
}

func (m *Model) handleSessionKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "q", "esc":
		m.showSessions = false
	case "j", "down":
		if m.sessSelected < len(m.sessions)-1 {
			m.sessSelected++
		}
	case "k", "up":
		if m.sessSelected > 0 {
			m.sessSelected--
		}
	case "enter":
		if m.sessSelected < len(m.sessions) {
			m.backend.SwitchSession(m.sessions[m.sessSelected].id)
			m.messages = nil
		}
		m.showSessions = false
	case "n":
		m.backend.NewSession()
		m.refreshSessions()
		m.showSessions = false
		m.messages = nil
	case "d":
		if m.sessSelected < len(m.sessions) {
			m.backend.DeleteSession(m.sessions[m.sessSelected].id)
			m.refreshSessions()
			if m.sessSelected >= len(m.sessions) && m.sessSelected > 0 {
				m.sessSelected--
			}
		}
	}
	return m, nil
}

func (m *Model) sendInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input)
	if text == "" || m.busy {
		return m, nil
	}
	m.history = append(m.history, text)
	m.histIndex = -1
	m.input = ""

	// 添加用户消息
	m.messages = append(m.messages, agent.Event{Type: agent.EventText, Content: "你: " + text})

	events, err := m.backend.RunAgent(context.Background(), text)
	if err != nil {
		m.messages = append(m.messages, agent.Event{Type: agent.EventError, Content: err.Error()})
		return m, nil
	}
	m.busy = true
	m.running = true

	// 启动 goroutine 收集事件到 channel
	go func() {
		for e := range events {
			m.eventCh <- e
		}
	}()

	// 返回 tick cmd 轮询事件
	return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		select {
		case e := <-m.eventCh:
			return e
		default:
			return nil
		}
	})
}

func (m *Model) View() string {
	if m.showSessions {
		return m.viewSessions()
	}
	return m.viewChat()
}

func (m *Model) viewChat() string {
	var b strings.Builder

	// 标题
	b.WriteString(titleStyle.Render(" licode TUI "))
	b.WriteString("\n")

	// 聊天区域
	chatH := m.height - 6
	if chatH < 3 {
		chatH = 3
	}
	msgStr := m.renderMessages()
	lines := strings.Split(msgStr, "\n")
	if len(lines) > chatH {
		lines = lines[len(lines)-chatH:]
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}

	// 填充空白
	for i := len(lines); i < chatH; i++ {
		b.WriteString("\n")
	}

	// 分隔线
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n")

	// 状态栏
	b.WriteString(m.viewStatus())
	b.WriteString("\n")

	// 输入框
	b.WriteString("› ")
	if m.input == "" {
		b.WriteString(helpStyle.Render("输入消息，Enter 发送，Tab 切换会话..."))
	} else {
		b.WriteString(m.input)
	}

	return b.String()
}

func (m *Model) renderMessages() string {
	var lines []string
	for _, evt := range m.messages {
		switch evt.Type {
		case agent.EventText:
			if strings.HasPrefix(evt.Content, "你: ") {
				lines = append(lines, userMsgStyle.Render("你: ")+evt.Content[3:])
			} else {
				lines = append(lines, aiMsgStyle.Render("AI: ")+evt.Content)
			}
		case agent.EventToolStart:
			lines = append(lines, toolStyle.Render(fmt.Sprintf("🔧 %s(%s)", evt.ToolName, evt.ToolArgs)))
		case agent.EventToolDone:
			out := evt.ToolOut
			if len(out) > 100 {
				out = out[:100] + "…"
			}
			lines = append(lines, toolStyle.Render(fmt.Sprintf("✅ %s → %s", evt.ToolName, out)))
		case agent.EventError:
			lines = append(lines, errorStyle.Render("错误: "+evt.Error))
		case agent.EventStatus:
			lines = append(lines, statusStyle.Render(evt.Content))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) viewStatus() string {
	s := m.backend.Settings()
	parts := []string{
		fmt.Sprintf("模型: %s", s.Model),
		fmt.Sprintf("提供商: %s", s.Provider),
	}
	if m.running {
		parts = append(parts, statusStyle.Render("运行中..."))
	}
	u := m.backend.SessionUsage()
	if u.InputTokens > 0 {
		parts = append(parts, fmt.Sprintf("Token: %d/%d", u.InputTokens, u.OutputTokens))
	}
	return statusStyle.Render(" " + strings.Join(parts, " · ") + " ")
}

func (m *Model) viewSessions() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" 会话列表 "))
	b.WriteString("\n\n")
	for i, s := range m.sessions {
		style := baseStyle
		if i == m.sessSelected {
			style = selectedStyle
		}
		title := s.title
		if len(title) > 20 {
			title = title[:20] + "…"
		}
		b.WriteString(style.Render(fmt.Sprintf(" %s (%d 条)", title, s.count)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(" j/k 移动 · enter 切换 · n 新建 · d 删除 · tab 返回"))
	return b.String()
}
