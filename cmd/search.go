package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"licode/internal/search"
)

// searchState 承载联网搜索服务（复用全局本地库单例）。
type searchState struct {
	svc *search.Service
}

func newSearchState() *searchState {
	st, err := search.DefaultStore()
	if err != nil {
		// 本地库打不开时不影响其他功能，仅搜索相关接口报错
		return &searchState{svc: nil}
	}
	return &searchState{svc: search.NewService(st, nil)}
}

// handleSearchEngines GET /api/search/engines → 可用引擎列表。
func (s *searchState) handleSearchEngines(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"engines": search.SupportedEngines()})
}

// handleSearch GET /api/search?q=…&engines=bing,baidu&local=1&max=10
func (s *searchState) handleSearch(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少关键词 q"})
		return
	}
	var engines []string
	for _, e := range strings.Split(r.URL.Query().Get("engines"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			engines = append(engines, e)
		}
	}
	enableMeta := r.URL.Query().Get("local") != "only"
	local := r.URL.Query().Get("local") != "0"
	max, _ := strconv.Atoi(r.URL.Query().Get("max"))
	if max <= 0 || max > 30 {
		max = 10
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if local && !enableMeta {
		// 仅本地库
		hits := s.svc.Store.Search(q, max)
		out := make([]search.Result, 0, len(hits))
		for _, h := range hits {
			out = append(out, search.Result{Engine: "local", Title: h.Title, URL: h.URL, Snippet: h.Snippet, Local: true})
		}
		writeJSON(w, http.StatusOK, map[string]any{"q": q, "results": out})
		return
	}
	out, err := s.svc.Search(ctx, q, engines, enableMeta, local, max)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"q": q, "results": out})
}

// handleSearchFetch POST /api/search/fetch {url} → 抓取单页供预览。
func (s *searchState) handleSearchFetch(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := httpDecodeJSON(w, r, &body); err != nil || body.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 url"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	title, text, err := s.svc.Fetch(ctx, body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	const previewLen = 30000
	if len([]rune(text)) > previewLen {
		text = string([]rune(text)[:previewLen]) + "\n…（预览截断）"
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": body.URL, "title": title, "text": text})
}

// handleSearchSave POST /api/search/save {url} → 抓取并收录到本地库。
func (s *searchState) handleSearchSave(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := httpDecodeJSON(w, r, &body); err != nil || body.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 url"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	d, err := s.svc.Save(ctx, body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": d.URL, "title": d.Title})
}

// handleSearchCatalog GET /api/search/catalog?q=… → 本地库列表（不含全文）。
func (s *searchState) handleSearchCatalog(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	docs := s.svc.Store.Filter(q)
	type brief struct {
		URL       string `json:"url"`
		Title     string `json:"title"`
		FetchedAt int64  `json:"fetched_at"`
		Len       int    `json:"len"`
	}
	out := make([]brief, 0, len(docs))
	for _, d := range docs {
		out = append(out, brief{URL: d.URL, Title: d.Title, FetchedAt: d.FetchedAt, Len: len([]rune(d.Text))})
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": out, "total": len(out)})
}

// handleSearchDelete POST /api/search/delete {url} → 从本地库删除。
func (s *searchState) handleSearchDelete(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := httpDecodeJSON(w, r, &body); err != nil || body.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 url"})
		return
	}
	if err := s.svc.Store.Remove(body.URL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": body.URL})
}

// httpDecodeJSON 解析 JSON 请求体（限 1MB，防滥用）。
func httpDecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	return json.NewDecoder(r.Body).Decode(v)
}

// handleSearchStats GET /api/search/stats → 本地库与引擎统计。
func (s *searchState) handleSearchStats(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "搜索服务不可用"})
		return
	}
	stats := s.svc.Store.Stats()
	stats["engines"] = search.SupportedEngines()
	stats["enabled"] = s.svc.Engines
	writeJSON(w, http.StatusOK, stats)
}
