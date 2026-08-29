package agent

import (
	"os"
	"path/filepath"
	"strings"

	"licode/internal/ai"
)

// AgentFile 是 agent 定义文件（frontmatter + 提示词），模仿 opencode 的
// .opencode/agents/*.md：可放在项目 .licode/agents 或用户 ~/.licode/agents。
type AgentFile struct {
	Name        string
	Description string
	Prompt      string
	Tools       []string
}

// AgentDirs 返回 agent 定义目录。
func AgentDirs() []string {
	dirs := []string{".licode/agents", ".opencode/agents"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".licode", "agents"))
	}
	return dirs
}

// LoadAgentFiles 加载所有 agent 定义文件。
func LoadAgentFiles(dirs ...string) []AgentFile {
	var out []AgentFile
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
			if af, ok := parseAgentFile(data); ok {
				out = append(out, af)
			}
		}
	}
	return out
}

func parseAgentFile(data []byte) (AgentFile, bool) {
	text := string(data)
	af := AgentFile{}
	if !strings.HasPrefix(text, "---") {
		return af, false
	}
	end := strings.Index(text[3:], "---")
	if end < 0 {
		return af, false
	}
	fm := text[3 : 3+end]
	af.Prompt = strings.TrimSpace(text[3+end+3:])
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "name:"):
			af.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			af.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		case strings.HasPrefix(line, "tools:"):
			af.Tools = parseToolListField(strings.TrimSpace(strings.TrimPrefix(line, "tools:")))
		}
	}
	if af.Name == "" {
		return af, false
	}
	if af.Prompt == "" {
		af.Prompt = af.Description
	}
	return af, true
}

func parseToolListField(s string) []string {
	s = strings.Trim(s, "[]")
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ToSpec 转成 SubAgentSpec（客户端由调用方统一注入）。
func (a AgentFile) ToSpec(client ai.LLMClient) SubAgentSpec {
	return SubAgentSpec{
		Name:   a.Name,
		Prompt: a.Prompt,
		Tools:  a.Tools,
		Client: client,
	}
}
