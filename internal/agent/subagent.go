package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"licode/internal/ai"
)

// SubAgentSpec describes a specialized worker agent with its own system
// prompt and an optional subset of tools.
type SubAgentSpec struct {
	Name          string
	Prompt        string
	Tools         []string // tool names allowed; empty = all default tools
	Client        ai.LLMClient
	MaxIterations int
	MaxTokens     int
	ShellPath     string // Shell 路径（默认 /bin/sh）
}

// buildAgent materializes a full Agent (own session, prompt, tool set).
func (s SubAgentSpec) buildAgent() *Agent {
	a := NewAgent(s.Client, s.Prompt)
	a.Name = s.Name
	if len(s.Tools) > 0 {
		full := NewRegistry()
		RegisterDefaultTools(full, s.ShellPath)
		keep := map[string]bool{}
		for _, n := range s.Tools {
			keep[n] = true
		}
		filtered := NewRegistry()
		for _, n := range full.Names() {
			if keep[n] {
				if t, ok := full.Get(n); ok {
					_ = filtered.Register(t)
				}
			}
		}
		a.Tools = filtered
	}
	if s.MaxIterations > 0 {
		a.MaxIterations = s.MaxIterations
	}
	if s.MaxTokens > 0 {
		a.MaxTokens = s.MaxTokens
	}
	return a
}

// SubAgentResult holds a finished sub-agent run.
type SubAgentResult struct {
	Name   string `json:"name"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Task is a unit of work assigned to a sub-agent. Dependencies refer to other
// task names and gate execution order (DAG scheduling).
type Task struct {
	Name      string   `json:"name"`
	Agent     string   `json:"agent"`
	Prompt    string   `json:"prompt"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// Scheduler executes tasks respecting dependency order, running independent
// tasks in parallel within the same dependency level.
type Scheduler struct {
	Specs []SubAgentSpec
}

func (s *Scheduler) agentByName(name string) (*Agent, bool) {
	for _, spec := range s.Specs {
		if spec.Name == name {
			return spec.buildAgent(), true
		}
	}
	return nil, false
}

// Run executes all tasks. It returns results keyed by task name.
func (s *Scheduler) Run(ctx context.Context, tasks []Task) (map[string]SubAgentResult, error) {
	results := map[string]SubAgentResult{}
	if len(tasks) == 0 {
		return results, nil
	}

	// Validate task names are unique and dependencies reference valid tasks.
	names := map[string]bool{}
	for _, t := range tasks {
		if names[t.Name] {
			return nil, fmt.Errorf("duplicate task name %q", t.Name)
		}
		names[t.Name] = true
	}
	for _, t := range tasks {
		if _, ok := s.agentByName(t.Agent); !ok {
			return nil, fmt.Errorf("task %q references unknown agent %q", t.Name, t.Agent)
		}
		for _, d := range t.DependsOn {
			if !names[d] {
				return nil, fmt.Errorf("task %q depends on unknown task %q", t.Name, d)
			}
		}
	}

	remaining := make([]Task, len(tasks))
	copy(remaining, tasks)

	for len(remaining) > 0 {
		// Pick tasks whose dependencies are all satisfied.
		var level []Task
		var rest []Task
		for _, t := range remaining {
			ready := true
			for _, d := range t.DependsOn {
				if _, done := results[d]; !done {
					ready = false
					break
				}
			}
			if ready {
				level = append(level, t)
			} else {
				rest = append(rest, t)
			}
		}
		if len(level) == 0 {
			// No task became runnable => dependency cycle.
			var pending []string
			for _, t := range rest {
				pending = append(pending, t.Name)
			}
			return results, fmt.Errorf("任务间存在依赖环，无法执行: %s", strings.Join(pending, ", "))
		}

		// Run this level's tasks in parallel.
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, t := range level {
			wg.Add(1)
			go func(t Task) {
				defer wg.Done()
				res := s.runTask(ctx, t)
				mu.Lock()
				results[t.Name] = res
				mu.Unlock()
			}(t)
		}
		wg.Wait()

		remaining = rest
	}
	return results, nil
}

func (s *Scheduler) runTask(ctx context.Context, t Task) SubAgentResult {
	ag, _ := s.agentByName(t.Agent)
	var out strings.Builder
	err := ag.Run(ctx, t.Prompt, func(e Event) {
		if e.Type == EventText {
			out.WriteString(e.Content)
		}
	})
	res := SubAgentResult{Name: t.Name, Output: out.String()}
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

// DefaultSubAgentSpecs returns the built-in explorer / builder / planner
// sub-agent definitions for the given client.
func DefaultSubAgentSpecs(client ai.LLMClient, shellPath string) []SubAgentSpec {
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	return []SubAgentSpec{
		{
			Name: "explorer",
			Prompt: `你是代码探索子代理。使用你的读/搜索工具调查代码库，并给出具体结论：
涉及的文件、关键函数（带 文件:行号 引用）、各部分如何组合。请保持彻底
且实事求是。不要修改文件。请用简体中文汇报。`,
			Tools:  []string{"Read", "ListDirectory", "Glob", "Grep"},
			Client: client,
		},
		{
			Name: "builder",
			Prompt: `你是构建子代理。通过写入或编辑文件实现所要求的改动，然后用
Bash 运行构建/测试命令进行验证。最后总结改动内容、涉及的文件以及
验证结果。请用简体中文汇报。`,
			Tools:  []string{"Read", "Write", "Edit", "ListDirectory", "Grep", "Glob", "Bash"},
			Client: client,
		},
		{
			Name: "planner",
			Prompt: `你是规划子代理。你没有工具。给定一个任务，输出一份简洁、可逐步执行的
实现计划：有序、可操作，并列出可能涉及的文件。不要写代码。请用简体中文
汇报。`,
			Tools:  []string{},
			Client: client,
		},
	}
}

// RegisterSubAgents wires the dispatch tool into the main agent so it can
// break a task into sub-agent tasks and run them (with optional DAG
// dependencies) in parallel.
func (a *Agent) RegisterSubAgents(specs []SubAgentSpec) {
	if len(specs) == 0 {
		return
	}
	a.SubAgents = specs
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	_ = a.Tools.Register(Tool{
		Name:        "Dispatch",
		Description: "Dispatch subtasks to specialized sub-agents in parallel. Each task needs a unique name, an agent name from {" + strings.Join(names, ", ") + "}, a prompt, and optional depends_on referencing other task names for ordering. Returns each task's output or error.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tasks": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":       map[string]any{"type": "string"},
							"agent":      map[string]any{"type": "string"},
							"prompt":     map[string]any{"type": "string"},
							"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
						"required": []string{"name", "agent", "prompt"},
					},
				},
			},
			"required": []string{"tasks"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			raw, ok := args["tasks"]
			if !ok {
				return "", fmt.Errorf("tasks required")
			}
			b, err := json.Marshal(raw)
			if err != nil {
				return "", err
			}
			var tasks []Task
			if err := json.Unmarshal(b, &tasks); err != nil {
				return "", fmt.Errorf("bad tasks: %w", err)
			}
			sched := &Scheduler{Specs: a.SubAgents}
			results, err := sched.Run(ctx, tasks)
			if err != nil {
				return "", err
			}
			out, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	})
}
