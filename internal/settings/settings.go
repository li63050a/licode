// Package settings 提供运行时可变的应用设置。不再使用任何配置文件，
// 设置可在 TUI / Web / 远程连接界面中实时修改，并立即生效。
package settings

import (
	"os"
	"strings"

	"licode/internal/agent"
	"licode/internal/ai"
)

// ProviderChoices 是设置界面可选的提供商。
var ProviderChoices = []string{"openai", "claude", "ollama", "gemini"}

// Settings 是运行时可变的全部应用设置。
type Settings struct {
	Provider      string   `json:"provider"`
	BaseURL       string   `json:"base_url"`
	APIKey        string   `json:"api_key"`
	Model         string   `json:"model"`
	Temperature   float64  `json:"temperature"`
	MaxTokens     int      `json:"max_tokens"`
	MaxIterations int      `json:"max_iterations"`
	SubAgents     bool     `json:"subagents"`
	AskTools      []string `json:"ask_tools"`  // 需要用户确认的工具
	DenyTools     []string `json:"deny_tools"` // 禁用的工具
}

// Defaults 从环境变量与内置默认值构造初始设置。
func Defaults() Settings {
	s := Settings{
		Provider:      os.Getenv("LICODE_PROVIDER"),
		Temperature:   0.7,
		MaxTokens:     4096,
		MaxIterations: 16,
		SubAgents:     true,
	}
	if s.Provider == "" {
		s.Provider = "openai"
	}
	if d, ok := ai.Defaults[s.Provider]; ok {
		s.BaseURL = d.BaseURL
		s.Model = d.Model
	}
	if v := os.Getenv("LICODE_BASE_URL"); v != "" {
		s.BaseURL = v
	}
	if v := os.Getenv("LICODE_MODEL"); v != "" {
		s.Model = v
	}
	if v := os.Getenv("LICODE_API_KEY"); v != "" {
		s.APIKey = v
	} else if v := providerEnvKey(s.Provider); v != "" {
		s.APIKey = v
	}
	return s
}

// ApplyFlags 用命令行参数覆盖初始设置。
func (s *Settings) ApplyFlags(provider, baseURL, apiKey, model string, noSubAgents bool) {
	if provider != "" {
		provider = strings.ToLower(provider)
		providerChanged := s.Provider != provider
		s.Provider = provider
		if d, ok := ai.Defaults[provider]; ok {
			if s.BaseURL == "" || providerChanged {
				s.BaseURL = d.BaseURL
			}
			// 提供商变化时，若无显式模型，则跟随新提供商的默认模型。
			if providerChanged && model == "" {
				s.Model = d.Model
			}
		}
	}
	if baseURL != "" {
		s.BaseURL = baseURL
	}
	if apiKey != "" {
		s.APIKey = apiKey
	}
	if model != "" {
		s.Model = model
	}
	if noSubAgents {
		s.SubAgents = false
	}
}

func providerEnvKey(provider string) string {
	switch provider {
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "claude":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "gemini":
		return os.Getenv("GEMINI_API_KEY")
	}
	return ""
}

// AIConfig 把设置转成 ai.Config。
func (s *Settings) AIConfig() ai.Config {
	return ai.Config{
		Provider: s.Provider,
		BaseURL:  s.BaseURL,
		APIKey:   s.APIKey,
		Model:    s.Model,
	}
}

// NewClient 根据设置创建 LLM 客户端。
func (s *Settings) NewClient() (ai.LLMClient, error) {
	return ai.New(s.AIConfig())
}

// BuildAgent 根据设置构建一个完整的 Agent。
func (s *Settings) BuildAgent(client ai.LLMClient) *agent.Agent {
	ag := agent.NewAgent(client, agent.DefaultMainPrompt)
	ag.MaxTokens = s.MaxTokens
	ag.MaxIterations = s.MaxIterations
	ag.Temperature = s.Temperature
	ag.Permissions = map[string]string{}
	for _, t := range s.DenyTools {
		ag.Permissions[t] = "deny"
	}
	for _, t := range s.AskTools {
		ag.Permissions[t] = "ask"
	}
	if s.SubAgents {
		ag.RegisterSubAgents(agent.DefaultSubAgentSpecs(client))
	}
	return ag
}

// Snapshot 返回设置的深拷贝，避免并发读写。
func (s *Settings) Snapshot() Settings {
	return Settings{
		Provider:      s.Provider,
		BaseURL:       s.BaseURL,
		APIKey:        s.APIKey,
		Model:         s.Model,
		Temperature:   s.Temperature,
		MaxTokens:     s.MaxTokens,
		MaxIterations: s.MaxIterations,
		SubAgents:     s.SubAgents,
		AskTools:      append([]string{}, s.AskTools...),
		DenyTools:     append([]string{}, s.DenyTools...),
	}
}

// Validate 校验设置值是否可用。
func (s *Settings) Validate() error {
	_, err := ai.New(s.AIConfig())
	return err
}

// ParseToolList 解析逗号分隔的工具列表。
func ParseToolList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}