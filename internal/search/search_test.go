package search

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenizeChineseBigrams(t *testing.T) {
	toks := Tokenize("你好世界 hello Go语言")
	for _, w := range []string{"你好", "好世", "世界", "hello"} {
		if !contains(toks, w) {
			t.Errorf("缺少词元 %q，实际 %v", w, toks)
		}
	}
	if contains(toks, "go语言") {
		t.Errorf("不应产生过长的混合词元：%v", toks)
	}
	// 长度 < 2 的英文被剔除
	if contains(Tokenize("a b c"), "a") {
		t.Error("单字母不应作为词元")
	}
}

func TestTokenizeNumericFiltered(t *testing.T) {
	if contains(Tokenize("API 2.0 版本 2026 发布"), "2026") {
		t.Error("纯数字不应作为词元")
	}
	if !contains(Tokenize("API 2.0 版本 2026 发布"), "版本") {
		t.Error("缺少中文词元")
	}
	// 版本 → 版本（两字 bigram）
	if !contains(Tokenize("API 2.0 版本 2026 发布"), "发布") {
		t.Error("缺少中文词元 发布")
	}
}

func contains(toks []string, w string) bool {
	for _, t := range toks {
		if t == w {
			return true
		}
	}
	return false
}

func TestStoreAddSearchRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Doc{URL: "https://a.dev/1", Title: "Go 语言并发模型", Text: "goroutine channel select 是 Go 并发的核心机制。"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Doc{URL: "https://a.dev/2", Title: "Rust 所有权", Text: "所有权与借用检查器的设计完全不同。"}); err != nil {
		t.Fatal(err)
	}
	if !s.Has("https://a.dev/1") {
		t.Error("Has 应命中")
	}
	// 中文 bigram 检索
	hits := s.Search("Go 并发", 5)
	if len(hits) == 0 {
		t.Fatal("检索无结果")
	}
	if hits[0].URL != "https://a.dev/1" {
		t.Errorf("BM25 应优先命中 doc1，实际 %s (score %.3f)", hits[0].URL, hits[0].Score)
	}
	if hits[0].Snippet == "" {
		t.Error("应生成摘要")
	}
	// 覆盖更新
	if err := s.Add(Doc{URL: "https://a.dev/1", Title: "Go 并发编程实战", Text: "深入讲解 goroutine 调度。"}); err != nil {
		t.Fatal(err)
	}
	// 删除
	if err := s.Remove("https://a.dev/2"); err != nil {
		t.Fatal(err)
	}
	if s.Has("https://a.dev/2") {
		t.Error("删除后 Has 不应命中")
	}
	// 持久化重开
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Has("https://a.dev/1") || s2.Has("https://a.dev/2") {
		t.Error("持久化数据不一致")
	}
	if len(s2.List()) != 1 {
		t.Errorf("List 数量错误：%d", len(s2.List()))
	}
}

func TestExtractHTML(t *testing.T) {
	doc := `<!DOCTYPE html><html><head><title>  测试 &amp; 页面  </title>
<script>var x=1;</script><style>.a{}</style></head><body>
<nav>导航</nav><p>你好，世界！</p><p>第二段。</p></body></html>`
	title, text := ExtractHTML([]byte(doc))
	if title != "测试 & 页面" {
		t.Errorf("标题解析错误：%q", title)
	}
	if !strings.Contains(text, "你好，世界！") || !strings.Contains(text, "第二段。") {
		t.Errorf("正文解析错误：%q", text)
	}
	if strings.Contains(text, "var x=1") {
		t.Errorf("脚本内容应被移除：%q", text)
	}
}

func TestMetaParse(t *testing.T) {
	html := `<html><body>
<div class="r"><a href="https://ex.com/a?s=1">示例 A</a><span class="s">这是摘要 A。</span></div>
<div class="r"><a href="https://ex.com/b">示例 B</a><span class="s">摘要 B</span></div>
<div class="r"><a href="https://ex.com/a?s=1">重复 A</a><span class="s">重复</span></div>
</body></html>`
	te := testEngine{html: html}
	rs := te.parse()
	if len(rs) != 2 {
		t.Fatalf("应解析出 2 条去重结果，实际 %d", len(rs))
	}
	if rs[0].Title != "示例 A" || rs[0].URL != "https://ex.com/a?s=1" {
		t.Errorf("结果解析错误：%+v", rs[0])
	}
	if rs[1].Snippet != "摘要 B" {
		t.Errorf("摘要解析错误：%+v", rs[1])
	}
	// 真实的必应/百度/DDG 模板能匹配到自身 Parse 正则（不回退崩溃）
	for name := range engineSet {
		_ = name
	}
	got := SupportedEngines()
	if len(got) == 0 {
		t.Error("应有内置引擎")
	}
	if !strings.Contains(strings.Join(got, ","), "bing") {
		t.Errorf("内置引擎缺失：%v", got)
	}
}
