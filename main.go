package main

import (
	"os"

	"licode/cmd"
)

func main() {
	// licode 直接运行即启动 Web 服务器，参数直接跟在命令后。
	root := cmd.NewServeCommand()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}