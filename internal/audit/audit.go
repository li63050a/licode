// Package audit 提供代码审计与一键修复功能：
//   - 扫描工作目录（按扩展名过滤），结合静态规则与 LLM 深度分析生成问题报告；
//   - 支持对选中的问题生成修复预览（unified diff），经二次人工确认后才落盘；
//   - 落盘前自动备份原文件（*.bak），支持回滚。
//
// 设计约束：修复预览为纯内存计算，绝不触碰磁盘；只有 confirm=true 时才写入文件。
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"licode/internal/ai"
)

// 严重级别。
const (
	SevCritical = "critical"
	SevHigh     = "high"
	SevMedium   = "medium"
	SevLow      = "low"
)

// 问题类别。
const (
	CatBug      = "bug"
	CatSecurity = "security"
	CatPerf     = "performance"
	CatStyle    = "style"
)

// 任务状态。
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Issue 是审计发现的一个问题。
type Issue struct {
	ID          string `json:"id"`
	File        string `json:"file"` // 相对扫描根目录的路径
	Line        int    `json:"line"` // 1-based；0 = 不适用
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
	// FixPatch 可选：统一 diff 补丁（在生成修复时填充）。
	FixPatch string `json:"fix_patch,omitempty"`
}

// Report 是一次审计的完整结果。
type Report struct {
	TaskID      string    `json:"task_id"`
	Root        string    `json:"root"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"` // 0-100
	Scanned     int       `json:"scanned_files"`
	Issues      []Issue   `json:"issues"`
	CreatedAt   time.Time `json:"created_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	StaticHits  int       `json:"static_hits,omitempty"`
	LLMHits     int       `json:"llm_hits,omitempty"`
	StaticCount int       `json:"static_files,omitempty"` // 静态扫描文件数
	LLMCount    int       `json:"llm_files,omitempty"`    // LLM 分析文件数
}

// Options 是一次审计的启动参数。
type Options struct {
	// Client 为当前配置的 LLM 客户端；nil 时仅做静态扫描。
	Client ai.LLMClient
	// ScanDirs 为相对 root 的扫描子目录；空表示 root 本身。
	ScanDirs []string
	// Exclude 为按相对路径匹配的排除正则；默认排除 vendor/ node_modules/ .git/。
	Exclude []string
	// MaxLLMFiles 限制进入 LLM 深度分析的文件数（0=默认 8）。
	MaxLLMFiles int
	// MaxBytes 为参与 LLM 分析的单个文件大小上限（0=默认 64KB）。
	MaxBytes int64
	// Workers 为 LLM 并发数（0=默认 3）。
	Workers int
}

// Task 是运行中的一次审计任务。
type Task struct {
	id     string
	done   chan struct{}
	mu     sync.RWMutex
	report *Report
	cancel context.CancelFunc
	root   string
}

// ID 返回任务 ID。
func (t *Task) ID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.id
}

// Done 返回在任务结束时关闭的通道。
func (t *Task) Done() <-chan struct{} { return t.done }

// Report 返回最新报告（运行中也会返回部分进度）。
func (t *Task) Report() *Report {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := *t.report
	issues := make([]Issue, len(t.report.Issues))
	copy(issues, t.report.Issues)
	out.Issues = issues
	return &out
}

// Cancel 取消运行中的任务。
func (t *Task) Cancel() {
	if t.cancel != nil {
		t.cancel()
	}
}

// Manager 管理审计任务与修复预览缓存。
type Manager struct {
	mu       sync.Mutex
	tasks    map[string]*Task
	latest   string
	previews sync.Mutex
	pv       map[string]*preview // 修复预览缓存（confirm 时复用，保证所见即所得）
}

type preview struct {
	ts         time.Time
	newContent map[string][]byte // 相对路径 -> 修复后内容
	diffs      map[string]string // 相对路径 -> unified diff
}

// NewManager 创建一个审计管理器。
func NewManager() *Manager {
	return &Manager{
		tasks: map[string]*Task{},
		pv:    map[string]*preview{},
	}
}

// Latest 返回最近一次任务（可能为 nil）。
func (m *Manager) Latest() *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == "" {
		return nil
	}
	return m.tasks[m.latest]
}

// Get 按 ID 返回任务，不存在返回 nil。
func (m *Manager) Get(id string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// RunningVisitor 无需外部调用，保留占位。
// Status 报告指定任务是否仍在运行及其最新报告；id 为空时返回最近任务状态。
// 返回值：(running, report, latestTaskID)。
func (m *Manager) Status(id string) (running bool, report *Report, latestID string) {
	m.mu.Lock()
	latestID = m.latest
	t := m.tasks[id]
	m.mu.Unlock()
	if t == nil {
		if lt := m.Get(latestID); lt != nil {
			r := lt.Report()
			if lt.Report().Status == StatusRunning {
				return true, r, latestID
			}
		}
		return false, nil, latestID
	}
	return t.Report().Status == StatusRunning, t.Report(), latestID
}

// Start 启动一次异步审计并返回任务。
func (m *Manager) Start(ctx context.Context, root string, opts Options) (*Task, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("审计根目录不可用: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("审计根目录不是目录")
	}
	finalizeOpts(&opts)
	id := newID()
	cctx, cancel := context.WithCancel(ctx)
	t := &Task{
		id:     id,
		done:   make(chan struct{}),
		cancel: cancel,
		root:   root,
		report: &Report{
			TaskID:    id,
			Root:      root,
			Status:    StatusRunning,
			CreatedAt: time.Now(),
		},
	}
	m.mu.Lock()
	m.tasks[id] = t
	m.latest = id
	m.mu.Unlock()

	go m.run(cctx, t, root, opts)
	return t, nil
}

// run 是审计主流程：文件发现 → 静态规则 → LLM 深度分析 → 汇总。
func (m *Manager) run(ctx context.Context, t *Task, root string, opts Options) {
	defer close(t.done)
	repFn := func(fn func(*Report)) {
		t.mu.Lock()
		defer t.mu.Unlock()
		fn(t.report)
	}

	// 1. 文件发现
	files := discoverFiles(root, opts.ScanDirs, opts.Exclude)
	repFn(func(r *Report) {
		r.Scanned = len(files)
		r.Progress = 10
	})

	// 2. 静态规则扫描（纯内存、快；对全部文件执行）
	staticIssues := scanFilesStatic(files, root, func(rel string, iss []Issue) {
		repFn(func(r *Report) {
			r.Issues = append(r.Issues, iss...)
		})
	})
	repFn(func(r *Report) {
		r.StaticCount = len(files)
		r.StaticHits = staticIssues
		r.Progress = 45
	})

	// 3. LLM 深度分析（对部分文件，并发受限）
	llmIssues := 0
	if opts.Client != nil && len(files) > 0 {
		llmIssues = m.scanWithLLM(ctx, t, files, root, opts)
	}

	// 4. 汇总去重 + 排序
	repFn(func(r *Report) {
		r.LLMHits = llmIssues
		mergeDedup(r)
		r.Status = StatusDone
		r.FinishedAt = time.Now()
		r.Progress = 100
	})
}

// issueScore 用于严重级别排序（critical>high>medium>low）。
func issueScore(s string) int {
	switch s {
	case SevCritical:
		return 4
	case SevHigh:
		return 3
	case SevMedium:
		return 2
	case SevLow:
		return 1
	}
	return 0
}

// mergeDedup 合并静态与 LLM 的问题，按“文件定位 + 分类 + 描述前缀”去重，再按
// 严重级别降序排序并分配稳定 ID。
func mergeDedup(r *Report) {
	seen := map[string]bool{}
	var out []Issue
	for _, it := range r.Issues {
		key := fmt.Sprintf("%s:%d:%s:%s", it.File, it.Line, it.Category, trunc(it.Description, 48))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if s := issueScore(out[i].Severity) - issueScore(out[j].Severity); s != 0 {
			return s > 0
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	for i := range out {
		out[i].ID = newID()
	}
	r.Issues = out
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// finalizeOpts 填充审计默认值。
func finalizeOpts(o *Options) {
	if len(o.Exclude) == 0 {
		o.Exclude = []string{`(^|/)vendor/`, `(^|/)node_modules/`, `(^|/)\.git/`, `(^|/)dist/`}
	}
	if o.MaxLLMFiles <= 0 {
		o.MaxLLMFiles = 8
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 64 << 10
	}
	if o.Workers <= 0 {
		o.Workers = 3
	}
}

// knownExts 是需要审计的源码扩展名。
var knownExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".java": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".rs": true,
	".cs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true,
	".sh": true, ".sql": true,
}

// discoverFiles 递归发现需要审计的源码文件（相对 root 的路径）。
func discoverFiles(root string, dirs, exclude []string) []string {
	pats := make([]*regexp.Regexp, 0, len(exclude))
	for _, p := range exclude {
		if re, err := regexp.Compile(p); err == nil {
			pats = append(pats, re)
		}
	}
	excluded := func(rel string) bool {
		for _, re := range pats {
			if re.MatchString(rel) {
				return true
			}
		}
		return false
	}

	roots := dirs
	if len(roots) == 0 {
		roots = []string{"."}
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range roots {
		abs := filepath.Join(root, d)
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(abs, func(path string, e os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			if e.IsDir() {
				if excluded(relSlash) {
					return filepath.SkipDir
				}
				return nil
			}
			if !knownExts[strings.ToLower(filepath.Ext(e.Name()))] {
				return nil
			}
			if excluded(relSlash) || seen[relSlash] {
				return nil
			}
			seen[relSlash] = true
			out = append(out, relSlash)
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// scanFilesStatic 对全部文件执行静态规则扫描；对每个文件把命中的问题通过 onIssues
// 回调写入任务报告，返回问题总数。
func scanFilesStatic(files []string, root string, onIssues func(rel string, iss []Issue)) int {
	total := 0
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		iss := staticScan(rel, data)
		if len(iss) > 0 {
			onIssues(rel, iss)
			total += len(iss)
		}
	}
	return total
}

// newID 生成审计任务/问题的随机 ID。
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// PathWithinRoot 校验并解析相对扫描根目录的路径，防止越界。
func PathWithinRoot(root, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("路径越界")
	}
	abs := filepath.Join(root, clean)
	rv, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	rroot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel2, err := filepath.Rel(rroot, rv)
	if err != nil || rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
		return "", errors.New("路径越界")
	}
	return abs, nil
}
