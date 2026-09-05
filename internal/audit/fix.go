package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"licode/internal/ai"
)

// previewKey 是修复预览缓存的键字节。它由任务 ID 与所选项的有序哈希构成，
// 保证 confirm 应用的内容与预览完全一致（所见即所得）。
type previewResult struct {
	Patch      string            `json:"patch"`
	NewContent string            `json:"new_content"`
	BackedUp   bool              `json:"backed_up"`
	BackupPath string            `json:"backup_path,omitempty"`
	Files      []string          `json:"files,omitempty"`
	generated  map[string][]byte // 内部使用：相对路径 -> 新内容
}

// Preview 为选中的问题与文件生成修复预览（不触碰磁盘）。
//
// 对每个受影响的文件发起一次 LLM 调用，让它输出修复后的完整文件内容，
// 再与原文计算 unified diff。返回 map[相对路径]diff。
//
// 生成的修复会缓存；Confirm 会复用缓存保证一致性。
func (m *Manager) Preview(ctx context.Context, client ai.LLMClient, root string, report *Report, issueIDs []string) (map[string]string, error) {
	selected := filterIssues(report.Issues, issueIDs)
	if len(selected) == 0 {
		return nil, errors.New("未选中任何问题")
	}

	files := uniqueFiles(selected)
	diffs := map[string]string{}
	generated := map[string][]byte{}
	for _, rel := range files {
		abs, err := PathWithinRoot(root, rel)
		if err != nil {
			continue
		}
		oldData, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("读取文件失败 %s: %w", rel, err)
		}
		newData, err := llmFixFile(ctx, client, rel, oldData, issuesIn(selected, rel))
		if err != nil {
			return nil, fmt.Errorf("生成修复失败 %s: %w", rel, err)
		}
		oldLines := splitKeepLines(oldData)
		newLines := splitKeepLines(newData)
		if string(oldData) == string(newData) {
			continue // 无需修改
		}
		diffs[rel] = UnifiedDiff(rel, oldLines, newLines)
		generated[rel] = newData
	}
	if len(diffs) == 0 {
		return nil, errors.New("LLM 未产生有效修改")
	}

	key := previewCacheKey(report.TaskID, issueIDs)
	m.previews.Lock()
	m.pv[key] = &preview{
		ts:         time.Now(),
		newContent: generated,
		diffs:      diffs,
	}
	m.previews.Unlock()

	return diffs, nil
}

// Confirm 应用之前预览过的修复到磁盘：先备份 *.bak，再写入新内容。
// previewKey 与 Preview 阶段的缓存键必须一致。
func (m *Manager) Confirm(root string, report *Report, issueIDs []string) (*previewResult, error) {
	key := previewCacheKey(report.TaskID, issueIDs)
	m.previews.Lock()
	pvc := m.pv[key]
	m.previews.Unlock()
	if pvc == nil {
		return nil, errors.New("预览已过期，请重新生成修复预览")
	}
	if time.Now().Sub(pvc.ts) > 10*time.Minute {
		m.previews.Lock()
		delete(m.pv, key)
		m.previews.Unlock()
		return nil, errors.New("预览已过期（超过 10 分钟），请重新生成修复预览")
	}

	res := &previewResult{
		Patch:      joinDiffs(pvc.diffs),
		NewContent: firstContent(pvc.newContent),
		Files:      keysSorted(pvc.newContent),
		generated:  pvc.newContent,
	}

	// 应用写盘（先备份）
	var rels []string
	for rel := range pvc.newContent {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		abs, err := PathWithinRoot(root, rel)
		if err != nil {
			return nil, fmt.Errorf("路径校验失败 %s: %w", rel, err)
		}
		backupPath := abs + ".bak"
		if err := os.WriteFile(backupPath, readOrNil(abs), 0o644); err != nil {
			return nil, fmt.Errorf("备份失败 %s: %w", rel, err)
		}
		res.BackupPath = backupPath
		if err := os.WriteFile(abs, pvc.newContent[rel], 0o644); err != nil {
			return nil, fmt.Errorf("写入失败 %s: %w", rel, err)
		}
	}
	res.BackedUp = true

	// 完成后删除预览缓存，防止二次应用
	m.previews.Lock()
	delete(m.pv, key)
	m.previews.Unlock()

	return res, nil
}

func firstContent(m map[string][]byte) string {
	for _, v := range m {
		return string(v)
	}
	return ""
}

func keysSorted(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinDiffs(d map[string]string) string {
	var out []string
	for _, k := range keysSortedBytes(d) {
		out = append(out, d[k])
	}
	return strings.Join(out, "\n")
}

func keysSortedBytes(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readOrNil(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return []byte{}
	}
	return data
}

// previewCacheKey 生成修复预览缓存键。
func previewCacheKey(taskID string, issueIDs []string) string {
	ids := append([]string{}, issueIDs...)
	sort.Strings(ids)
	h := sha256.New()
	h.Write([]byte(taskID + "|" + strings.Join(ids, ",")))
	return taskID + ":" + hex.EncodeToString(h.Sum(nil))
}

// filterIssues 根据 ID 列表筛选问题。重复 ID 只会被选中一次。
func filterIssues(issues []Issue, ids []string) []Issue {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	var out []Issue
	for _, it := range issues {
		if set[it.ID] {
			out = append(out, it)
			delete(set, it.ID) // 每个问题只出现一次
		}
	}
	return out
}

// uniqueFiles 返回问题涉及的去重文件列表（保持顺序）。
func uniqueFiles(issues []Issue) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range issues {
		if !seen[it.File] {
			seen[it.File] = true
			out = append(out, it.File)
		}
	}
	return out
}

// issuesIn 返回某文件下的问题子集。
func issuesIn(issues []Issue, rel string) []Issue {
	var out []Issue
	for _, it := range issues {
		if it.File == rel {
			out = append(out, it)
		}
	}
	return out
}

// splitKeepLines 把内容拆成保留行尾换行的行片段，便于 diff 精确对齐。
func splitKeepLines(data []byte) []string {
	s := string(data)
	if s == "" {
		return []string{""}
	}
	var out []string
	for len(s) > 0 {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			out = append(out, s[:i+1])
			s = s[i+1:]
		} else {
			out = append(out, s)
			break
		}
	}
	return out
}

// llmFixFile 让 LLM 输出修复后的完整文件内容。
func llmFixFile(ctx context.Context, client ai.LLMClient, rel string, oldData []byte, iss []Issue) ([]byte, error) {
	if len(oldData) > 64<<10 {
		return nil, errors.New("文件过大，跳过 LLM 修复")
	}
	var describe strings.Builder
	fmt.Fprintf(&describe, "文件：%s\n需要修复的问题：\n", rel)
	for i, it := range iss {
		fmt.Fprintf(&describe, "%d. 第 %d 行 [%s/%s]：%s\n   建议：%s\n",
			i+1, it.Line, it.Severity, it.Category, it.Description, it.Suggestion)
	}

	user := fmt.Sprintf(
		"%s\n以下是当前文件内容：\n```\n%s\n```\n请输出修复后的完整文件内容（代码块包裹）：",
		describe.String(), string(oldData))
	req := ai.ChatRequest{
		Model:       client.Model(),
		System:      fixSystem,
		Messages:    []ai.Message{{Role: ai.RoleUser, Content: user}},
		Temperature: 0,
		MaxTokens:   4096,
	}
	out, err := client.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return []byte(extractCodeBlock(out)), nil
}
