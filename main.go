package main

import (
	"os"

	"github.com/spf13/cobra"

	"licode/cmd"
)

func main() {
	root := &cobra.Command{
		Use:   "licode",
		Short: "终端里的 AI 编程助手",
		Long: `licode —— 终端里的 AI 编程助手。

在本地终端（tui）或浏览器（serve）中使用，支持多 AI 提供商一键切换、
工具调用、子代理并行编排，以及远程瘦客户端模式。

直接运行 licode（不带参数）即进入 TUI 界面；设置可在 TUI / Web / 远程
界面中实时修改，无需配置文件。`,
		SilenceUsage: true,
	}
	tui := cmd.NewTUICommand()
	serve := cmd.NewServeCommand()
	root.AddCommand(tui, serve)

	// 默认模式：不带任何参数时直接进入 TUI。
	if len(os.Args) == 1 {
		root.SetArgs([]string{"tui"})
	}

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}