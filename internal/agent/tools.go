package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"licode/internal/search"
)

var (
	allowedCommandPattern = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)
	cachedWD              string
	cachedHome            string
	pathCacheOnce         sync.Once
	pathCacheErr          error
)

func initPathCache() {
	wd, err := os.Getwd()
	if err == nil {
		cachedWD = filepath.Clean(wd)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		cachedHome = filepath.Clean(home)
	}
}

func validateToolPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path required")
	}
	if strings.Contains(path, "..") {
		return "", fmt.Errorf("path contains ..")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	pathCacheOnce.Do(initPathCache)
	if cachedWD != "" {
		rel, err := filepath.Rel(cachedWD, clean)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
			resolved, err := filepath.EvalSymlinks(clean)
			if err != nil {
				return "", err
			}
			cleanResolved := filepath.Clean(resolved)
			rel2, err := filepath.Rel(cachedWD, cleanResolved)
			if err == nil && !strings.HasPrefix(rel2, "..") && rel2 != ".." {
				return cleanResolved, nil
			}
		}
	}
	if cachedHome != "" {
		rel, err := filepath.Rel(cachedHome, clean)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
			resolved, err := filepath.EvalSymlinks(clean)
			if err != nil {
				return "", err
			}
			cleanResolved := filepath.Clean(resolved)
			rel2, err := filepath.Rel(cachedHome, cleanResolved)
			if err == nil && !strings.HasPrefix(rel2, "..") && rel2 != ".." {
				return cleanResolved, nil
			}
		}
	}
	return "", fmt.Errorf("path outside allowed directories")
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intArg(args map[string]any, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// ShellConfig 配置 Shell 工具执行方式。
type ShellConfig struct {
	Path    string // shell 可执行文件（默认 /bin/sh）
	Sandbox bool   // 使用 Docker 沙箱隔离执行
	Image   string // 沙箱镜像（默认 alpine:latest）
}

func (c *ShellConfig) resolve() {
	c.Path = strings.TrimSpace(c.Path)
	if c.Path == "" {
		c.Path = "/bin/sh"
	}
	if c.Sandbox && strings.TrimSpace(c.Image) == "" {
		c.Image = "alpine:latest"
	}
}

// RegisterDefaultTools installs the built-in coding tools on a registry.
func RegisterDefaultTools(r *Registry, sh ShellConfig) {
	sh.resolve()
	// Read - 读取文件
	r.Register(Tool{
		Name:        "Read",
		Description: "Read a text file. Returns the requested lines with line numbers; use offset/limit for large files.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to the file"},
				"offset": map[string]any{"type": "integer", "description": "1-based starting line"},
				"limit":  map[string]any{"type": "integer", "description": "Max lines to read (default 200)"},
			},
			"required": []string{"path"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
			path, err := validateToolPath(path)
			if err != nil {
				return "", err
			}
			f, err := os.Open(path)
			if err != nil {
				return "", err
			}
			defer f.Close()
			offset := intArg(args, "offset", 1)
			limit := intArg(args, "limit", 200)
			if offset < 1 {
				offset = 1
			}
			var sb strings.Builder
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1024*1024), 1024*1024)
			line := 0
			skipped := 0
			for sc.Scan() {
				line++
				if line < offset {
					skipped++
					continue
				}
				if line >= offset+limit {
					break
				}
				fmt.Fprintf(&sb, "%d: %s\n", line, sc.Text())
			}
			if err := sc.Err(); err != nil {
				return "", err
			}
			if skipped > 0 {
				return fmt.Sprintf("(skipped %d lines before offset %d)\n%s", skipped, offset, sb.String()), nil
			}
			if sb.Len() == 0 {
				return "(empty or offset beyond end of file)", nil
			}
			return sb.String(), nil
		},
	})

	// Write - 写入文件
	r.Register(Tool{
		Name:        "Write",
		Description: "Write or replace a file with the given content. Creates parent directories automatically.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to write"},
				"content": map[string]any{"type": "string", "description": "Full file content"},
			},
			"required": []string{"path", "content"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
			path, err := validateToolPath(path)
			if err != nil {
				return "", err
			}
			content := strArg(args, "content")
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
		},
	})

	// Edit - 编辑文件（查找替换）
	r.Register(Tool{
		Name:        "Edit",
		Description: "Edit a file by finding and replacing specific text. Use for targeted changes without rewriting the whole file.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file"},
				"old":  map[string]any{"type": "string", "description": "Exact text to find (must be unique in file)"},
				"new":  map[string]any{"type": "string", "description": "Replacement text"},
				"all":  map[string]any{"type": "boolean", "description": "Replace all occurrences (default: false, only first)"},
			},
			"required": []string{"path", "old", "new"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
			path, err := validateToolPath(path)
			if err != nil {
				return "", err
			}
			old := strArg(args, "old")
			new := strArg(args, "new")
			if old == "" {
				return "", fmt.Errorf("old text required")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			content := string(data)
			if !strings.Contains(content, old) {
				return "", fmt.Errorf("old text not found in file")
			}
			count := strings.Count(content, old)
			replaceAll := false
			if v, ok := args["all"]; ok {
				if b, ok := v.(bool); ok {
					replaceAll = b
				}
			}
			if !replaceAll && count > 1 {
				return "", fmt.Errorf("old text appears %d times; use all=true or provide more context", count)
			}
			var result string
			if replaceAll {
				result = strings.ReplaceAll(content, old, new)
			} else {
				result = strings.Replace(content, old, new, 1)
			}
			if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
				return "", err
			}
			replaced := 1
			if replaceAll {
				replaced = count
			}
			return fmt.Sprintf("replaced %d occurrence(s) in %s", replaced, path), nil
		},
	})

	// ListDirectory - 列出目录
	r.Register(Tool{
		Name:        "ListDirectory",
		Description: "List entries in a directory with file sizes.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directory to list"},
			},
			"required": []string{"path"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
			path, err := validateToolPath(path)
			if err != nil {
				return "", err
			}
			if path == "" {
				path = "."
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			for _, e := range entries {
				suffix := ""
				if e.IsDir() {
					suffix = "/"
				}
				info, ierr := e.Info()
				size := ""
				if ierr == nil {
					size = fmt.Sprintf(" %d", info.Size())
				}
				fmt.Fprintf(&sb, "%s%s%s\n", e.Name(), suffix, size)
			}
			return sb.String(), nil
		},
	})

	// Grep - 搜索文件内容
	r.Register(Tool{
		Name:        "Grep",
		Description: "Search file contents with a regex pattern. Returns matching file:line results.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Regular expression pattern"},
				"include": map[string]any{"type": "string", "description": "File glob filter, e.g. *.go or *.ts"},
				"path":    map[string]any{"type": "string", "description": "Root directory to search (default: current dir)"},
			},
			"required": []string{"pattern"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			pattern := strArg(args, "pattern")
			include := strArg(args, "include")
			root := strArg(args, "path")
			if root == "" {
				root = "."
			}
			if len(pattern) > 1000 {
				return "", fmt.Errorf("pattern too long")
			}
			if strings.Count(pattern, "*") > 10 {
				return "", fmt.Errorf("pattern too complex")
			}
			cmd := exec.CommandContext(ctx, "rg", "--line-number", "--no-heading", "-S")
			if include != "" {
				cmd.Args = append(cmd.Args, "-g", include)
			}
			cmd.Args = append(cmd.Args, "-g", "!.git", "-g", "!node_modules", "-g", "!vendor")
			cmd.Args = append(cmd.Args, pattern, root)
			out, err := cmd.CombinedOutput()
			if err != nil {
				if len(out) == 0 {
					return "(no matches)", nil
				}
			}
			s := string(out)
			if len(s) > 30000 {
				s = s[:30000] + "\n...(truncated)"
			}
			return s, nil
		},
	})

	// Glob - 按通配符查找文件
	r.Register(Tool{
		Name:        "Glob",
		Description: "Find files by glob pattern, e.g. **/*.go or src/**/*.ts",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern"},
			},
			"required": []string{"pattern"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			pattern := strArg(args, "pattern")
			if pattern == "" {
				return "", fmt.Errorf("pattern required")
			}
			absPattern, err := filepath.Abs(pattern)
			if err == nil {
				wd, _ := os.Getwd()
				cleanWD := filepath.Clean(wd)
				cleanPattern := filepath.Clean(absPattern)
				rel, _ := filepath.Rel(cleanWD, cleanPattern)
				if !strings.HasPrefix(rel, "..") && rel != ".." {
					matches, err := filepath.Glob(pattern)
					if err != nil {
						return "", err
					}
					if len(matches) == 0 {
						return "(no matches)", nil
					}
					return strings.Join(matches, "\n"), nil
				}
			}
			return "", fmt.Errorf("pattern must be within workspace")
		},
	})

	// Shell - 执行 shell 命令
	r.Register(Tool{
		Name:        "Shell",
		Description: "Execute a shell command and return its output. Use for building, testing, git, and system operations.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 30, max 300)"},
				"cwd":     map[string]any{"type": "string", "description": "Working directory for the command"},
			},
			"required": []string{"command"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			command := strArg(args, "command")
			if command == "" {
				return "", fmt.Errorf("command required")
			}
			if !safeCommand(command) {
				return "", fmt.Errorf("command contains disallowed characters or patterns")
			}
			cwd := strArg(args, "cwd")
			if cwd != "" {
				validatedCwd, err := validateToolPath(cwd)
				if err != nil {
					return "", fmt.Errorf("invalid cwd: %w", err)
				}
				cwd = validatedCwd
			}
			timeout := time.Duration(intArg(args, "timeout", 30)) * time.Second
			if timeout > 300*time.Second {
				timeout = 300 * time.Second
			}
			cmdCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			var cmd *exec.Cmd
			if sh.Sandbox {
				// Docker 沙箱隔离执行：只读挂载工作区到 /work，命令在容器内运行。
				img := sh.Image
				dargs := []string{"run", "--rm", "-i", "-w", "/work"}
				if cwd != "" {
					dargs = append(dargs, "-v", cwd+":/work:ro")
				}
				dargs = append(dargs, img, sh.Path, "-c", command)
				cmd = exec.CommandContext(cmdCtx, "docker", dargs...)
			} else {
				cmd = exec.CommandContext(cmdCtx, sh.Path, "-c", command)
				if cwd != "" {
					cmd.Dir = cwd
				}
			}
			out, err := cmd.CombinedOutput()
			s := string(out)
			if len(s) > 30000 {
				s = s[:30000] + "\n...(truncated)"
			}
			if err != nil {
				return fmt.Sprintf("exit error: %v\n%s", err, s), nil
			}
			return s, nil
		},
	})

	// Delete - 删除文件
	r.Register(Tool{
		Name:        "Delete",
		Description: "Delete a file or empty directory.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to delete"},
			},
			"required": []string{"path"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
			path, err := validateToolPath(path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if info.IsDir() {
				entries, _ := os.ReadDir(path)
				if len(entries) > 0 {
					return "", fmt.Errorf("directory not empty (%d entries); only empty directories can be deleted", len(entries))
				}
			}
			if err := os.Remove(path); err != nil {
				return "", err
			}
			return fmt.Sprintf("deleted %s", path), nil
		},
	})

	// Move - 移动/重命名文件
	r.Register(Tool{
		Name:        "Move",
		Description: "Move or rename a file/directory.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{"type": "string", "description": "Source path"},
				"dest":   map[string]any{"type": "string", "description": "Destination path"},
			},
			"required": []string{"source", "dest"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			source := strArg(args, "source")
			source, err := validateToolPath(source)
			if err != nil {
				return "", err
			}
			dest := strArg(args, "dest")
			dest, err = validateToolPath(dest)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", err
			}
			if err := os.Rename(source, dest); err != nil {
				return "", err
			}
			return fmt.Sprintf("moved %s -> %s", source, dest), nil
		},
	})
}

var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`rm\s+-rf\s+/`),
	regexp.MustCompile(`rm\s+-rf\s+~\s*`),
	regexp.MustCompile(`:\(\)\{\s*:\|:\&\}\s*;`),
	regexp.MustCompile(`>\s*/dev/sd`),
	regexp.MustCompile(`mkfs\s+`),
	regexp.MustCompile(`dd\s+.*of=`),
}

func safeCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, pattern := range dangerousPatterns {
		if pattern.MatchString(lower) {
			return false
		}
	}
	return true
}

// RegisterSearchTools 注册联网搜索工具：WebSearch（多引擎合成检索 + 本地知识库）
// 与 WebFetch（抓取单页全文并自动收录到本地库）。
// svc 为 nil 时静默跳过，保证搜索不可用时 Agent 照常工作。
func RegisterSearchTools(r *Registry, svc *search.Service) {
	if svc == nil {
		return
	}
	r.Register(Tool{
		Name: "WebSearch",
		Description: "Search the web using multiple engines (bing / baidu / duckduckgo) and the local knowledge base. " +
			"Returns ranked results (title, url, snippet). Use for real-time, up-to-date or unfamiliar topics.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "搜索关键词，支持中文"},
				"max":   map[string]any{"type": "integer", "description": "最多返回条数（默认 8，上限 15）"},
			},
			"required": []string{"query"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			q := strArg(args, "query")
			if q == "" {
				return "", fmt.Errorf("web search requires a non-empty query")
			}
			k := intArg(args, "max", 8)
			if k < 1 {
				k = 1
			}
			if k > 15 {
				k = 15
			}
			res, err := svc.Search(ctx, q, nil, true, true, k)
			if err != nil {
				return "", err
			}
			if len(res) == 0 {
				return "（各引擎均未返回结果）", nil
			}
			var sb strings.Builder
			for i, r := range res {
				fmt.Fprintf(&sb, "[%d][%s] %s\n%s\n%s\n", i+1, r.Engine, r.Title, r.URL, r.Snippet)
			}
			return strings.TrimSpace(sb.String()), nil
		},
	})
	r.Register(Tool{
		Name: "WebFetch",
		Description: "Fetch a web page and return its readable full text (title + content). " +
			"The page is also added to the local knowledge base. Use to read the actual content behind a search result.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "要抓取的 http(s) 链接"},
			},
			"required": []string{"url"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			u := strArg(args, "url")
			if u == "" {
				return "", fmt.Errorf("web fetch requires a url")
			}
			fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			title, text, err := svc.Fetch(fctx, u)
			if err != nil {
				return "", fmt.Errorf("抓取失败：%w", err)
			}
			_, _ = svc.Save(fctx, u) // 尽力收录，失败不影响返回
			const maxLen = 6000
			if rest := countRunes(text) - maxLen; rest > 0 {
				text = string([]rune(text)[:maxLen]) + fmt.Sprintf("\n…（已截断，原文共多出 %d 字）", rest)
			}
			return "标题：" + title + "\n\n" + text, nil
		},
	})
}

func countRunes(s string) int {
	return len([]rune(s))
}
