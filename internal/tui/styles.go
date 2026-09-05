package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// 基础样式
	baseStyle = lipgloss.NewStyle().
			Padding(0, 1)

	// 标题样式
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 2)

	// 边框样式
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(0, 1)

	// 用户消息样式
	userMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D1FF")).
			Bold(true)

	// AI 消息样式
	aiMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF79C6"))

	// 工具调用样式
	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1FA8C")).
			Italic(true)

	// 状态栏样式
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6272A4")).
			Italic(true)

	// 输入框样式
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#6272A4")).
			Padding(0, 1)

	// 侧边栏样式
	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#6272A4")).
			Width(26).
			Padding(0, 1)

	// 选中样式
	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#44475A")).
			Foreground(lipgloss.Color("#F8F8F2"))

	// 错误样式
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5555")).
			Bold(true)

	// 成功样式
	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#50FA7B"))

	// 帮助样式
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6272A4")).
			Italic(true)
)
