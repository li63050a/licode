package settings

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 用户数据根目录：~/.licode（可用 LICODE_HOME 覆盖）。
// 首次使用后自动生成以下子目录：
//
//	~/.licode/config.json   配置文件（设置/MCP 等）
//	~/.licode/skills/       技能（markdown）
//	~/.licode/mcp/          MCP 服务器配置
//	~/.licode/sessions/     对话记录
//	~/.licode/logs/         日志
//	~/.licode/cache/        缓存
//	~/.licode/md/           附加提示词（自动读取其中所有 .md，默认空）
func BaseDir() string {
	if v := os.Getenv("LICODE_HOME"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".licode")
	}
	return ".licode"
}

func SubDir(name string) string {
	return filepath.Join(BaseDir(), name)
}

func ConfigPath() string  { return filepath.Join(BaseDir(), "config.json") }
func SkillsDir() string   { return filepath.Join(BaseDir(), "skills") }
func MCPDir() string      { return filepath.Join(BaseDir(), "mcp") }
func SessionsDir() string { return filepath.Join(BaseDir(), "sessions") }
func LogsDir() string     { return filepath.Join(BaseDir(), "logs") }
func CacheDir() string    { return filepath.Join(BaseDir(), "cache") }
func MDPromptDir() string { return filepath.Join(BaseDir(), "md") }
func ToolsDir() string    { return filepath.Join(BaseDir(), "tools") }

// SystemPromptPath 系统提示词文件（可直接编辑生效）。
func SystemPromptPath() string { return filepath.Join(BaseDir(), "system-prompt.md") }

// ReadSystemPrompt 读取系统提示词文件；不存在或为空时返回空串。
func ReadSystemPrompt() string {
	data, err := os.ReadFile(SystemPromptPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// EnsureDirs 创建用户数据目录与子目录（首次使用自动生成）。
func EnsureDirs() error {
	dirs := []string{BaseDir(), SkillsDir(), MCPDir(), SessionsDir(), LogsDir(), CacheDir(), MDPromptDir(), ToolsDir()}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// 生成默认配置文件（如果不存在）
	cfg := ConfigPath()
	if _, err := os.Stat(cfg); os.IsNotExist(err) {
		s := Defaults()
		_ = s.Save(cfg)
	}
	return nil
}

// ReadMDPrompts 递归读取目录（含所有嵌套子目录）下所有 .md 文件并拼接为
// 附加提示词（~/.licode/md，默认空）。
func ReadMDPrompts(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	sort.Strings(files)
	var sb strings.Builder
	for _, path := range files {
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		rel, _ := filepath.Rel(dir, path)
		sb.WriteString("\n===== " + rel + " =====\n")
		sb.Write(data)
	}
	return sb.String(), nil
}

// LogFile 返回追加写入的日志文件句柄（~/.licode/logs/licode.log）。
func LogFile() (*os.File, error) {
	_ = EnsureDirs()
	return os.OpenFile(filepath.Join(LogsDir(), "licode.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}
