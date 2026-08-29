package ai

import (
	"fmt"
	"os"
	"strings"
)

// Config holds connection settings for an LLM provider.
type Config struct {
	Provider string // "openai" | "claude" | "ollama"
	BaseURL  string
	APIKey   string
	Model    string
}

const (
	EnvProvider = "LICODE_PROVIDER"
	EnvBaseURL  = "LICODE_BASE_URL"
	EnvAPIKey   = "LICODE_API_KEY"
	EnvModel    = "LICODE_MODEL"
)

// Default provider settings.
var Defaults = map[string]struct {
	BaseURL string
	Model   string
}{
	"openai": {BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"},
	"claude": {BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4-20250514"},
	"ollama": {BaseURL: "http://localhost:11434", Model: "llama3.1:8b"},
	"gemini": {BaseURL: "https://generativelanguage.googleapis.com", Model: "gemini-2.0-flash"},
}

// Resolve fills empty Config fields from environment variables, then defaults.
// Precedence: explicit field > env var > default.
func (c *Config) Resolve() error {
	if c.Provider == "" {
		c.Provider = os.Getenv(EnvProvider)
	}
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	if c.Provider == "" {
		c.Provider = "openai"
	}
	d, ok := Defaults[c.Provider]
	if !ok {
		return fmt.Errorf("unsupported provider %q (want openai, claude or ollama)", c.Provider)
	}
	if c.BaseURL == "" {
		c.BaseURL = os.Getenv(EnvBaseURL)
	}
	if c.BaseURL == "" {
		c.BaseURL = d.BaseURL
	}
	if c.APIKey == "" {
		c.APIKey = os.Getenv(EnvAPIKey)
		if c.APIKey == "" {
			c.APIKey = providerEnvKey(c.Provider)
		}
	}
	if c.Model == "" {
		c.Model = os.Getenv(EnvModel)
	}
	if c.Model == "" {
		c.Model = d.Model
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return nil
}

// providerEnvKey 返回各提供商标准的 API Key 环境变量名。
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

// New is the provider factory.
func New(cfg Config) (LLMClient, error) {
	if err := cfg.Resolve(); err != nil {
		return nil, err
	}
	switch cfg.Provider {
	case "openai":
		return &OpenAIProvider{baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model}, nil
	case "claude":
		return &ClaudeProvider{baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model}, nil
	case "ollama":
		return &OllamaProvider{baseURL: cfg.BaseURL, model: cfg.Model}, nil
	case "gemini":
		return &GeminiProvider{baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}