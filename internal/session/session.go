// Package session manages multi-turn conversation history with context-window
// truncation. It stores provider-agnostic messages and produces a trimmed
// message list that fits the configured token budget.
package session

import (
	"sync"
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
	messages []ai.Message
	maxTok   int
}

func NewSession(maxTokens int) *Session {
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	return &Session{maxTok: maxTokens}
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
func (s *Session) MessagesForLLM(system string) []ai.Message {
	s.mu.Lock()
	msgs := make([]ai.Message, len(s.messages))
	copy(msgs, s.messages)
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
	return tail
}