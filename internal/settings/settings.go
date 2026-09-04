// Package settings 提供运行时可变的应用设置。不再使用任何配置文件，
// 设置可在 TUI / Web / 远程连接界面中实时修改，并立即生效。
package settings

import (
	"context"
	"encoding/json"
	"strings"

	"licode/internal/agent"
	"licode/internal/ai"
	"licode/internal/plugin"
)

// ProviderChoices 是可选厂商。
var ProviderChoices = []string{"openai", "claude", "ollama", "gemini"}

// ProviderConfig 描述一个已配置的厂商条目（可自定义名称与协议类型）。
type ProviderConfig struct {
	Provider string `json:"provider"` // 标识（内置名或自定义）
	Name     string `json:"name"`     // 自定义显示名称
	Type     string `json:"type"`     // 协议类型：openai/claude/ollama/gemini；空按 Provider 推断
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

// resolveType 推断协议类型（openai / claude / google；ollama 归 openai）。
func (p *ProviderConfig) resolveType() string {
	t := strings.ToLower(p.Type)
	switch t {
	case "openai", "claude", "google":
		return t
	case "ollama":
		return "openai"
	case "gemini", "anthropic":
		if t == "gemini" {
			return "google"
		}
		return "claude"
	}
	switch strings.ToLower(p.Provider) {
	case "openai", "ollama", "custom":
		return "openai"
	case "claude", "anthropic":
		return "claude"
	case "gemini", "google":
		return "google"
	}
	return "openai" // 自定义厂商默认 OpenAI 兼容
}

// DisplayName 显示名称：Name > Provider。
func (p *ProviderConfig) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.Provider
}

// MCPServer 描述一个 MCP 服务器（stdio 进程）。
type MCPServer = agent.MCPServer

// Settings 是运行时可变的全部应用设置。
type Settings struct {
	Provider      string            `json:"provider"`
	BaseURL       string            `json:"base_url"`
	APIKey        string            `json:"api_key"`
	Model         string            `json:"model"`
	Providers     []ProviderConfig  `json:"providers"` // 已配置的多个厂商
	Temperature   float64           `json:"temperature"`
	MaxTokens     int               `json:"max_tokens"`
	MaxIterations int               `json:"max_iterations"`
	SubAgents     bool              `json:"subagents"`
	AskTools      []string          `json:"ask_tools"`
	DenyTools     []string          `json:"deny_tools"`
	Compaction    bool              `json:"compaction"`     // 上下文超限时用 LLM 压缩
	TitleGen      bool              `json:"title_gen"`      // 自动生成对话标题
	AutoAllow     bool              `json:"auto_allow"`     // 风险工具自动允许
	Streaming     *bool             `json:"streaming"`      // 流式输出（nil=默认开）
	ToolRules     map[string]string `json:"tool_rules"`     // 工具名 -> allow/ask/deny
	MCPServers    []MCPServer       `json:"mcp_servers"`    // MCP 服务器列表
	ShellPath     string            `json:"shell_path"`     // Shell 路径（默认 /bin/sh）
	RetryMax      int               `json:"retry_max"`      // LLM 调用失败重试次数（0=关闭）
	SubTimeout    int               `json:"sub_timeout"`    // 子代理硬超时（秒，0=不限制）
	MaxCtxTokens  int               `json:"max_ctx_tokens"` // 上下文窗口保护阈值（0=关闭）
	RedactSecrets bool              `json:"redact_secrets"` // 工具输出敏感信息脱敏
	Sandbox       bool              `json:"sandbox"`        // 使用沙箱执行 Shell（Docker）
	SandboxImage  string            `json:"sandbox_image"`  // 沙箱镜像（默认 alpine）
	// 特性1：语义缓存
	CacheEnabled bool `json:"cache_enabled"` // 开启问题-结果缓存
	CacheTTL     int  `json:"cache_ttl"`     // 缓存有效期（秒，默认 3600）
	// 特性3：迭代式工具调用
	ToolAutoRetry bool `json:"tool_auto_retry"` // 空结果/错误时自动重试
	ToolRetryMax  int  `json:"tool_retry_max"`  // 最多重试次数（默认 3）
	// 特性4：优雅退出等待时间
	ShutdownTimeout int `json:"shutdown_timeout"` // 关停等待当前任务完成的秒数（默认 30）
	// 特性5：轻量 RAG
	RAGEnabled  bool   `json:"rag_enabled"`   // 索引项目源码并注入上下文
	RAGSource   string `json:"rag_source"`    // 索引根目录（默认当前工作目录）
	RAGTopFiles int    `json:"rag_top_files"` // 注入的最大文件数（默认 5）
}

// Defaults 返回合并了配置文件、环境变量与内置默认值的初始设置。
func Defaults() Settings {
	s, _ := Load()
	return s
}

// ApplyFlags 用命令行参数覆盖初始设置。
func (s *Settings) ApplyFlags(noSubAgents bool) {
	if noSubAgents {
		s.SubAgents = false
	}
	s.UpsertActive()
	s.syncTopLevel()
}

// UpsertActive 把当前激活厂商的信息写回 Providers 列表。
func (s *Settings) UpsertActive() {
	for i := range s.Providers {
		if s.Providers[i].Provider == s.Provider {
			if s.BaseURL != "" {
				s.Providers[i].BaseURL = s.BaseURL
			}
			if s.APIKey != "" {
				s.Providers[i].APIKey = s.APIKey
			}
			if s.Model != "" {
				s.Providers[i].Model = s.Model
			}
			s.Providers[i].Type = s.Providers[i].resolveType()
			return
		}
	}
	pc := ProviderConfig{
		Provider: s.Provider,
		BaseURL:  s.BaseURL,
		APIKey:   s.APIKey,
		Model:    s.Model,
	}
	if d, ok := ai.Defaults[strings.ToLower(s.Provider)]; ok && pc.BaseURL == "" {
		pc.BaseURL = d.BaseURL
	}
	if d, ok := ai.Defaults[strings.ToLower(s.Provider)]; ok && pc.Model == "" {
		pc.Model = d.Model
	}
	pc.Type = pc.resolveType()
	s.Providers = append(s.Providers, pc)
}

// syncTopLevel 把激活厂商条目同步到顶层字段。
func (s *Settings) syncTopLevel() {
	if s.Provider == "" {
		return
	}
	for _, p := range s.Providers {
		if p.Provider == s.Provider {
			s.BaseURL = p.BaseURL
			s.APIKey = p.APIKey
			if s.Model == "" || p.Model != "" {
				s.Model = p.Model
			}
			return
		}
	}
}

// ActiveProvider 返回当前激活的厂商配置。
func (s *Settings) ActiveProvider() ProviderConfig {
	for _, p := range s.Providers {
		if p.Provider == s.Provider {
			if p.Model == "" {
				p.Model = s.Model
			}
			return p
		}
	}
	return ProviderConfig{Provider: s.Provider, BaseURL: s.BaseURL, APIKey: s.APIKey, Model: s.Model}
}

// SetActiveProvider 切换到指定厂商（未配置则创建默认条目）。
func (s *Settings) SetActiveProvider(name string) {
	s.Provider = name
	s.UpsertActive()
	s.syncTopLevel()
}

// AIConfig 把激活厂商设置转成 ai.Config。
func (s *Settings) AIConfig() ai.Config {
	pc := s.ActiveProvider()
	return ai.Config{
		Provider: pc.DisplayName(),
		Type:     pc.resolveType(),
		BaseURL:  pc.BaseURL,
		APIKey:   pc.APIKey,
		Model:    pc.Model,
		RetryMax: s.RetryMax,
	}
}

// NewClient 根据激活厂商创建设置 LLM 客户端。
func (s *Settings) NewClient() (ai.LLMClient, error) {
	return ai.New(s.AIConfig())
}

// BuildAgent 根据设置构建一个完整的 Agent（含子代理、Skills、MCP、压缩）。
func (s *Settings) BuildAgent(client ai.LLMClient) *agent.Agent {
	// 特性1：语义缓存（问题-结果缓存，命中即返回，跳过 LLM 调用）
	if s.CacheEnabled {
		client = ai.CacheDecorator(client, ai.NewCache(CacheDir(), s.CacheTTL))
	}
	ag := agent.NewAgent(client, agent.DefaultMainPrompt)
	// 特性6：把 fsnotify 热加载的外部命令工具并入当前 Agent（动态增/删）。
	ag.Tools.MergeFrom(agent.ExternalTools)
	// 附加提示词：~/.licode/md/ 下的所有 .md（默认空）
	if md, err := ReadMDPrompts(MDPromptDir()); err == nil && md != "" {
		ag.System += "\n" + md
	}
	ag.MaxTokens = s.MaxTokens
	ag.MaxIterations = s.MaxIterations
	ag.Temperature = s.Temperature
	ag.Compaction = s.Compaction
	ag.Timeout = s.SubTimeout
	ag.RedactSecrets = s.RedactSecrets
	ag.ToolAutoRetry = s.ToolAutoRetry
	ag.ToolRetryMax = s.ToolRetryMax
	ag.Shell = agent.ShellConfig{Path: s.ShellPath, Sandbox: s.Sandbox, Image: s.SandboxImage}
	if s.MaxCtxTokens > 0 {
		ag.Session.SetMaxTokens(s.MaxCtxTokens)
	}
	ag.Permissions = map[string]string{}
	// 工具规则：tool -> allow/ask/deny（未配置默认为 allow）
	for tool, mode := range s.ToolRules {
		if mode == "deny" || mode == "ask" || mode == "allow" {
			ag.Permissions[tool] = mode
		}
	}
	// 兼容旧的 ask_tools / deny_tools
	for _, t := range s.DenyTools {
		ag.Permissions[t] = "deny"
	}
	for _, t := range s.AskTools {
		ag.Permissions[t] = "ask"
	}
	if s.SubAgents {
		ag.RegisterSubAgents(agent.DefaultSubAgentSpecs(client, ag.Shell, s.SubTimeout))
	}
	agent.RegisterSkills(ag.Tools, agent.LoadSkills(agent.SkillDirs()...))
	_ = agent.RegisterMCPServers(ag.Tools, s.MCPServers)
	// WASM 插件（wazero 沙箱，运行时热加载）
	for _, p := range plugin.Default.Plugins() {
		pp := p
		schema := pp.Schema
		if len(schema) == 0 {
			schema = map[string]any{"type": "object"}
		}
		_ = ag.Tools.Register(agent.Tool{
			Name:        "plugin_" + pp.Name,
			Description: "WASM 插件 " + pp.Name + "：" + pp.Description,
			Schema:      schema,
			Run: func(ctx context.Context, args map[string]any) (string, error) {
				b, _ := json.Marshal(args)
				return pp.Call(ctx, string(b))
			},
		})
	}
	return ag
}

// Snapshot 返回设置的深拷贝，避免并发读写。
func (s *Settings) Snapshot() Settings {
	out := Settings{
		Provider:        s.Provider,
		BaseURL:         s.BaseURL,
		APIKey:          s.APIKey,
		Model:           s.Model,
		Providers:       append([]ProviderConfig{}, s.Providers...),
		Temperature:     s.Temperature,
		MaxTokens:       s.MaxTokens,
		MaxIterations:   s.MaxIterations,
		SubAgents:       s.SubAgents,
		AskTools:        append([]string{}, s.AskTools...),
		DenyTools:       append([]string{}, s.DenyTools...),
		Compaction:      s.Compaction,
		TitleGen:        s.TitleGen,
		AutoAllow:       s.AutoAllow,
		Streaming:       s.Streaming,
		MCPServers:      append([]MCPServer{}, s.MCPServers...),
		ToolRules:       map[string]string{},
		ShellPath:       s.ShellPath,
		RetryMax:        s.RetryMax,
		SubTimeout:      s.SubTimeout,
		MaxCtxTokens:    s.MaxCtxTokens,
		RedactSecrets:   s.RedactSecrets,
		Sandbox:         s.Sandbox,
		SandboxImage:    s.SandboxImage,
		CacheEnabled:    s.CacheEnabled,
		CacheTTL:        s.CacheTTL,
		ToolAutoRetry:   s.ToolAutoRetry,
		ToolRetryMax:    s.ToolRetryMax,
		ShutdownTimeout: s.ShutdownTimeout,
		RAGEnabled:      s.RAGEnabled,
		RAGSource:       s.RAGSource,
		RAGTopFiles:     s.RAGTopFiles,
	}
	for k, v := range s.ToolRules {
		out.ToolRules[k] = v
	}
	return out
}

// Validate 校验设置值是否可用。
func (s *Settings) Validate() error {
	_, err := ai.New(s.AIConfig())
	return err
}

// EnsureDefaults 补全默认值（提供商模型、风险工具规则等）。
func (s *Settings) EnsureDefaults() {
	_ = s.finalize()
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
