package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"licode/internal/ai"
)

// auditSystem 审计深度分析的系统提示词。
const auditSystem = `你是一名严谨的资深安全与代码质量审计专家。你只报告真实存在的问题，
绝不为了凑数而编造。对每一处问题，你判断：
- severity: critical / high / medium / low
- category: bug / security / performance / style

输出要求：仅输出一个 JSON 数组，不要输出任何其他文字（不要用 markdown 代码块包裹）。
每个元素格式：
{"line": 行号, "severity": "...", "category": "...", "description": "问题描述",
 "suggestion": "具体的修复建议，可含代码片段"}`

// auditMaxLLMLines 交给 LLM 评估的单个文件最大行数（超出截断并标注）。
const auditMaxLLMLines = 400

// fixSystem 修复生成系统提示词。
const fixSystem = `你是一名资深工程师，负责把审计发现的问题修复到指定文件中。
规则：
1. 只修改与给定问题相关的代码行，其余所有内容必须逐字节保持原样（包括注释、换行、缩进）。
2. 修复要最小化、稳妥，符合语言最佳实践。
3. 输出修复后的【完整文件内容】，用代码块包裹（开始标记和结束标记均为三个反引号，语言标记可选），不要输出任何解释文字。`

// scanWithLLM 对最多 opts.MaxLLMFiles 个文件并发调用 LLM 深度分析，把发现的问题
// 写入报告（去重由 mergeDedup 负责），返回 LLM 命中的问题总数。
func (m *Manager) scanWithLLM(ctx context.Context, t *Task, files []string, root string, opts Options) int {
	chosen := pickFilesForLLM(files, root, opts)
	if len(chosen) == 0 {
		return 0
	}
	sem := make(chan struct{}, opts.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for _, rel := range chosen {
		select {
		case <-ctx.Done():
			return total
		default:
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(rel string) {
			defer wg.Done()
			defer func() { <-sem }()
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil || int64(len(data)) > opts.MaxBytes {
				return
			}
			issues := llmAnalyze(ctx, opts.Client, rel, data)
			if len(issues) == 0 {
				return
			}
			mu.Lock()
			total += len(issues)
			t.mu.Lock()
			t.report.Issues = append(t.report.Issues, issues...)
			t.mu.Unlock()
			mu.Unlock()
		}(rel)
	}
	wg.Wait()
	return total
}

// pickFilesForLLM 挑选参与 LLM 分析的文件：小文件优先全收，再按大小补足到上限。
func pickFilesForLLM(files []string, root string, opts Options) []string {
	if len(files) <= opts.MaxLLMFiles {
		return files
	}
	type sz struct {
		rel string
		n   int64
	}
	var all []sz
	var small []string
	for _, rel := range files {
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || st.IsDir() || st.Size() > opts.MaxBytes {
			continue
		}
		if st.Size() < 4096 {
			small = append(small, rel)
		} else {
			all = append(all, sz{rel, st.Size()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
	out := append([]string{}, small...)
	sort.Strings(out)
	if len(out) >= opts.MaxLLMFiles {
		out = out[:opts.MaxLLMFiles]
		return out
	}
	remain := opts.MaxLLMFiles - len(out)
	for i := 0; i < len(all) && i < remain; i++ {
		out = append(out, all[i].rel)
	}
	return out
}

// llmAnalyze 对单个文件调用 LLM 深度分析，解析返回的 JSON 问题列表。
func llmAnalyze(ctx context.Context, client ai.LLMClient, rel string, data []byte) []Issue {
	content := string(data)
	if strings.Count(content, "\n") > auditMaxLLMLines {
		content = truncateLines(content, auditMaxLLMLines)
	}

	user := fmt.Sprintf("请审计以下文件 %q：\n```\n%s\n```", rel, content)
	req := ai.ChatRequest{
		Model:       client.Model(),
		System:      auditSystem,
		Messages:    []ai.Message{{Role: ai.RoleUser, Content: user}},
		Temperature: 0,
		MaxTokens:   3000,
	}
	out, err := client.Chat(ctx, req)
	if err != nil {
		return nil
	}
	return parseIssues(out, rel, strings.Count(content, "\n")+1)
}

// parseIssues 从 LLM 文本中提取 JSON 数组并规范化 Issue。
func parseIssues(text, rel string, maxLine int) []Issue {
	arr := extractJSONArray(text)
	if arr == nil {
		return nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(arr, &raw); err != nil {
		return nil
	}
	var out []Issue
	for _, it := range raw {
		iss := Issue{File: rel}
		switch v := it["line"].(type) {
		case float64:
			iss.Line = int(v)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				iss.Line = n
			}
		}
		iss.Severity = normalizeSeverity(strVal(it["severity"]))
		iss.Category = normalizeCategory(strVal(it["category"]))
		iss.Description = strings.TrimSpace(strVal(it["description"]))
		iss.Suggestion = strings.TrimSpace(strVal(it["suggestion"]))
		if iss.Severity == "" || iss.Description == "" {
			continue
		}
		if iss.Line < 0 || iss.Line > maxLine {
			iss.Line = 0
		}
		out = append(out, iss)
	}
	return out
}

// normalizeSeverity 把 LLM 返回的严重级别映射到枚举。
func normalizeSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "crit"):
		return SevCritical
	case s == "high" || strings.Contains(s, "高危") || strings.Contains(s, "严重"):
		return SevHigh
	case s == "medium" || strings.Contains(s, "中危") || strings.Contains(s, "中"):
		return SevMedium
	case s == "low" || strings.Contains(s, "低"):
		return SevLow
	}
	return ""
}

// normalizeCategory 把 LLM 返回的类别映射到枚举。
func normalizeCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "bug") || strings.Contains(s, "错误") || strings.Contains(s, "error"):
		return CatBug
	case strings.Contains(s, "sec") || strings.Contains(s, "安全") || strings.Contains(s, "注入") || strings.Contains(s, "泄露"):
		return CatSecurity
	case strings.Contains(s, "perf") || strings.Contains(s, "性能"):
		return CatPerf
	case strings.Contains(s, "style") || strings.Contains(s, "风格") || strings.Contains(s, "规范"):
		return CatStyle
	}
	return CatBug
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

var fenceRe = regexp.MustCompile("```[a-zA-Z0-9_+-]*\\n?([\\s\\S]*?)```")

// extractJSONArray 提取文本中的 JSON 数组（容忍 ```json 包裹与非代码噪声）。
func extractJSONArray(text string) []byte {
	if m := fenceRe.FindStringSubmatch(text); len(m) == 2 {
		text = m[1]
	}
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil
	}
	return []byte(text[start : end+1])
}

// extractCodeBlock 提取修复结果中的代码块；无围栏时返回全文。
func extractCodeBlock(text string) string {
	if m := fenceRe.FindStringSubmatch(text); len(m) == 2 {
		return strings.TrimRight(m[1], "\n") + "\n"
	}
	return strings.TrimRight(text, "\n") + "\n"
}

func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n..."
}
