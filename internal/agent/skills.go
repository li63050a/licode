package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill 是一个技能：markdown 指令文件，带 frontmatter（name/description）。
type Skill struct {
	Name        string
	Description string
	Body        string
}

// SkillDirs 返回技能目录（项目内与用户级）。
func SkillDirs() []string {
	dirs := []string{".licode/skills", ".opencode/skills"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".licode", "skills"),
			filepath.Join(home, ".config", "licode", "skills"),
			filepath.Join(home, ".config", "opencode", "skills"),
		)
	}
	return dirs
}

// LoadSkills 从指定目录加载所有 .md 技能文件。
func LoadSkills(dirs ...string) []Skill {
	var skills []Skill
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
			if s, ok := ParseSkill(data); ok {
				skills = append(skills, s)
			}
		}
	}
	return skills
}

// ParseSkill 解析带 frontmatter 的技能文件。
func ParseSkill(data []byte) (Skill, bool) {
	text := string(data)
	s := Skill{Name: "", Description: ""}
	if strings.HasPrefix(text, "---") {
		end := strings.Index(text[3:], "---")
		if end < 0 {
			return s, false
		}
		fm := text[3 : 3+end]
		body := text[3+end+3:]
		body = strings.TrimSpace(body)
		for _, line := range strings.Split(fm, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "name:") {
				s.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			}
			if strings.HasPrefix(line, "description:") {
				s.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			}
		}
		if s.Name == "" {
			return s, false
		}
		s.Body = body
		return s, true
	}
	// 无 frontmatter：用文件名当名字
	return s, false
}

// RegisterSkills 把技能注册为工具：调用时把技能指令返回给模型遵循。
func RegisterSkills(r *Registry, skills []Skill) {
	for _, sk := range skills {
		body := sk.Body
		name := sk.Name
		desc := sk.Description
		if desc == "" {
			desc = "技能 " + name
		}
		_ = r.Register(Tool{
			Name:        "skill_" + name,
			Description: "技能「" + name + "」：" + desc + "。调用后按返回的指令执行。",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{"type": "string", "description": "交给该技能处理的任务描述"},
				},
				"required": []string{"task"},
			},
			Run: func(ctx context.Context, args map[string]any) (string, error) {
				return fmt.Sprintf("技能 %s 指令如下，请严格按这些步骤执行：\n%s", name, body), nil
			},
		})
	}
}
