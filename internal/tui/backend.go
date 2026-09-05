package tui

import (
	"context"
	"fmt"
	"log"
	"sync"

	"licode/internal/agent"
	"licode/internal/ai"
	"licode/internal/session"
	"licode/internal/settings"
)

// Backend 封装 TUI 所需的所有后端能力（设置、会话、Agent 运行）。
type Backend struct {
	mu       sync.Mutex
	settings *settings.Settings
	sessions *session.Manager
	client   ai.LLMClient
	agent    *agent.Agent
	running  bool
	cancel   context.CancelFunc
}

// NewBackend 创建 TUI 后端，加载配置与会话。
func NewBackend() (*Backend, error) {
	s, err := settings.Load()
	if err != nil {
		return nil, err
	}
	s.EnsureDefaults()
	client, err := s.NewClient()
	if err != nil {
		log.Printf("LLM 客户端初始化失败: %v（可在设置中修改后重试）", err)
		client = nil
	}
	mgr := session.NewManager(settings.SessionsDir(), true)
	return &Backend{settings: &s, sessions: mgr, client: client}, nil
}

// Settings 返回当前设置。
func (b *Backend) Settings() *settings.Settings {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.settings
}

// RebuildClient 在设置变更后重建 LLM 客户端。
func (b *Backend) RebuildClient() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rebuildClientLocked()
}

func (b *Backend) rebuildClientLocked() error {
	if b.client != nil {
		if c, ok := b.client.(interface{ Close() }); ok {
			c.Close()
		}
	}
	client, err := b.settings.NewClient()
	if err != nil {
		b.client = nil
		return err
	}
	b.client = client
	return nil
}

// Sessions 返回会话列表。
func (b *Backend) Sessions() []session.Info {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions.List()
}

// CurrentID 返回当前会话 ID。
func (b *Backend) CurrentID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions.CurrentID()
}

// CurrentSession 返回当前会话的完整消息历史。
func (b *Backend) CurrentSessionMessages() []ai.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions.Current().Messages()
}

// SessionUsage 返回当前会话的 token 用量。
func (b *Backend) SessionUsage() ai.Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions.Current().Usage()
}

// SwitchSession 切换到指定会话。
func (b *Backend) SwitchSession(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions.SetCurrent(id)
	_ = b.sessions.SaveAll()
}

// NewSession 创建新会话。
func (b *Backend) NewSession() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions.New()
	_ = b.sessions.SaveAll()
}

// DeleteSession 删除会话。
func (b *Backend) DeleteSession(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions.Delete(id)
	_ = b.sessions.SaveAll()
}

// BranchSession 从当前会话创建分支。
func (b *Backend) BranchSession() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions.Branch(b.sessions.CurrentID(), -1)
	_ = b.sessions.SaveAll()
}

// RenameSession 重命名会话。
func (b *Backend) RenameSession(id, title string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions.Rename(id, title)
	_ = b.sessions.SaveAll()
}

// RunAgent 运行 Agent 并流式回传事件。返回一个 channel 用于接收事件。
// 如果 busy 则返回 nil 和错误。
func (b *Backend) RunAgent(ctx context.Context, input string) (<-chan agent.Event, error) {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil, fmt.Errorf("上一条消息仍在处理中")
	}
	if b.client == nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("LLM 客户端未配置，请先在设置中配置 API 密钥")
	}
	b.running = true
	s := b.settings.Snapshot()
	sess := b.sessions.Current()
	ag := s.BuildAgent(b.client)
	ag.Session = sess
	ag.Ask = func(ctx context.Context, toolName, args string) (bool, error) {
		if s.AutoAllow || sess.AlwaysAllowed(toolName) {
			return true, nil
		}
		// TUI 模式下默认允许所有工具
		return true, nil
	}

	var cancelCtx context.Context
	cancelCtx, b.cancel = context.WithCancel(ctx)
	b.mu.Unlock()

	events := make(chan agent.Event, 64)
	go func() {
		defer close(events)
		_ = ag.RunWithAttachments(cancelCtx, input, nil, func(e agent.Event) {
			events <- e
		})
		b.mu.Lock()
		b.running = false
		b.cancel = nil
		_ = b.sessions.SaveAll()
		b.mu.Unlock()
	}()
	return events, nil
}

// Interrupt 中断当前正在运行的 Agent。
func (b *Backend) Interrupt() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
}

// IsRunning 返回是否正在运行 Agent。
func (b *Backend) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// UpdateSettingsFn 是设置更新函数签名。
type UpdateSettingsFn func(fn func(s *settings.Settings))

// UpdateSettings 在锁内更新设置。
func (b *Backend) UpdateSettings(fn func(s *settings.Settings)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(b.settings)
	_ = b.settings.Save("")
}

// RefreshClients 重建所有客户端（设置变更后调用）。
func (b *Backend) RefreshClients() {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.rebuildClientLocked()
}

// SetClient 设置 LLM 客户端（测试用）。
func (b *Backend) SetClient(c ai.LLMClient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.client = c
}
