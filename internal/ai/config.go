package ai

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"licode/internal/dnsclient"
)

// NewLLMHTTPClient 构造一个 *http.Client，并依据 DNS 配置注入自定义解析器。
func (c Config) NewLLMHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout}
	if c.DNSMode != "" && c.DNSMode != "system" && c.DNSServer != "" {
		dc := dnsclient.Config{Mode: dnsclient.Mode(c.DNSMode), Server: c.DNSServer}
		if dial := dc.Resolver(); dial != nil {
			client.Transport = &http.Transport{DialContext: dial, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: timeout}
		}
	}
	return client
}

// Config holds connection settings for an LLM provider.
type Config struct {
	Provider  string // 显示名（如 "openai" 或自定义名称）
	Type      string // 协议类型：openai | claude | ollama | gemini；空则按 Provider 推断，未知按 openai 兼容
	BaseURL   string
	APIKey    string
	Model     string
	RetryMax  int    // LLM 调用失败重试次数（指数退避，处理 429/503/网络抖动）
	DNSMode   string // 自定义 DNS 模式："" | system | plain | dot | doh
	DNSServer string // 自定义 DNS 服务器地址
}

const (
	EnvProvider = "LICODE_PROVIDER"
	EnvBaseURL  = "LICODE_BASE_URL"
	EnvAPIKey   = "LICODE_API_KEY"
	EnvModel    = "LICODE_MODEL"
)

// Default provider settings. 协议类型：openai / claude / google
// （ollama 属 openai 兼容，地址给 /v1）。
var Defaults = map[string]struct {
	BaseURL string
	Model   string
}{
	"openai": {BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"},
	"claude": {BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4-20250514"},
	"ollama": {BaseURL: "http://localhost:11434/v1", Model: "llama3.1:8b"},
	"google": {BaseURL: "https://generativelanguage.googleapis.com", Model: "gemini-2.0-flash"},
}

// normalizeType 规范化协议类型：ollama -> openai（兼容），gemini -> google。
func normalizeType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "ollama":
		return "openai"
	case "gemini", "google":
		return "google"
	case "openai":
		return "openai"
	case "claude", "anthropic":
		return "claude"
	}
	return "openai"
}

// Resolve fills empty Config fields from environment variables, then defaults.
// Precedence: explicit field > env var > default.
// 支持自定义厂商：Provider 可为任意名称，Type 指定协议；未知 Type 按 openai 兼容。
func (c *Config) Resolve() error {
	if c.Provider == "" {
		c.Provider = os.Getenv(EnvProvider)
	}
	c.Provider = strings.TrimSpace(c.Provider)
	if c.Provider == "" {
		c.Provider = "openai"
	}
	if c.Type == "" {
		c.Type = strings.ToLower(c.Provider)
	}
	c.Type = normalizeType(c.Type)
	d, ok := Defaults[c.Type]
	if c.BaseURL == "" {
		c.BaseURL = os.Getenv(EnvBaseURL)
	}
	if c.BaseURL == "" && ok {
		c.BaseURL = d.BaseURL
	}
	if c.APIKey == "" {
		c.APIKey = os.Getenv(EnvAPIKey)
		if c.APIKey == "" {
			c.APIKey = providerEnvKey(c.Type)
		}
	}
	if c.Model == "" {
		c.Model = os.Getenv(EnvModel)
	}
	if c.Model == "" && ok {
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
	case "google":
		return os.Getenv("GEMINI_API_KEY")
	}
	return ""
}

// New is the provider factory.
func New(cfg Config) (LLMClient, error) {
	if err := cfg.Resolve(); err != nil {
		return nil, err
	}
	name := cfg.Provider
	dnsMode, dnsServer := cfg.DNSMode, cfg.DNSServer
	switch cfg.Type {
	case "openai":
		return &OpenAIProvider{name: name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model, retry: cfg.RetryMax, dnsMode: dnsMode, dnsServer: dnsServer}, nil
	case "claude":
		return &ClaudeProvider{name: name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model, retry: cfg.RetryMax, dnsMode: dnsMode, dnsServer: dnsServer}, nil
	case "ollama":
		return &OllamaProvider{name: name, baseURL: cfg.BaseURL, model: cfg.Model, retry: cfg.RetryMax, dnsMode: dnsMode, dnsServer: dnsServer}, nil
	case "google", "gemini":
		return &GeminiProvider{name: name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model, retry: cfg.RetryMax, dnsMode: dnsMode, dnsServer: dnsServer}, nil
	default:
		return nil, fmt.Errorf("unsupported protocol type %q", cfg.Type)
	}
}
