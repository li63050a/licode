package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"licode/internal/ai"
)

// fakeClient 实现 ai.LLMClient，按系统提示词区分审计/修复，返回预设文本。
type fakeClient struct {
	auditResp string
	fixResp   string
	auditErr  error
}

func (f *fakeClient) Provider() string { return "fake" }
func (f *fakeClient) Model() string    { return "fake-model" }
func (f *fakeClient) Chat(_ context.Context, req ai.ChatRequest) (string, error) {
	if req.System == fixSystem {
		return f.fixResp, nil
	}
	if f.auditErr != nil {
		return "", f.auditErr
	}
	return f.auditResp, nil
}
func (f *fakeClient) ChatStream(context.Context, ai.ChatRequest, func(ai.StreamEvent) error) error {
	return nil
}

func TestStaticScan(t *testing.T) {
	src := `package main
api_key = "12345678901234"
// TODO: 清理过期分支
result := yaml.load(input)
password := "abcdefghijklmn"`

	issues := staticScan("sample.py", []byte(src))
	var hasSecret, hasTODO, hasYAML bool
	for _, it := range issues {
		switch {
		case it.Category == CatSecurity && it.Description == "检测到疑似硬编码密钥/口令字面量":
			hasSecret = true
		case it.Description == "存在 TODO/FIXME/HACK 待办标记":
			hasTODO = true
		case it.Description == "yaml.load 未指定 SafeLoader，可能执行任意代码":
			hasYAML = true
		}
	}
	if !hasSecret {
		t.Error("静态扫描未命中硬编码密钥")
	}
	if !hasTODO {
		t.Error("静态扫描未命中 TODO")
	}
	if !hasYAML {
		t.Error("静态扫描未命中 yaml.load")
	}
	for _, it := range issues {
		if it.Severity == "" || it.Category == "" {
			t.Errorf("问题缺少严重级别或类别: %+v", it)
		}
		t.Logf("static rule → %s/L%d in %s", it.Severity, it.Line, it.File)
	}
}

func TestStaticScanLangFilter(t *testing.T) {
	// yaml 规则只在 .py 生效；.go 不该命中
	goSrc := `func f() { yaml.load(x) }`
	if n := len(staticScan("x.go", []byte(goSrc))); n != 0 {
		t.Errorf("yaml.load 规则不应命中 .go 文件，实际命中 %d 条", n)
	}
	pySrc := `yaml.load(x)`
	found := false
	for _, it := range staticScan("x.py", []byte(pySrc)) {
		if it.Description == "yaml.load 未指定 SafeLoader，可能执行任意代码" {
			found = true
		}
	}
	if !found {
		t.Error("yaml.load 规则应命中 .py 文件")
	}
}

func TestDiscoverFiles(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.go", "b.js", "c.txt", "sub/d.go", "vendor/v.go", "node_modules/n.js", ".git/hook.sh"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := discoverFiles(dir, nil, []string{`(^|/)vendor/`, `(^|/)node_modules/`, `(^|/)\.git/`})
	expect := []string{"a.go", "b.js", "sub/d.go"}
	if len(got) != len(expect) {
		t.Fatalf("文件发现结果不符: got %v want %v", got, expect)
	}
	// c.txt 不应出现
	joined := strings.Join(got, ",")
	if strings.Contains(joined, "c.txt") || strings.Contains(joined, "vendor") {
		t.Errorf("不应包含 txt/vendor 文件: %v", got)
	}
}

func TestUnifiedDiff(t *testing.T) {
	oldL := []string{"line1\n", "line2\n", "apikey = \"1234\"\n", "line4\n"}
	newL := []string{"line1\n", "apikey = getenv(\"KEY\")\n", "line4\n"}
	d := UnifiedDiff("app.js", oldL, newL)
	if !strings.Contains(d, "@@") {
		t.Errorf("diff 缺少 hunk 头: %s", d)
	}
	if !strings.Contains(d, "-apikey") || !strings.Contains(d, "+apikey") {
		t.Errorf("diff 缺少增删行: %s", d)
	}
	if !strings.HasPrefix(d, "--- a/app.js\n+++ b/app.js\n") {
		t.Errorf("diff 头格式错误: %s", d)
	}
	t.Logf("\n%s", d)
}

func TestParseIssues(t *testing.T) {
	raw := "```json\n[{\"line\": 3, \"severity\": \"high\", \"category\": \"security\", \"description\": \"硬编码密钥\", \"suggestion\": \"改用环境变量\"}, {\"line\": 99, \"severity\": \"medium\", \"category\": \"performance\", \"description\": \"循环低效\", \"suggestion\": \"提前退出\"}]\n```"
	issues := parseIssues(raw, "a.py", 100)
	if len(issues) != 2 {
		t.Fatalf("解析到 %d 条问题，期望 2", len(issues))
	}
	if issues[0].Severity != SevHigh || issues[0].Category != CatSecurity || issues[0].Line != 3 {
		t.Errorf("问题字段不规范: %+v", issues[0])
	}
	if issues[0].File != "a.py" {
		t.Errorf("File 字段未填充: %+v", issues[0])
	}
	// 行号越界 → 归零
	out := parseIssues("[{\"line\": 500, \"severity\": \"low\", \"category\": \"style\", \"description\": \"x\", \"suggestion\": \"y\"}]", "x.go", 10)
	if out[0].Line != 0 {
		t.Errorf("越界行号应归零，got %d", out[0].Line)
	}
}

func TestExtractCodeBlock(t *testing.T) {
	got := extractCodeBlock("```go\npackage main\nfunc main() {}\n```")
	want := "package main\nfunc main() {}\n"
	if got != want {
		t.Errorf("代码块提取不符:\ngot  %q\nwant %q", got, want)
	}
	// 无围栏时原样返回 + 换行
	if got := extractCodeBlock("plain text"); got != "plain text\n" {
		t.Errorf("无围栏提取不符: %q", got)
	}
}

func TestManagerStartStaticOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("api_key = \"123456789012345\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	task, err := mgr.Start(context.Background(), dir, Options{Client: nil})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("任务超时")
	}
	rep := task.Report()
	if rep.Status != StatusDone {
		t.Fatalf("状态不符: %s", rep.Status)
	}
	if rep.Scanned != 1 {
		t.Errorf("扫描文件数不符: %d", rep.Scanned)
	}
	if len(rep.Issues) == 0 {
		t.Fatal("静态扫描应发现硬编码密钥")
	}
	if rep.Issues[0].ID == "" {
		t.Error("问题缺少稳定 ID")
	}
}

func TestManagerRunWithLLM(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{auditResp: `[{"line":1,"severity":"high","category":"security","description":"未处理 panic","suggestion":"建议修复"}]`}
	mgr := NewManager()
	task, err := mgr.Start(context.Background(), dir, Options{Client: fc, MaxLLMFiles: 2, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("任务超时")
	}
	rep := task.Report()
	if rep.Status != StatusDone {
		t.Fatalf("状态不符: %s", rep.Status)
	}
	found := false
	for _, it := range rep.Issues {
		if it.Severity == SevHigh && it.Category == CatSecurity {
			found = true
		}
	}
	if !found {
		t.Errorf("LLM 审计问题未写入报告: %+v", rep.Issues)
	}
}

func TestPreviewAndConfirm(t *testing.T) {
	dir := t.TempDir()
	orig := "const key = \"1234567890123\";\nconsole.log(key);\n"
	app := filepath.Join(dir, "app.js")
	if err := os.WriteFile(app, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	fc := &fakeClient{fixResp: "```js\nconst key = process.env.KEY || \"\";\nconsole.log(key);\n```"}
	mgr := NewManager()
	report := &Report{
		TaskID: "task-fix-1",
		Issues: []Issue{{ID: "i1", File: "app.js", Line: 1, Severity: SevHigh, Category: CatSecurity, Description: "硬编码密钥", Suggestion: "改用环境变量"}},
	}

	// 1. 预览（不落盘）
	diffs, err := mgr.Preview(context.Background(), fc, dir, report, []string{"i1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := diffs["app.js"]; !ok {
		t.Fatalf("缺少 app.js 的 diff: %v", diffs)
	}
	// 文件必须保持原样
	data, _ := os.ReadFile(app)
	if string(data) != orig {
		t.Fatal("预览阶段不应修改磁盘文件")
	}

	// 2. 确认（落盘 + 备份）
	res, err := mgr.Confirm(dir, report, []string{"i1"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BackedUp {
		t.Error("确认后应产生备份")
	}
	if _, err := os.Stat(app + ".bak"); err != nil {
		t.Errorf("备份文件缺失: %v", err)
	}
	after, _ := os.ReadFile(app)
	if !strings.Contains(string(after), "process.env.KEY") {
		t.Errorf("修复内容未写入:\n%s", string(after))
	}
	backup, _ := os.ReadFile(app + ".bak")
	if string(backup) != orig {
		t.Errorf("备份内容应与原文件一致: %q", string(backup))
	}

	// 3. 预览缓存已被清除，二次确认应失败
	if _, err := mgr.Confirm(dir, report, []string{"i1"}); err == nil {
		t.Error("二次确认应失败（预览已消费）")
	}
}

func TestPreviewNoChangeFails(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "a.go")
	if err := os.WriteFile(app, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{fixResp: "```go\npackage main\n```"} // 与原文一致
	mgr := NewManager()
	report := &Report{TaskID: "task-fix-2", Issues: []Issue{{ID: "i2", File: "a.go", Severity: SevHigh, Description: "x", Suggestion: "y"}}}
	if _, err := mgr.Preview(context.Background(), fc, dir, report, []string{"i2"}); err == nil {
		t.Error("LLM 未产生有效修改时应报错")
	}
}

func TestCancellation(t *testing.T) {
	dir := t.TempDir()
	fc := &fakeClient{}
	mgr := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := mgr.Start(ctx, dir, Options{Client: fc, Workers: 1, MaxLLMFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 取消后任务应快速结束且不 panic
	time.Sleep(50 * time.Millisecond)
	if _, _, id := mgr.Status(""); id == "" {
		t.Error("启动的任务应被登记")
	}
}
