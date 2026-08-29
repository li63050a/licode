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

启动 Web 服务器，浏览器访问即用：http://<host>:<port>。
支持多 AI 提供商一键切换、工具调用、子代理并行编排、多对话、MCP/Skills。

直接运行 licode（不带参数，或只带参数不带子命令）即启动服务器，等价于：
    licode serve [--host 0.0.0.0] [--port 8080] [--password 密码] [--provider openai] ...`,
		SilenceUsage: true,
	}
	root.AddCommand(serve)

	// 默认模式：不带子命令时，把参数转给 serve 子命令。
	args := os.Args[1:]
	if len(args) == 0 || !isSubcommand(args[0]) {
		root.SetArgs(append([]string{"serve"}, args...))
	}

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// isSubcommand 判断第一个参数是否为已注册的子命令（或帮助/补全）。
func isSubcommand(a string) bool {
	switch a {
	case "serve", "help", "completion", "-h", "--help":
		return true
	}
	return false
}