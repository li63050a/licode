package settings

import (
	"os"

	"github.com/BurntSushi/toml"
)

// TOMLConfig 是 config.toml 的完整结构。
// 注意：AI/API 配置只放在 Web 设置里（写入 ~/.licode/config.json），
// 本文件仅包含服务器相关设置。
type TOMLConfig struct {
	Server struct {
		Host     string `toml:"host"`
		Port     int    `toml:"port"`
		Username string `toml:"username"`
		Password string `toml:"password"`
		HTTPS    bool   `toml:"https"`
		TLSCert  string `toml:"tls_cert"`
		TLSKey   string `toml:"tls_key"`
	} `toml:"server"`
}

// DefaultTOML 返回默认配置内容。
func DefaultTOML() TOMLConfig {
	var c TOMLConfig
	c.Server.Host = "127.0.0.1"
	c.Server.Port = 8080
	c.Server.Username = "licode"
	return c
}

// LoadTOML 读取配置文件（不存在时返回 error）。
func LoadTOML(path string) (TOMLConfig, error) {
	var c TOMLConfig
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, err
	}
	return c, nil
}

// GenerateTOML 生成默认配置文件。
func GenerateTOML(path string, c TOMLConfig) error {
	content := "# licode 配置文件\n" +
		"# 启动时默认加载当前目录 config.toml；可用 -c <路径> 指定其他文件。\n" +
		"# 缺失时自动生成。优先级：命令行参数 > 本文件 > 环境变量。\n" +
		"# AI/API（提供商、模型、密钥等）统一在 Web 界面「设置」里配置。\n\n" +
		"[server]\n" +
		"host = \"" + c.Server.Host + "\"\n" +
		"port = " + itoa(c.Server.Port) + "\n" +
		"addr = \"\"\n" +
		"username = \"" + c.Server.Username + "\"\n" +
		"password = \"\"\n" +
		"https = false\n" +
		"tls_cert = \"\"\n" +
		"tls_key = \"\"\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
