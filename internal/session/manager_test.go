package session

import (
	"testing"

	"licode/internal/ai"
)

func TestManagerBranch(t *testing.T) {
	m := NewManager("", false)
	cur := m.New()
	cur.Add(ai.Message{Role: ai.RoleUser, Content: "hello"})
	cur.Add(ai.Message{Role: ai.RoleAssistant, Content: "hi there"})
	cur.Add(ai.Message{Role: ai.RoleUser, Content: "explain foo"})
	curID := cur.ID()
	from := cur.Len() // branch at the very end

	b, ok := m.Branch(curID, from)
	if !ok {
		t.Fatal("branch should succeed")
	}
	if m.CurrentID() != b.ID() {
		t.Fatalf("expected current switch to branch")
	}
	msgs := b.Messages()
	// 复制了父上下文（from=end → 全部 3 条）
	if len(msgs) != 3 {
		t.Fatalf("expected 3 copied messages, got %d", len(msgs))
	}
	// 分支的上下文应来自父会话
	if b.Title() != "分支·新对话" {
		t.Fatalf("unexpected title %q", b.Title())
	}
}

func TestManagerBranchNegativeMeaningWhole(t *testing.T) {
	m := NewManager("", false)
	cur := m.New()
	cur.Add(ai.Message{Role: ai.RoleUser, Content: "hello"})
	cur.Add(ai.Message{Role: ai.RoleAssistant, Content: "hi"})
	b, ok := m.Branch(cur.ID(), -1)
	if !ok {
		t.Fatal("branch whole should succeed")
	}
	if b.Len() != 2 {
		t.Fatalf("expected whole-conversation copy (2), got %d", b.Len())
	}
}

func TestManagerBranchInvalid(t *testing.T) {
	m := NewManager("", false)
	if _, ok := m.Branch("nope", 0); ok {
		t.Fatal("branch from missing parent should fail")
	}
}
