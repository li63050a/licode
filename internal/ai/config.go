package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// LoadConfig reads an optional JSON config file and overlays the result on cfg.
// Looked up in order: <path> (if given), .licode.json (cwd), ~/.licode.json.
// Explicit fields in cfg (e.g. from CLI flags) always win over the file.
func LoadConfig(cfg Config, path string) (Config, error) {
	var found string
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return cfg, fmt.Errorf("config file: %w", err)
		}
		found = path
	} else {
		for _, p := range []string{
			filepath.Join(".", ".licode.json"),
			filepath.Join(homeDir(), ".licode.json"),
		} {
			if _, err := os.Stat(p); err == nil {
				found = p
				break
			}
		}
	}
	if found != "" {
		data, err := os.ReadFile(found)
		if err != nil {
			return cfg, fmt.Errorf("read config %s: %w", found, err)
		}
		var fileCfg Config
		if err := json.Unmarshal(data, &fileCfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", found, err)
		}
		if cfg.Provider == "" {
			cfg.Provider = fileCfg.Provider
		}
		if cfg.BaseURL == "" {
			cfg.BaseURL = fileCfg.BaseURL
		}
		if cfg.APIKey == "" {
			cfg.APIKey = fileCfg.APIKey
		}
		if cfg.Model == "" {
			cfg.Model = fileCfg.Model
		}
	}
	return cfg, nil
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
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
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}