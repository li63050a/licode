package ai

import (
	"context"
	"strings"
	"testing"
)

type fakeClient struct {
	calls int
}

func (f *fakeClient) Provider() string { return "openai" }
func (f *fakeClient) Model() string    { return "test-model" }
func (f *fakeClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	f.calls++
	return "answer", nil
}
func (f *fakeClient) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	f.calls++
	if err := onEvent(StreamEvent{Content: "answer"}); err != nil {
		return err
	}
	return onEvent(StreamEvent{Done: true, Usage: &Usage{}})
}
func (f *fakeClient) ListModels(ctx context.Context) ([]string, error) { return nil, nil }

func TestCacheHit(t *testing.T) {
	inner := &fakeClient{}
	cache := NewCache(t.TempDir(), 3600)
	c := CacheDecorator(inner, cache)

	req := ChatRequest{
		Model:    "test-model",
		System:   "sys",
		Messages: []Message{{Role: RoleUser, Content: "What is 2+2?"}},
	}

	out1, err := c.Chat(context.Background(), req)
	if err != nil || out1 != "answer" {
		t.Fatalf("first call: %q err=%v", out1, err)
	}
	// 第二次调用应命中缓存，不再触发底层
	out2, err := c.Chat(context.Background(), req)
	if err != nil || out2 != "answer" {
		t.Fatalf("second call: %q err=%v", out2, err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected 1 underlying call, got %d", inner.calls)
	}

	// 大小写/多余空白差异应命中（归一化），因为模型与系统一致
	req2 := ChatRequest{
		Model:    "test-model",
		System:   "sys",
		Messages: []Message{{Role: RoleUser, Content: "  what  is   2+2?  "}},
	}
	out3, err := c.Chat(context.Background(), req2)
	if err != nil || out3 != "answer" {
		t.Fatalf("normalized hit: %q err=%v", out3, err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected still 1 underlying call, got %d", inner.calls)
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize("  Hello   World?  ")
	if got != "hello world" {
		t.Fatalf("normalize %q", got)
	}
	if !strings.Contains(Normalize("Explain the MitM Attack"), "attack") {
		t.Fatal("case-insensitivity broken")
	}
}
