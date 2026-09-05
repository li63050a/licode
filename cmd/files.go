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
	"strconv"
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

// fsPath 把用户输入解析为绝对路径，用于文件管理器（Web 界面）：
// - 以 / 开头（或平台绝对路径）→ 按原样使用（可浏览整个文件系统，含根目录）
// - 其他 → 相对工作目录
// 不做"限界"检查：文件管理器是用户显式操作，允许浏览/修改任意路径。
func (w *workspaceState) fsPath(p string) (string, error) {
	if p == "" {
		return w.Root(), nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Clean(filepath.Join(w.Root(), p)), nil
}

// resolveWriteAbs 同 fsPath，用于新建/保存场景（目标本身可以尚不存在）。
func (w *workspaceState) resolveWriteAbs(p string) (string, error) {
	return w.fsPath(p)
}

type fileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// browseDir 列出绝对目录（abs）下的内容，子条目 Path 为绝对路径。
func browseDir(abs string) ([]fileEntry, error) {
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
		out = append(out, fileEntry{
			Name: e.Name(), Path: filepath.Join(abs, e.Name()), IsDir: e.IsDir(), Size: size,
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

// handleFiles 列出目录内容。GET /api/files?path=…（可为绝对路径）
func handleFiles(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	p := r.URL.Query().Get("path")
	abs, err := ws.fsPath(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	out, err := browseDir(abs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": ws.Root(), "path": abs, "entries": out})
}

// handleFile 读取文件内容。GET /api/file?path=…（可为绝对路径）
func handleFile(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	abs, err := ws.fsPath(r.URL.Query().Get("path"))
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

// handleSaveFile 写文件（目标不存在则新建）。POST /api/file {path, content}
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
	abs, err := ws.resolveWriteAbs(body.Path)
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

// handleMkdir 创建目录（可为绝对路径）。POST /api/mkdir {path}
func handleMkdir(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	abs, err := ws.resolveWriteAbs(body.Path)
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

// handleDeleteFile 删除文件/目录（可为绝对路径）。POST /api/delete {path, recursive}
// 非空目录必须显式 recursive=true 才会删除，防止误删。
func handleDeleteFile(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	abs, err := ws.fsPath(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径不存在"})
		return
	}
	if info.IsDir() && !body.Recursive {
		if ents, _ := os.ReadDir(abs); len(ents) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "目录非空，请确认递归删除"})
			return
		}
	}
	if err := os.RemoveAll(abs); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无法删除"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChmod 修改文件/目录权限。POST /api/chmod {path, mode}
func handleChmod(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
		return
	}
	abs, err := ws.fsPath(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	mode, err := parseMode(body.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := os.Chmod(abs, mode); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "修改权限失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": abs, "mode": fmt.Sprintf("%o", uint32(mode))})
}

// parseMode 解析八进制权限串（644 / 0755 / 0o644）。
func parseMode(s string) (os.FileMode, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0o")
	s = strings.TrimPrefix(s, "0O")
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 0o7777 {
		return 0, errors.New("权限格式无效（八进制，如 644 / 755 / 0o644）")
	}
	return os.FileMode(n), nil
}

// handleChown 修改文件/目录所有者。POST /api/chown {path, owner}
// owner 形如 "uid:gid"，任一侧可为 -1 表示保持不变（如 "1000:-1"）。
func handleChown(w http.ResponseWriter, r *http.Request, ws *workspaceState) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Path  string `json:"path"`
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
		return
	}
	abs, err := ws.fsPath(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "路径无效"})
		return
	}
	uid, gid, err := parseOwner(body.Owner)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := os.Chown(abs, uid, gid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "修改所有者失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": abs, "uid": uid, "gid": gid})
}

// parseOwner 解析 "uid:gid"（-1 表示不变）。空串返回 -1:-1。
func parseOwner(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1, -1, nil
	}
	parts := strings.SplitN(s, ":", 2)
	atoi := func(v string) (int, error) {
		v = strings.TrimSpace(v)
		if v == "" || v == "-" {
			return -1, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, errors.New("所有者格式无效（应为 uid:gid，-1 表示不变）")
		}
		return n, nil
	}
	uid, err := atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	gid := -1
	if len(parts) == 2 {
		if gid, err = atoi(parts[1]); err != nil {
			return 0, 0, err
		}
	}
	return uid, gid, nil
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
