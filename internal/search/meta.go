package search

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MetaResult 是某个引擎返回的一条结果。
type MetaResult struct {
	Engine  string `json:"engine"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Engine 定义一种可插拔的搜索结果来源。自定义引擎只需实现该接口并加入 Engines()。
type Engine interface {
	// Name 返回引擎标识（与前端勾选项一致）。
	Name() string
	// URL 构造查询地址。
	URL(q string) string
	// Parse 解析结果页 HTML。解析失败返回 nil（上层宽容处理）。
	Parse(body []byte, q string) []MetaResult
}

// engineSet 内置引擎注册表。
var engineSet = []Engine{
	bingEngine{},
	baiduEngine{},
	ddgEngine{},
}

// SupportedEngines 返回所有可用的引擎名。
func SupportedEngines() []string {
	out := make([]string, 0, len(engineSet))
	for _, e := range engineSet {
		out = append(out, e.Name())
	}
	return out
}

// GetEngine 按名取引擎。
func GetEngine(name string) (Engine, bool) {
	for _, e := range engineSet {
		if e.Name() == name {
			return e, true
		}
	}
	return nil, false
}

// MetaHeaders 伪装成浏览器的请求头（部分引擎对无 UA 请求不返回结果）。
var MetaHeaders = http.Header{
	"User-Agent":      {"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"},
	"Accept":          {"text/html,application/xhtml+xml"},
	"Accept-Language": {"zh-CN,zh;q=0.9,en;q=0.8"},
}

// parseResults 用正则从引擎结果页提取 标题+URL 列表，供各引擎的 Parse 复用。
// blockRE 匹配单条结果块，titleRE 在块内匹配标题与链接，snippetRE 在块内匹配摘要。
func parseResults(body []byte, blockRE, titleRE, snippetRE *regexp.Regexp) []MetaResult {
	s := string(body)
	var out []MetaResult
	blocks := blockRE.FindAllString(s, -1)
	if len(blocks) == 0 {
		return nil
	}
	for _, b := range blocks {
		u, t := "", ""
		if m := titleRE.FindStringSubmatch(b); len(m) >= 3 {
			u = cleanEntity(m[1])
			t = StripHTMLTags(m[2])
		}
		if u == "" || !looksLikeURL(u) {
			continue
		}
		if t == "" {
			t = hostOf(u)
		}
		sn := ""
		if snippetRE != nil {
			if m := snippetRE.FindStringSubmatch(b); len(m) > 1 {
				sn = StripHTMLTags(m[1])
			}
		}
		out = append(out, MetaResult{Title: firstRunes(t, 120), URL: u, Snippet: firstRunes(sn, 200)})
	}
	return dedupeResults(out)
}

func dedupeResults(in []MetaResult) []MetaResult {
	seen := map[string]bool{}
	out := in[:0]
	for _, r := range in {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
	}
	return out
}

func looksLikeURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return u
}

func cleanEntity(s string) string {
	dec := func(m string) string {
		switch m {
		case "&amp;":
			return "&"
		case "&amp":
			return "&"
		}
		return m
	}
	return entityReplace.Replace(dec(s))
}

// MetaSearch 依次调用选中的引擎（并发），合并去重后按引擎交错返回。
func MetaSearch(ctx context.Context, client *http.Client, engines []string, q string, perEngine, max int) []MetaResult {
	if len(engines) == 0 {
		return nil
	}
	type slot struct {
		idx int
		rs  []MetaResult
	}
	ch := make(chan slot, len(engines))
	for i, name := range engines {
		e, ok := GetEngine(name)
		if !ok {
			continue
		}
		go func(i int, e Engine) {
			sctx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			var rs []MetaResult
			req, err := http.NewRequestWithContext(sctx, http.MethodGet, e.URL(q), nil)
			if err == nil {
				for k, vs := range MetaHeaders {
					req.Header[k] = vs
				}
				if resp, err := client.Do(req); err == nil {
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
					resp.Body.Close()
					rs = e.Parse(body, q)
				}
			}
			if len(rs) > perEngine {
				rs = rs[:perEngine]
			}
			for i := range rs {
				rs[i].Engine = e.Name()
			}
			ch <- slot{i, rs}
		}(i, e)
	}
	got := make([][]MetaResult, len(engines))
	for range engines {
		s := <-ch
		got[s.idx] = s.rs
	}
	var out []MetaResult
	for i := 0; i < max; {
		any := false
		for j := range got {
			if len(got[j]) > 0 {
				any = true
				out = append(out, got[j][0])
				got[j] = got[j][1:]
				i++
				if i >= max {
					break
				}
			}
		}
		if !any {
			break
		}
	}
	return out
}

// ---- 必应（cn 国际版） ----

type bingEngine struct{}

func (bingEngine) Name() string { return "bing" }
func (bingEngine) URL(q string) string {
	return "https://cn.bing.com/search?q=" + url.QueryEscape(q) + "&count=10&setlang=zh-cn&mkt=zh-CN"
}
func (bingEngine) Parse(body []byte, _ string) []MetaResult {
	block := regexp.MustCompile(`(?is)<li\s+class=["'][^"']*b_algo[^"']*["'][^>]*>.*?</li>`)
	title := regexp.MustCompile(`(?is)<h2[^>]*>\s*<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>\s*</h2>`)
	snip := regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	return parseResults(body, block, title, snip)
}

// ---- 百度 ----

type baiduEngine struct{}

func (baiduEngine) Name() string { return "baidu" }
func (baiduEngine) URL(q string) string {
	return "https://www.baidu.com/s?wd=" + url.QueryEscape(q) + "&ie=utf-8"
}
func (baiduEngine) Parse(body []byte, _ string) []MetaResult {
	block := regexp.MustCompile(`(?is)<div[^>]+class=["'][^"']*result[_-][^"']*["'][^>]*>.*?<div[^>]+class=["'][^"']*tools[^"']*["'][^>]*>`)
	title := regexp.MustCompile(`(?is)<h3[^>]*>\s*<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>\s*</h3>`)
	snip := regexp.MustCompile(`(?is)<span class=["'][^"']*content-right[^"']*["'][^>]*>(.*?)</span>|<div class=["'][^"']*c-abstract[^"']*["'][^>]*>(.*?)</div>`)
	return parseResults(body, block, title, snip)
}

// ---- DuckDuckGo（HTML 版，无需 JS） ----

type ddgEngine struct{}

func (ddgEngine) Name() string { return "duckduckgo" }
func (ddgEngine) URL(q string) string {
	return "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
}
func (ddgEngine) Parse(body []byte, _ string) []MetaResult {
	block := regexp.MustCompile(`(?is)<div\s+class=["'][^"']*(?:result|results_links)[^"']*["'][^>]*>.*?</div>\s*<div\s+class="result__snippet"[^>]*>.*?</div>`)
	title := regexp.MustCompile(`(?is)<a[^>]+class=["'][^"']*result__a[^"']*["'][^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	snip := regexp.MustCompile(`(?is)<div\s+class=["'][^"']*result__snippet[^"']*["'][^>]*>(.*?)</div>`)
	return parseResults(body, block, title, snip)
}

// testEngine 仅供单元测试注入固定结果页。
type testEngine struct{ html string }

func (testEngine) Name() string                               { return "test" }
func (testEngine) URL(q string) string                        { return "https://example.test/?q=" + url.QueryEscape(q) }
func (t testEngine) Parse(body []byte, q string) []MetaResult { return t.parse() }
func (t testEngine) parse() []MetaResult {
	block := regexp.MustCompile(`(?is)<div class="r">.*?</div>`)
	title := regexp.MustCompile(`(?is)<a href="([^"]+)">(.*?)</a>`)
	snip := regexp.MustCompile(`(?is)<span class="s">(.*?)</span>`)
	return parseResults([]byte(t.html), block, title, snip)
}
