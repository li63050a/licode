// Package cmd 提供 TUI 终端界面命令。
package cmd

import (
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"licode/internal/tui"
)

// NewTUICommand 返回 TUI 命令。
func NewTUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "终端 TUI 界面",
		Long: `licode tui —— 启动终端 TUI 界面。

启动后即可在终端中与 licode 对话，支持：
  - 流式回复（实时显示 AI 输出）
  - 工具调用可视化
  - 会话管理（新建/切换/删除/分支）
  - 快捷键操作

快捷键：
  Enter      发送消息
  Tab        切换会话
  Ctrl+C     停止/退出
  Ctrl+U     清空输入`,
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := tui.NewBackend()
			if err != nil {
				return err
			}
			model := tui.NewModel(backend)
			p := tea.NewProgram(model, tea.WithAltScreen())
			if _, err := p.Run(); err != nil {
				log.Printf("TUI 退出: %v", err)
				return err
			}
			return nil
		},
	}
}
