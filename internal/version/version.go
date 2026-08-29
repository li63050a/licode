// Package version 实现 licode 的版本计数。
// 版本为四位点分格式，每位 0..100，满 100 向高位进位：
// 0.0.0.0 → 0.0.0.1 → … → 0.0.0.100 → 0.0.1.0 → …
// 计数保存在用户数据目录的 version 文件中，每次启动递增。
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Version 可由构建时通过 -ldflags "-X internal/version.Version=..." 覆盖。
var Version = ""

// Base 是每一位的进制（0..100 共 101 个值）。
const Base = 101

func baseDir() string {
	if v := os.Getenv("LICODE_HOME"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".licode")
	}
	return ".licode"
}

func counterPath() string {
	return filepath.Join(baseDir(), "version")
}

// Render 把整数渲染为四位点分版本（101 进制）。
func Render(n int) string {
	if n < 0 {
		n = 0
	}
	d0 := n % Base
	d1 := (n / Base) % Base
	d2 := (n / (Base * Base)) % Base
	d3 := (n / (Base * Base * Base)) % Base
	return fmt.Sprintf("%d.%d.%d.%d", d3, d2, d1, d0)
}

// Parse 解析四位点分版本为整数。
func Parse(s string) int {
	parts := strings.Split(s, ".")
	n := 0
	for _, p := range parts {
		d, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0
		}
		n = n*Base + d
	}
	return n
}

// readCounter 读取当前计数。
func readCounter() int {
	data, err := os.ReadFile(counterPath())
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func writeCounter(n int) error {
	_ = os.MkdirAll(baseDir(), 0o755)
	return os.WriteFile(counterPath(), []byte(strconv.Itoa(n)), 0o644)
}

// Bump 计数 +1 并持久化，返回新版本号。
func Bump() string {
	n := readCounter() + 1
	_ = writeCounter(n)
	return Render(n)
}

// Current 返回当前版本号（有构建时嵌入值则用嵌入值）。
func Current() string {
	if Version != "" {
		return Version
	}
	return Render(readCounter())
}
