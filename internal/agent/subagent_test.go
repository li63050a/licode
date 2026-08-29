package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"licode/internal/ai"
)

// blockingMockClient 记录每次调用发生的顺序，用于验证并行与依赖顺序。
type blockingMockClient struct {
	model string
	mu    sync.Mutex
	order []string
}

func (m *blockingMockClient) Provider() string { return "mock" }
func (m *blockingMockClient) Model() string    { return m.model }
func (m *blockingMockClient) Chat(ctx context.Context, req ai.ChatRequest) (string, error) {
	return "", nil
}
func (m *blockingMockClient) ChatStream(ctx context.Context, req ai.ChatRequest, onEvent func(ai.StreamEvent) error) error {
	// 从最后一条 user 消息中取任务标识
	tag := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			tag = req.Messages[i].Content
			break
		}
	}
	m.mu.Lock()
	m.order = append(m.order, tag)
	m.mu.Unlock()
	// 输出内容包含自己的 tag
	if err := onEvent(ai.StreamEvent{Content: "output(" + tag + ")"}); err != nil {
		return err
	}
	return onEvent(ai.StreamEvent{Done: true})
}

func newSpec(name string, mc *blockingMockClient) SubAgentSpec {
	return SubAgentSpec{Name: name, Prompt: name + " 子代理", Tools: []string{}, Client: mc}
}

func TestSchedulerDAGOrdering(t *testing.T) {
	mc := &blockingMockClient{model: "m"}
	// 任务 t3 依赖 t1、t2；t1、t2 无依赖，应并行先执行。
	tasks := []Task{
		{Name: "t1", Agent: "a", Prompt: "A", DependsOn: nil},
		{Name: "t2", Agent: "b", Prompt: "B", DependsOn: nil},
		{Name: "t3", Agent: "c", Prompt: "C", DependsOn: []string{"t1", "t2"}},
	}
	sched := &Scheduler{Specs: []SubAgentSpec{
		newSpec("a", mc), newSpec("b", mc), newSpec("c", mc),
	}}
	results, err := sched.Run(context.Background(), tasks)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// t1/t2 在 t3 之前（顺序无所谓，但 t3 必须是最后一个开始）
	idx := map[string]int{}
	for i, tag := range mc.order {
		idx[tag] = i
	}
	if idx["C"] < idx["A"] || idx["C"] < idx["B"] {
		t.Fatalf("t3 must start after t1 and t2, order=%v", mc.order)
	}
	// 结果文本：每个任务输出应包含其 prompt 标识
	promptByTask := map[string]string{}
	for _, t := range tasks {
		promptByTask[t.Name] = t.Prompt
	}
	for name, res := range results {
		if res.Error != "" {
			t.Fatalf("task %s error: %s", name, res.Error)
		}
		if !strings.Contains(res.Output, "output("+promptByTask[name]+")") {
			t.Fatalf("task %s output=%q", name, res.Output)
		}
	}
}

func TestSchedulerCycleDetection(t *testing.T) {
	mc := &blockingMockClient{model: "m"}
	tasks := []Task{
		{Name: "t1", Agent: "a", Prompt: "A", DependsOn: []string{"t2"}},
		{Name: "t2", Agent: "a", Prompt: "B", DependsOn: []string{"t1"}},
	}
	sched := &Scheduler{Specs: []SubAgentSpec{newSpec("a", mc)}}
	_, err := sched.Run(context.Background(), tasks)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "环") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSchedulerUnknownAgent(t *testing.T) {
	mc := &blockingMockClient{model: "m"}
	tasks := []Task{{Name: "t1", Agent: "nope", Prompt: "X"}}
	sched := &Scheduler{Specs: []SubAgentSpec{newSpec("a", mc)}}
	if _, err := sched.Run(context.Background(), tasks); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}
