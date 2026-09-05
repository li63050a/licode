package search

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Doc 是一篇已收录（索引）的网页。
type Doc struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	FetchedAt int64  `json:"fetched_at"` // unix 秒
}

// Hit 是检索命中的文档与得分/摘要。
type Hit struct {
	Doc
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
}

const (
	bm25K1    = 1.2
	bm25B     = 0.75
	indexFile = "index.json"
)

// storeData 是磁盘持久化结构。
type storeData struct {
	Docs     []Doc                  `json:"docs"`
	Postings map[string]map[int]int `json:"postings"` // 词元 -> docId -> 词频
}

// Store 是一个进程内存中的倒排索引，变更后落盘到 JSON 文件。
type Store struct {
	mu    sync.RWMutex
	path  string
	data  storeData
	idKey map[string]int // URL -> docId
}

// OpenStore 打开（不存在则创建）索引文件 dir/index.json。
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, indexFile)
	s := &Store{path: p, idKey: map[string]int{}}
	raw, err := os.ReadFile(p)
	if err == nil {
		if uerr := json.Unmarshal(raw, &s.data); uerr != nil {
			return nil, uerr
		}
		for i, d := range s.data.Docs {
			s.idKey[d.URL] = i
		}
	}
	if s.data.Postings == nil {
		s.data.Postings = map[string]map[int]int{}
	}
	return s, nil
}

// Add 收录（或按 URL 覆盖）一篇文档并立即落盘。
func (s *Store) Add(d Doc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, exists := s.idKey[d.URL]
	if !exists {
		id = len(s.data.Docs)
		s.data.Docs = append(s.data.Docs, d)
		s.idKey[d.URL] = id
	} else {
		old := s.data.Docs[id]
		rterms := Tokenize(old.Title + " " + old.Text)
		for _, t := range rterms {
			if pm := s.data.Postings[t]; pm != nil {
				delete(pm, id)
				if len(pm) == 0 {
					delete(s.data.Postings, t)
				}
			}
		}
		s.data.Docs[id] = d
	}
	terms := Tokenize(d.Title + " " + d.Text)
	for _, t := range terms {
		pm := s.data.Postings[t]
		if pm == nil {
			pm = map[int]int{}
			s.data.Postings[t] = pm
		}
		pm[id] = pm[id] + 1
	}
	return s.saveLocked()
}

// Has 判断 URL 是否已收录。
func (s *Store) Has(url string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.idKey[url]
	return ok
}

// Remove 按 URL 删除已收录文档。
func (s *Store) Remove(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.idKey[url]
	if !ok {
		return errors.New("未收录该页面")
	}
	for t, pm := range s.data.Postings {
		if _, has := pm[id]; has {
			delete(pm, id)
			if len(pm) == 0 {
				delete(s.data.Postings, t)
			}
		}
	}
	// 删除后回收：保持 id 连续，重排后续文档的倒排引用
	s.data.Docs = append(s.data.Docs[:id], s.data.Docs[id+1:]...)
	for i := id; i < len(s.data.Docs); i++ {
		if _, ok := s.idKey[s.data.Docs[i].URL]; ok {
			s.idKey[s.data.Docs[i].URL] = i
		}
	}
	for _, pm := range s.data.Postings {
		if len(pm) == 0 {
			continue
		}
		for did := range pm {
			if did > id {
				pm[did-1] = pm[did]
				delete(pm, did)
			}
		}
	}
	delete(s.idKey, url)
	return s.saveLocked()
}

// Search BM25 检索，返回得分最高的 k 个命中（含摘要）。
func (s *Store) Search(q string, k int) []Hit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.data.Docs)
	if n == 0 {
		return nil
	}
	qToks := Tokenize(q)
	if len(qToks) == 0 {
		return nil
	}
	var avgdl float64
	for _, d := range s.data.Docs {
		avgdl += float64(len(Tokenize(d.Title + " " + d.Text)))
	}
	avgdl /= float64(n)
	scores := make([]float64, n)
	docLen := make([]int, n)
	for i, d := range s.data.Docs {
		docLen[i] = len(Tokenize(d.Title + " " + d.Text))
	}
	for _, t := range qToks {
		pm := s.data.Postings[t]
		df := len(pm)
		if df == 0 {
			continue
		}
		idf := log1p(float64(n-df+1) / (float64(df) + 0.5))
		for id, tf := range pm {
			denom := float64(tf) + bm25K1*(1-bm25B+bm25B*float64(docLen[id])/avgdl)
			scores[id] += idf * float64(tf) * (bm25K1 + 1) / denom
		}
	}
	ids := make([]int, 0, n)
	for id := range scores {
		if scores[id] > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return scores[ids[i]] > scores[ids[j]] })
	if len(ids) > k {
		ids = ids[:k]
	}
	out := make([]Hit, 0, len(ids))
	for _, id := range ids {
		d := s.data.Docs[id]
		out = append(out, Hit{Doc: d, Score: scores[id], Snippet: snippetAround(d.Text, qToks, 150)})
	}
	return out
}

// List 全量列出已收录文档（按收录顺序）。
func (s *Store) List() []Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Doc, len(s.data.Docs))
	copy(out, s.data.Docs)
	return out
}

// Filter 列出标题/URL/正文包含关键字 q 的文档。
func (s *Store) Filter(q string) []Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ql := strings.ToLower(q)
	var out []Doc
	for _, d := range s.data.Docs {
		if ql != "" && !strings.Contains(strings.ToLower(d.Title)+" "+strings.ToLower(d.URL)+" "+strings.ToLower(d.Text), ql) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// Stats 返回统计信息。
func (s *Store) Stats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var bytes int64
	for _, d := range s.data.Docs {
		bytes += int64(len(d.Title) + len(d.Text))
	}
	return map[string]any{
		"docs":       len(s.data.Docs),
		"terms":      len(s.data.Postings),
		"text_bytes": bytes,
	}
}

func (s *Store) saveLocked() error {
	if s.data.Postings == nil {
		s.data.Postings = map[string]map[int]int{}
	}
	b, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// log1p 计算 ln(1+x)。
func log1p(x float64) float64 { return math.Log(1 + x) }
