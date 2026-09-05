package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// workspaceState 管理"工作目录"：文件浏览/编辑与 Agent 工具都基于该目录。
type workspaceState struct {
	mu   sync.RWMutex
	root string
}

func newWorkspace() *workspaceState {
	root, _ := os.Getwd()
	return &workspaceState{root: root}
}

func (w *workspaceState) Root() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.root
}

// Set 设置工作目录（需存在且为目录）。
func (w *workspaceState) Set(path string) error {
	if path == "" {
		return errors.New("路径不能为空")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("不是目录")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.root = abs
	w.mu.Unlock()
	return nil
}

// resolve 把相对工作目录的路径解析为绝对路径，并防止越界。
func (w *workspaceState) resolve(p string) (string, error) {
	root := w.Root()
	if p == "" {
		return root, nil
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("路径无效")
	}
	resolved = filepath.Clean(resolved)
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", errors.New("路径无效")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("不允许访问工作目录之外")
	}
	return resolved, nil
}

// resolveForWrite 解析目标路径，允许目标本身（或其子路径）尚不存在，
// 用于新建/保存场景。仍会解析已存在的最深前缀并校验不越出工作目录。
func (w *workspaceState) resolveForWrite(p string) (string, error) {
	root := w.Root()
	if p == "" {
		return root, nil
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	// 从目标逐级向上，找到已存在的最深前缀并解析其符号链接
	missing := abs
	var existing string
	for {
		ex, err := filepath.EvalSymlinks(missing)
		if err == nil {
			existing = ex
			break
		}
		parent := filepath.Dir(missing)
		if parent == missing { // 已经到根目录仍未命中
			return "", errors.New("路径无效")
		}
		missing = parent
	}
	relPart, err := filepath.Rel(existing, abs)
	if err != nil {
		return "", errors.New("路径无效")
	}
	combined := filepath.Clean(filepath.Join(existing, relPart))
	rel, err := filepath.Rel(root, combined)
	if err != nil {
		return "", errors.New("路径无效")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("不允许访问工作目录之外")
	}
	return combined, nil
}

type fileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// listDirEntries 列出工作目录下（相对路径 p）的目录内容，供 JSON API 与 HTMX 片段共用。
func listDirEntries(ws *workspaceState, p string) ([]fileEntry, error) {
	abs, err := ws.resolve(p)
	if err != nil {
		return nil, errors.New("路径无效")
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, errors.New("无法读取目录")
	}
	var out []fileEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		rel := p
		if rel != "" {
			rel = strings.TrimSuffix(rel, "/") + "/"
		}
		out = append(out, fileEntry{
			Name: e.Name(), Path: rel + e.Name(), IsDir: e.IsDir(), Size: size,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// handleFiles 列出工作目录下的目录内容。GET /api/files?path=sub
func handleFiles(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	p := r.URL.Query().Get("path")
	out, err := listDirEntries(ws, p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": ws.Root(), "path": p, "entries": out})
}

// handleFile 读取文件内容。GET /api/file?path=...
func handleFile(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	abs, err := ws.resolve(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无法读取文件"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, http.StatusOK, map[string]any{"path": abs, "content": string(data)})
}

// handleSaveFile 写文件。POST /api/file {path, content}
func handleSaveFile(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
		return
	}
	abs, err := ws.resolveForWrite(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无法创建目录"})
		return
	}
	if err := os.WriteFile(abs, []byte(body.Content), 0o644); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无法写入文件"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": abs})
}

// handleMkdir 创建目录。POST /api/mkdir {path}
func handleMkdir(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	abs, err := ws.resolveForWrite(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无法创建目录"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": abs})
}

// handleDeleteFile 删除文件/目录。POST /api/delete {path}
func handleDeleteFile(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	abs, err := ws.resolve(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	if err := os.RemoveAll(abs); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无法删除"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleWorkspace 获取/设置工作目录。GET /api/workspace  POST /api/workspace {path}
func handleWorkspace(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"root": ws.Root()})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
			return
		}
		if err := ws.Set(body.Path); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "root": ws.Root()})
	}
}

// handleUpload 上传文件到工作目录的 uploads/ 子目录。POST /api/upload（multipart）
func handleUpload(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "文件过大或格式错误"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 file 字段"})
		return
	}
	defer file.Close()
	root := ws.Root()
	uploadDir := filepath.Join(root, "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	name := fmt.Sprintf("%d_%s", time.Now().UnixNano(), sanitizeName(header.Filename))
	dst := filepath.Join(uploadDir, name)
	out, err := os.Create(dst)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	rel := "uploads/" + name
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": rel, "url": "/uploads/" + name})
}

func sanitizeName(name string) string {
	name = filepath.Base(name)
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
