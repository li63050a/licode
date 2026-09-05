package main

import (
	"os"

	"licode/cmd"
)

func main() {
	// licode 直接运行即启动 TUI 终端界面，加 web 参数启动 Web 服务器。
	root := cmd.NewTUICommand()
	root.AddCommand(cmd.NewServeCommand())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}