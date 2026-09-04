package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// 配置文件约定：
//   - 用户级：~/.licode/config.json（首次使用自动生成）
//   - 项目级：<项目目录>/licode.json（可选，覆盖用户级）
//   - 修改设置时写回用户级配置文件，并同步项目级（若存在）。
const ProjectConfigName = "licode.json"

// ConfigPaths 返回 [用户级, 项目级] 配置文件路径。
func ConfigPaths() []string {
	var paths []string
	paths = append(paths, ConfigPath())
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, ProjectConfigName))
	}
	return paths
}

// SavePath 返回修改设置时应写入的路径（用户级 ~/.licode/config.json）。
func SavePath() string {
	return ConfigPath()
}

// loadFromFiles 读取用户级与项目级配置文件并合并到 s（项目级优先）。
func (s *Settings) loadFromFiles() {
	paths := ConfigPaths()
	// 先读用户级，再读项目级，后者覆盖前者。
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var file Settings
		if err := json.Unmarshal(data, &file); err != nil {
			continue
		}
		s.mergeFrom(&file)
	}
}

// mergeFrom 把 other 中非零字段合并进 s。
func (s *Settings) mergeFrom(o *Settings) {
	if o.Provider != "" {
		s.Provider = o.Provider
	}
	if o.BaseURL != "" {
		s.BaseURL = o.BaseURL
	}
	if o.APIKey != "" {
		s.APIKey = o.APIKey
	}
	if o.Model != "" {
		s.Model = o.Model
	}
	if len(o.Providers) > 0 {
		s.Providers = append([]ProviderConfig{}, o.Providers...)
	}
	if o.Temperature != 0 {
		s.Temperature = o.Temperature
	}
	if o.MaxTokens != 0 {
		s.MaxTokens = o.MaxTokens
	}
	if o.MaxIterations != 0 {
		s.MaxIterations = o.MaxIterations
	}
	if o.SubAgents {
		s.SubAgents = true
	}
	if len(o.AskTools) > 0 {
		s.AskTools = append([]string{}, o.AskTools...)
	}
	if len(o.DenyTools) > 0 {
		s.DenyTools = append([]string{}, o.DenyTools...)
	}
	s.Compaction = o.Compaction
	s.TitleGen = o.TitleGen
	s.AutoAllow = o.AutoAllow
	if o.Streaming != nil {
		s.Streaming = o.Streaming
	}
	if len(o.MCPServers) > 0 {
		s.MCPServers = append([]MCPServer{}, o.MCPServers...)
	}
	if len(o.ToolRules) > 0 {
		s.ToolRules = map[string]string{}
		for k, v := range o.ToolRules {
			s.ToolRules[k] = v
		}
	}
	if o.ShellPath != "" {
		s.ShellPath = o.ShellPath
	}
	if o.RetryMax != 0 {
		s.RetryMax = o.RetryMax
	}
	if o.SubTimeout != 0 {
		s.SubTimeout = o.SubTimeout
	}
	if o.MaxCtxTokens != 0 {
		s.MaxCtxTokens = o.MaxCtxTokens
	}
	s.RedactSecrets = o.RedactSecrets
	s.Sandbox = o.Sandbox
	if o.SandboxImage != "" {
		s.SandboxImage = o.SandboxImage
	}
}

// Save 把设置写入配置文件（默认 SavePath）。
func (s *Settings) Save(path string) error {
	if path == "" {
		path = SavePath()
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load 返回合并了配置文件、环境变量与默认值的设置。
// 优先级：默认值 < 配置文件 < 环境变量 < 命令行参数(ApplyFlags)。
func Load() (Settings, error) {
	s := Settings{}
	s.loadFromFiles()
	s.applyEnv()
	if err := s.finalize(); err != nil {
		return s, err
	}
	return s, nil
}

// finalize 填充默认值并确保激活厂商条目存在。
func (s *Settings) finalize() error {
	if s.Provider == "" {
		s.Provider = "openai"
	}
	// 默认：写文件/执行 shell 属于风险操作，默认询问
	if len(s.ToolRules) == 0 && len(s.AskTools) == 0 && len(s.DenyTools) == 0 {
		s.ToolRules = map[string]string{
			"Write": "ask",
			"Shell": "ask",
			"Delete": "ask",
		}
	}
	if s.Streaming == nil {
		t := true
		s.Streaming = &t
	}
	s.UpsertActive()
	s.syncTopLevel()
	if s.Temperature == 0 {
		s.Temperature = 0.7
	}
	if s.MaxTokens == 0 {
		s.MaxTokens = 4096
	}
	if s.MaxIterations == 0 {
		s.MaxIterations = 16
	}
	if s.SubAgents {
		// 默认开
	}
	_ = s.Validate()
	return nil
}

// applyEnv 用环境变量覆盖设置。
func (s *Settings) applyEnv() {
	env := func(name string) string { return os.Getenv(name) }
	if v := env("LICODE_PROVIDER"); v != "" {
		s.Provider = v
	}
	if v := env("LICODE_BASE_URL"); v != "" {
		s.BaseURL = v
	}
	if v := env("LICODE_API_KEY"); v != "" {
		s.APIKey = v
	}
	if v := env("LICODE_MODEL"); v != "" {
		s.Model = v
	}
	if s.APIKey == "" {
		switch s.Provider {
		case "openai":
			s.APIKey = env("OPENAI_API_KEY")
		case "claude":
			s.APIKey = env("ANTHROPIC_API_KEY")
		case "gemini":
			s.APIKey = env("GEMINI_API_KEY")
		}
	}
}

// ErrNoConfig 表明没有找到任何配置文件。
var ErrNoConfig = errors.New("未找到配置文件")

// SaveConfigFileForTest 供测试导出。
func SaveConfigFileForTest(s Settings, path string) error {
	return s.Save(path)
}
