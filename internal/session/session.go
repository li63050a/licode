// Package session manages multi-turn conversation history with context-window
// truncation. It stores provider-agnostic messages and produces a trimmed
// message list that fits the configured token budget.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"licode/internal/ai"
)

// DefaultMaxTokens is the per-session context budget used when unset.
const DefaultMaxTokens = 128_000

// TokenEstimator approximates token count. ~4 chars per token is a reasonable
// heuristic for mixed English/Chinese text.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := utf8.RuneCountInString(s)
	return n/4 + 1
}

// Session holds the rolling conversation history.
type Session struct {
	mu       sync.Mutex
	id       string
	title    string
	messages []ai.Message
	maxTok   int
	summary  string
	onChange func()
}

// SetOnChange 设置消息变化回调（用于实时落盘）。
func (s *Session) SetOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// NewSession creates a session with a generated id.
func NewSession(maxTokens int) *Session {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	return &Session{id: genID(), title: "新对话", maxTok: maxTokens}
}

// ID returns the session identifier.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Title returns the session title.
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// SetTitle updates the session title.
func (s *Session) SetTitle(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t != "" {
		s.title = t
	}
}

// Restore 导入时恢复会话内容（标题/消息/摘要）。
func (s *Session) Restore(title string, msgs []ai.Message, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if title != "" {
		s.title = title
	}
	s.messages = append(s.messages, msgs...)
	if summary != "" {
		s.summary = summary
	}
}

// Summary returns the compacted context summary, if any.
func (s *Session) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summary
}

// SetSummary stores a compacted context summary.
func (s *Session) SetSummary(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summary = v
}

func genID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// SetID 覆盖会话 ID（加载存档时使用）。
func (s *Session) SetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		s.id = id
	}
}

// fileRecord 是会话的磁盘存档格式。
type fileRecord struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Summary  string       `json:"summary"`
	MaxTok   int          `json:"max_tokens"`
	Messages []ai.Message `json:"messages"`
}

// SaveToFile 将会话写入磁盘（对话记录）。
func (s *Session) SaveToFile(path string) error {
	s.mu.Lock()
	rec := fileRecord{ID: s.id, Title: s.title, Summary: s.summary, MaxTok: s.maxTok}
	rec.Messages = make([]ai.Message, len(s.messages))
	copy(rec.Messages, s.messages)
	s.mu.Unlock()
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadSessionFile 从磁盘加载会话存档。
func LoadSessionFile(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec fileRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	s := NewSession(rec.MaxTok)
	s.id = rec.ID
	s.title = rec.Title
	s.summary = rec.Summary
	s.messages = rec.Messages
	return s, nil
}

func (s *Session) Add(m ai.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
}

func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = s.messages[:0]
}

// Messages returns a copy of all messages (no truncation).
func (s *Session) Messages() []ai.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ai.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Len returns the number of stored messages.
func (s *Session) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// MessagesForLLM returns the message list trimmed to the context budget.
// Truncation strategy: keep the most recent messages that fit, dropping the
// oldest tool-call pairs first so the tail of the conversation always survives.
// 若存在压缩摘要（SetSummary），会把摘要作为一条 user 消息前置。
func (s *Session) MessagesForLLM(system string) []ai.Message {
	s.mu.Lock()
	msgs := make([]ai.Message, len(s.messages))
	copy(msgs, s.messages)
	summary := s.summary
	s.mu.Unlock()

	var budget int
	if system != "" {
		budget = EstimateTokens(system)
	}

	// Walk backwards, collecting messages until the budget is exceeded.
	var tail []ai.Message
	total := budget
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		cost := EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			cost += EstimateTokens(tc.Function.Arguments)
		}
		if total+cost > s.maxTok && len(tail) > 0 {
			break
		}
		tail = append(tail, m)
		total += cost
		if total >= s.maxTok {
			break
		}
	}
	// Reverse to restore chronological order.
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	if summary != "" {
		head := make([]ai.Message, 0, len(tail)+1)
		head = append(head, ai.Message{Role: ai.RoleUser, Content: "【之前对话的压缩摘要】\n" + summary})
		head = append(head, tail...)
		return head
	}
	return tail
}

// TrimHead 移除最旧的 n 条消息（压缩摘要覆盖后释放空间）。
func (s *Session) TrimHead(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n >= len(s.messages) {
		s.messages = nil
		return
	}
	s.messages = s.messages[n:]
}

// Dropped returns the messages that would be trimmed by MessagesForLLM.
// 用于判断是否需要触发上下文压缩。
func (s *Session) Dropped(system string) int {
	s.mu.Lock()
	msgs := make([]ai.Message, len(s.messages))
	copy(msgs, s.messages)
	s.mu.Unlock()

	total := 0
	if system != "" {
		total = EstimateTokens(system)
	}
	dropped := 0
	for _, m := range msgs {
		cost := EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			cost += EstimateTokens(tc.Function.Arguments)
		}
		if total+cost > s.maxTok {
			dropped += cost
		} else {
			total += cost
		}
	}
	return dropped
}
