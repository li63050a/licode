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
		Long: `licode —— 类 OpenCode 的 AI 编程助手。

在本地终端（tui）或浏览器（serve）中使用，支持多 AI 提供商一键切换、
工具调用、子代理并行编排，以及远程瘦客户端模式。`,
		SilenceUsage: true,
	}
	root.AddCommand(cmd.NewTUICommand())
	root.AddCommand(cmd.NewServeCommand())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}