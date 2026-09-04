package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"licode/internal/ai"
)

// Info 是会话列表中的一条。
type Info struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

// Manager 管理多个会话（对话），支持新建/切换/重命名/删除，并持久化到磁盘。
type Manager struct {
	mu       sync.Mutex
	dir      string
	sessions map[string]*Session
	order    []string
	current  string
	seq      int
}

// NewManager 创建会话管理器。dir 为对话记录目录（~/.licode/sessions），
// load 为 true 时加载已有的对话记录。
func NewManager(dir string, load bool) *Manager {
	m := &Manager{
		dir:      dir,
		sessions: map[string]*Session{},
	}
	if load && dir != "" {
		m.loadExisting()
	}
	if len(m.sessions) == 0 {
		s := NewSession(0)
		s.SetOnChange(func() { m.SaveSession(s.ID()) })
		m.sessions[s.ID()] = s
		m.order = append(m.order, s.ID())
		m.current = s.ID()
	}
	if _, ok := m.sessions[m.current]; !ok {
		m.current = m.order[len(m.order)-1]
	}
	return m
}

func (m *Manager) loadExisting() {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := LoadSessionFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		s.SetOnChange(func() { m.SaveSession(s.ID()) })
		m.sessions[s.ID()] = s
		ids = append(ids, s.ID())
	}
	sort.Strings(ids)
	m.order = ids
}

// SaveAll 把所有会话写入对话记录目录。
func (m *Manager) SaveAll() error {
	if m.dir == "" {
		return nil
	}
	_ = os.MkdirAll(m.dir, 0o755)
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for id, s := range m.sessions {
		if err := s.SaveToFile(filepath.Join(m.dir, id+".json")); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SaveSession 保存单个会话。
func (m *Manager) SaveSession(id string) error {
	if m.dir == "" {
		return nil
	}
	_ = os.MkdirAll(m.dir, 0o755)
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s.SaveToFile(filepath.Join(m.dir, id+".json"))
	}
	return nil
}

// Current 返回当前会话。
func (m *Manager) Current() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[m.current]
}

// CurrentID 返回当前会话 ID。
func (m *Manager) CurrentID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// New 新建一个会话并切换到它。
func (m *Manager) New() *Session {
	const maxSessions = 100
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= maxSessions {
		return m.sessions[m.current]
	}
	s := NewSession(0)
	s.SetOnChange(func() { m.SaveSession(s.ID()) })
	m.seq++
	s.title = "新对话"
	m.sessions[s.ID()] = s
	m.order = append(m.order, s.ID())
	m.current = s.ID()
	return s
}

// NewFromSession 加入一个外部构造的会话（导入时使用）并切换到它。
func (m *Manager) NewFromSession(s *Session) {
	s.SetOnChange(func() { m.SaveSession(s.ID()) })
	m.mu.Lock()
	m.sessions[s.ID()] = s
	m.order = append(m.order, s.ID())
	m.current = s.ID()
	m.mu.Unlock()
}

// Get 返回指定会话。
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// Branch 从 parent 会话复制出直到某个位置的消息，形成新会话（对话分支），并切换
// 到它。fromIndex 为截断位置：0<=i<=len 复制前 i 条；<0 表示复制整个对话。
// parent 不存在时返回 ok=false。分支继承父会话的上下文预算。
func (m *Manager) Branch(parentID string, fromIndex int) (*Session, bool) {
	const maxSessions = 100
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= maxSessions {
		return nil, false
	}
	parent, ok := m.sessions[parentID]
	if !ok {
		return nil, false
	}
	parent.mu.Lock()
	msgs := parent.messages
	title := parent.title
	parent.mu.Unlock()
	if fromIndex < 0 || fromIndex > len(msgs) {
		fromIndex = len(msgs)
	}
	b := NewSession(parent.maxTok)
	copied := make([]ai.Message, fromIndex)
	copy(copied, msgs[:fromIndex])
	b.title = "分支·" + title
	b.SetOnChange(func() { m.SaveSession(b.ID()) })
	b.messages = copied
	m.sessions[b.ID()] = b
	m.order = append(m.order, b.ID())
	m.current = b.ID()
	return b, true
}

// ParentIndex 返回指定会话中“可分支”的位置数（即消息条数），供前端展示分支点。
func (m *Manager) Len(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s.Len()
	}
	return 0
}

// SetCurrent 切换到指定会话。
func (m *Manager) SetCurrent(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; !ok {
		return false
	}
	m.current = id
	return true
}

// Rename 重命名会话。
func (m *Manager) Rename(id, title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.SetTitle(title)
	}
}

// Delete 删除会话；若删除当前会话则切换到最近一个。
func (m *Manager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; !ok {
		return
	}
	delete(m.sessions, id)
	_ = os.Remove(filepath.Join(m.dir, id+".json"))
	for i, o := range m.order {
		if o == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	if m.current == id {
		if len(m.order) > 0 {
			m.current = m.order[len(m.order)-1]
		} else {
			s := NewSession(0)
			m.sessions[s.ID()] = s
			m.order = append(m.order, s.ID())
			m.current = s.ID()
		}
	}
}

// List 返回会话列表。
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		s := m.sessions[id]
		out = append(out, Info{ID: id, Title: s.Title(), Count: s.Len()})
	}
	return out
}
