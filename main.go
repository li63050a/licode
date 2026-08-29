package main

import (
	"os"

	"github.com/spf13/cobra"

	"licode/cmd"
)

func main() {
	serve := cmd.NewServeCommand()
	root := &cobra.Command{
		Use:   "licode",
		Short: "AI 编程助手（Web 界面）",
		Long: `licode —— AI 编程助手（Web 界面）。

启动 Web 服务器，浏览器访问即用：http://<host>:8080。
支持多 AI 提供商一键切换、工具调用、子代理并行编排、多对话、MCP/Skills。

直接运行 licode（不带参数）即启动服务器，等价于 licode serve。`,
		SilenceUsage: true,
	}
	root.AddCommand(serve)

	// 默认模式：不带任何参数时直接启动 Web 服务器。
	if len(os.Args) == 1 {
		root.SetArgs([]string{"serve"})
	}

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}