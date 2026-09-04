// Package rag 提供轻量级检索增强生成（RAG）支持：对项目源码建立基于
// 关键词/标识符的倒排索引，查询时按与用户问题的词重叠度检索最相关的文件与
// 行，作为上文注入到系统提示词，帮助模型回答与代码库相关的问题。
//
// 刻意不引入向量数据库：通过词频与源码标识符（函数名、类名等）近似语义，
// 部署零依赖、内存开销小。
package rag

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Snippet 表示注入给模型的一条相关代码片段。
type Snippet struct {
	File    string
	LineNum int
	Text    string
}

var (
	// 忽略的目录与二进制/超大文件
	ignoreDirs = map[string]bool{
		".git": true, ".hg": true, ".svn": true, "node_modules": true,
		"vendor": true, "dist": true, "build": true, "target": true,
		".idea": true, ".vscode": true, "__pycache__": true,
	}
	// 收录的源码扩展名
	exts = map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
		".jsx": true, ".rs": true, ".c": true, ".h": true, ".cpp": true,
		".hpp": true, ".java": true, ".kt": true, ".rb": true, ".php": true,
		".swift": true, ".sh": true, ".css": true, ".html": true, ".json": true,
		".yaml": true, ".yml": true, ".toml": true, ".md": true, ".sql": true,
	}
	maxFileBytes int64 = 256 << 10 // 256KB 以上文件跳过
)

// Index 是整个检索索引。
type Index struct {
	mu    sync.RWMutex
	root  string
	terms map[string]map[string]int // term -> file -> freq
	lines map[string][]string       // file -> 每行文本
}

// NewIndex 构建指定根目录的索引。
func NewIndex(root string) *Index {
	idx := &Index{
		root:  root,
		terms: map[string]map[string]int{},
		lines: map[string][]string{},
	}
	idx.Refresh()
	return idx
}

// Refresh 重新遍历源码目录建立索引。
func (idx *Index) Refresh() {
	newTerms := map[string]map[string]int{}
	newLines := map[string][]string{}
	_ = filepath.WalkDir(idx.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != idx.root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		rel, _ := filepath.Rel(idx.root, path)
		newLines[rel] = lines
		tf := map[string]int{}
		for _, ln := range lines {
			for _, tok := range tokenize(ln) {
				tf[tok]++
			}
		}
		for tok := range tf {
			if newTerms[tok] == nil {
				newTerms[tok] = map[string]int{}
			}
			newTerms[tok][rel] = tf[tok]
		}
		return nil
	})
	idx.mu.Lock()
	idx.terms = newTerms
	idx.lines = newLines
	idx.mu.Unlock()
}

// Root returns the indexed root path.
func (idx *Index) Root() string { return idx.root }

// Query 按与 query 的词重叠度检索最相关文件，返回 topN 个匹配行片段。
// 只在查询词中包含源码标识符（长度>=3 的字母数字）时才会有命中。
func (idx *Index) Query(query string, topN int) []Snippet {
	if topN <= 0 {
		topN = 5
	}
	qToks := tokenize(query)
	if len(qToks) == 0 {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	score := map[string]int{}
	for _, tok := range qToks {
		if fm, ok := idx.terms[tok]; ok {
			for f, freq := range fm {
				score[f] += freq
			}
		}
	}
	if len(score) == 0 {
		return nil
	}
	type fc struct {
		file  string
		score int
	}
	files := make([]fc, 0, len(score))
	for f, s := range score {
		files = append(files, fc{f, s})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].score != files[j].score {
			return files[i].score > files[j].score
		}
		return files[i].file < files[j].file
	})
	if len(files) > topN {
		files = files[:topN]
	}
	var out []Snippet
	for _, f := range files {
		lines := idx.lines[f.file]
		if len(lines) == 0 {
			continue
		}
		out = append(out, Snippet{File: f.file, LineNum: 1, Text: header(f.file)})
		// 取包含任一查询词的行（最多 12 行）
		count := 0
		for i, ln := range lines {
			if count >= 12 {
				break
			}
			if containsAny(ln, qToks) {
				out = append(out, Snippet{File: f.file, LineNum: i + 1, Text: ln})
				count++
			}
		}
	}
	return out
}

func header(file string) string {
	return "===== " + file + " ====="
}

var tokenRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// tokenize 提取文本中的标识符（单词/函数名），长度 >=3 且非纯数字。
func tokenize(s string) []string {
	set := map[string]bool{}
	for _, m := range tokenRe.FindAllString(s, -1) {
		if len(m) >= 3 && !isNumeric(m) {
			set[strings.ToLower(m)] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out
}

func isNumeric(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) == -1
}

func containsAny(s string, toks []string) bool {
	ls := strings.ToLower(s)
	for _, t := range toks {
		if strings.Contains(ls, t) {
			return true
		}
	}
	return false
}
