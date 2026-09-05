package search

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultDirEnv 覆盖用户数据目录的环境变量（与 internal/settings 保持一致）。
	DefaultDirEnv = "LICODE_HOME"
	// SubDir 索引文件名所在目录的相对名。
	SubDir = "search"
	// FetchSizeCap 单页抓取最大字节数。
	FetchSizeCap = 4 << 20
	// FetchTimeout 单页抓取超时。
	FetchTimeout = 15 * time.Second
)

var baseDirOnce sync.Once
var baseDirCache string

// baseDir 返回用户数据根目录（~/.licode，可用 LICODE_HOME 覆盖）。
func baseDir() string {
	baseDirOnce.Do(func() {
		if v := os.Getenv(DefaultDirEnv); v != "" {
			baseDirCache = v
			return
		}
		if home, err := os.UserHomeDir(); err == nil {
			baseDirCache = filepath.Join(home, ".licode")
			return
		}
		baseDirCache = ".licode"
	})
	return baseDirCache
}

// DefaultStore 返回共享的单例索引（供 Agent 工具与 /api 复用，同一数据文件）。
func DefaultStore() (*Store, error) {
	defaultStoreOnce.Do(func() {
		s, err := OpenStore(filepath.Join(baseDir(), SubDir))
		if err != nil {
			defaultStoreErr = err
			return
		}
		defaultStore = s
	})
	return defaultStore, defaultStoreErr
}

var (
	defaultStoreOnce sync.Once
	defaultStore     *Store
	defaultStoreErr  error
)

// Service 是联网搜索的对外入口：meta 搜索 + 网页抓取 + 本地库收录/检索。
type Service struct {
	Store   *Store
	HTTP    *http.Client
	Engines []string // 启用/顺序
	PerEng  int      // 每引擎最多条数
	Max     int      // 合并后最多条数
}

// NewService 构造服务（engines 为空时使用全部内置引擎）。
func NewService(store *Store, engines []string) *Service {
	if store == nil {
		store, _ = DefaultStore()
	}
	es := engines
	if len(es) == 0 {
		es = SupportedEngines()
	}
	return &Service{
		Store: store,
		HTTP: &http.Client{Timeout: FetchTimeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("重定向过多")
			}
			return nil
		}},
		Engines: es,
		PerEng:  5,
		Max:     10,
	}
}

// Result 是给 UI / LLM 的统一结果项（含本地库命中标记）。
type Result struct {
	Engine  string `json:"engine"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Local   bool   `json:"local"`
}

// Search 执行一次搜索：meta 引擎（enableMeta）与本地库（enableLocal）可分别开关。
func (s *Service) Search(ctx context.Context, q string, engines []string, enableMeta, enableLocal bool, k int) ([]Result, error) {
	if k <= 0 {
		k = s.Max
	}
	if q == "" {
		return nil, errors.New("关键词不能为空")
	}
	var out []Result
	if enableMeta {
		es := engines
		if len(es) == 0 {
			es = s.Engines
		}
		metas := MetaSearch(ctx, s.HTTP, es, q, s.PerEng, k)
		for i, m := range metas {
			out = append(out, Result{Engine: m.Engine, Title: m.Title, URL: m.URL, Snippet: m.Snippet})
			if i >= k {
				break
			}
		}
	}
	if enableLocal {
		for _, h := range s.Store.Search(q, k) {
			if len(out) >= k {
				break
			}
			out = append(out, Result{Engine: "local", Title: h.Title, URL: h.URL, Snippet: h.Snippet, Local: true})
		}
	}
	return out, nil
}

// Fetch 抓取单页并提取标题与正文（供"查看网页"预览与 LLM web_fetch 使用）。
func (s *Service) Fetch(ctx context.Context, rawURL string) (string, string, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", MetaHeaders.Get("User-Agent"))
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", "", errors.New("HTTP " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, FetchSizeCap))
	if err != nil {
		return "", "", err
	}
	title, text := ExtractHTML(body)
	return title, text, nil
}

// Save 抓取并收录（覆盖同名 URL）到本地库，返回文档。
func (s *Service) Save(ctx context.Context, rawURL string) (Doc, error) {
	var d Doc
	title, text, err := s.Fetch(ctx, rawURL)
	if err != nil {
		return d, err
	}
	d = Doc{URL: strings.TrimSpace(rawURL), Title: title, Text: text, FetchedAt: time.Now().Unix()}
	if err := s.Store.Add(d); err != nil {
		return d, err
	}
	return d, nil
}

// ValidateURL 对外暴露 URL 合法性校验（供 API 复用）。
func ValidateURL(raw string) (string, error) { return validateURL(raw) }

func validateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("URL 无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("仅支持 http/https")
	}
	return u.String(), nil
}
