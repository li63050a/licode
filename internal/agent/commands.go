package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// CommandFile 是命令定义文件（frontmatter + 提示词模板），模仿 opencode 的
// .opencode/commands/*.md。运行命令时把提示词作为一条用户消息发出。
type CommandFile struct {
	Name        string
	Description string
	Prompt      string
}

// CommandDirs 返回命令定义目录。
func CommandDirs() []string {
	dirs := []string{".licode/commands", ".opencode/commands"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".licode", "commands"))
	}
	return dirs
}

// LoadCommandFiles 加载所有命令定义文件。
func LoadCommandFiles(dirs ...string) []CommandFile {
	var out []CommandFile
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if cf, ok := parseCommandFile(data); ok {
				out = append(out, cf)
			}
		}
	}
	return out
}

func parseCommandFile(data []byte) (CommandFile, bool) {
	text := string(data)
	cf := CommandFile{}
	if !strings.HasPrefix(text, "---") {
		return cf, false
	}
	end := strings.Index(text[3:], "---")
	if end < 0 {
		return cf, false
	}
	fm := text[3 : 3+end]
	cf.Prompt = strings.TrimSpace(text[3+end+3:])
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "name:"):
			cf.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			cf.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	if cf.Name == "" {
		return cf, false
	}
	if cf.Prompt == "" {
		cf.Prompt = cf.Description
	}
	return cf, true
}
