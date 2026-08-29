package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

// RegisterDefaultTools installs the built-in coding tools on a registry.
func RegisterDefaultTools(r *Registry) {
	r.Register(Tool{
		Name:        "read_file",
		Description: "Read a text file. Returns the requested lines; use offset/limit for large files.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to the file"},
				"offset": map[string]any{"type": "integer", "description": "1-based starting line"},
				"limit":  map[string]any{"type": "integer", "description": "Max lines to read"},
			},
			"required": []string{"path"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
			if path == "" {
				return "", fmt.Errorf("path required")
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

	r.Register(Tool{
		Name:        "write_file",
		Description: "Write or replace a file with the given content. Creates parent directories.",
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

	r.Register(Tool{
		Name:        "list_dir",
		Description: "List entries in a directory.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directory to list"},
			},
			"required": []string{"path"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			path := strArg(args, "path")
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

	r.Register(Tool{
		Name:        "grep",
		Description: "Search file contents with a regex. Returns matching file:line matches.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Regular expression"},
				"include": map[string]any{"type": "string", "description": "File glob filter, e.g. *.go"},
				"path":    map[string]any{"type": "string", "description": "Root directory (default .)"},
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
			cmd := exec.CommandContext(ctx, "rg", "--line-number", "--no-heading", "-S")
			if include != "" {
				cmd.Args = append(cmd.Args, "-g", include)
			}
			cmd.Args = append(cmd.Args, "-g", "!.git", "-g", "!node_modules", "-g", "!vendor")
			cmd.Args = append(cmd.Args, pattern, root)
			out, err := cmd.CombinedOutput()
			if err != nil {
				// rg exits 1 when no matches; that's not a real error.
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

	r.Register(Tool{
		Name:        "glob",
		Description: "Find files by glob pattern, e.g. **/*.go",
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
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return "", err
			}
			if len(matches) == 0 {
				return "(no matches)", nil
			}
			return strings.Join(matches, "\n"), nil
		},
	})

	r.Register(Tool{
		Name:        "run_shell",
		Description: "Run a shell command and capture its output. Use for builds, tests, and git. Set timeout in seconds.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to run"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 30)"},
				"cwd":     map[string]any{"type": "string", "description": "Working directory"},
			},
			"required": []string{"command"},
		},
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			command := strArg(args, "command")
			if command == "" {
				return "", fmt.Errorf("command required")
			}
			cwd := strArg(args, "cwd")
			timeout := time.Duration(intArg(args, "timeout", 30)) * time.Second
			cmdCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
			if cwd != "" {
				cmd.Dir = cwd
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
}
