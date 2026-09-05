package audit

import (
	"regexp"
	"strings"
)

// rule 是一条静态检测规则：按行匹配源码，命中即产生一个 Issue。
type rule struct {
	name     string
	severity string
	category string
	desc     string
	suggest  string
	re       *regexp.Regexp
	langs    string // 逗号分隔的扩展名白名单，空 = 适用所有语言
}

var ruleList = []*rule{
	{
		name:     "hardcoded_secret",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "检测到疑似硬编码密钥/口令字面量",
		suggest:  "改用环境变量或密钥管理服务引用，并轮换该密钥。",
		re:       regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token|password|passwd)\s*[=:]\s*["'][^"']{12,}["']`),
	},
	{
		name:     "sql_concat",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "SQL 语句字符串拼接，存在注入风险",
		suggest:  "改用参数化查询或占位符，禁止把变量直接拼进 SQL。",
		re:       regexp.MustCompile(`(?i)(select|insert|update|delete)[^;\n]{0,80}(concat|format|\+\s*[a-z_]|\|s*[a-z_])`),
		langs:    ".go,.py,.java,.js,.ts,.rb,.php,.cs",
	},
	{
		name:     "eval_usage",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "使用 eval 执行动态代码，可导致代码注入",
		suggest:  "避免 eval；改用安全的解析或对输入做白名单校验。",
		re:       regexp.MustCompile(`(?i)\b(eval|exec)\s*\([^)]*[a-zA-Z_]`),
		langs:    ".js,.ts,.jsx,.tsx,.py,.rb,.php",
	},
	{
		name:     "shell_injection",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "通过 shell 执行可能包含用户输入的字符串命令",
		suggest:  "优先使用不经过 shell 的 API（参数数组），并严格校验输入。",
		re:       regexp.MustCompile(`(?i)(shell\s*=\s*True|os\.system\(|subprocess\.run\(|child_process\.exec\(|Runtime\.getRuntime\(\).*exec)`),
		langs:    ".py,.js,.ts,.php,.java",
	},
	{
		name:     "unsafe_innerhtml",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "直接把变量写入 innerHTML 或 document.write，存在 XSS 风险",
		suggest:  "使用 textContent 或转义后插入，对用户输入做 HTML 转义。",
		re:       regexp.MustCompile(`(?i)\.innerHTML\s*=|document\.write\s*\(`),
	},
	{
		name:     "weak_hash",
		severity: SevMedium,
		category: CatSecurity,
		desc:     "使用弱哈希（MD5/SHA1）处理敏感数据",
		suggest:  "改用 SHA-256 或 Argon2id 等强算法。",
		re:       regexp.MustCompile(`(?i)\b(md5|sha1)\s*\(`),
		langs:    ".go,.py,.js,.ts,.php",
	},
	{
		name:     "chmod_777",
		severity: SevMedium,
		category: CatSecurity,
		desc:     "设置过于宽泛的文件权限（777）",
		suggest:  "收紧为最小必要权限，如 0755/0644。",
		re:       regexp.MustCompile(`(?i)chmod\s+["']?0?777["']?|0o777|0[0-7]*777\b`),
	},
	{
		name:     "insecure_http",
		severity: SevLow,
		category: CatSecurity,
		desc:     "使用明文 http:// 地址，敏感信息可能被窃听",
		suggest:  "改用 https://；内网或测试环境请注释说明。",
		re:       regexp.MustCompile(`\bhttp://`),
		langs:    ".js,.ts,.php,.go,.py",
	},
	{
		name:     "unsafe_go",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "使用 Go unsafe 包或裸指针操作",
		suggest:  "尽量避免 unsafe；确需使用时添加严格注释与边界检查。",
		re:       regexp.MustCompile(`\bunsafe\.Pointer\s*\(|import\s+"unsafe"`),
		langs:    ".go",
	},
	{
		name:     "unchecked_error",
		severity: SevMedium,
		category: CatBug,
		desc:     "忽略可能返回错误的调用（_ = 丢弃返回值）",
		suggest:  "显式检查错误并处理（记录日志或向上返回），避免静默失败。",
		re:       regexp.MustCompile(`^\s*_\s*=\s*(os\.|io\.|db\.|cmd\.|rows\.|stmt\.|resp\.|body\.|[a-zA-Z_]+\.(Close|Remove|Rename))\s*\(`),
		langs:    ".go",
	},
	{
		name:     "todo_fixme",
		severity: SevLow,
		category: CatStyle,
		desc:     "存在 TODO/FIXME/HACK 待办标记",
		suggest:  "尽快处理并移除标记，避免技术债累积。",
		re:       regexp.MustCompile(`(?i)(//|#|/\*|<!--)\s*(todo|fixme|hack|xxx)\b`),
	},
	{
		name:     "yaml_unsafe_load",
		severity: SevHigh,
		category: CatSecurity,
		desc:     "yaml.load 未指定 SafeLoader，可能执行任意代码",
		suggest:  "使用 yaml.safe_load 或指定 Loader=yaml.SafeLoader。",
		re:       regexp.MustCompile(`(?i)\byaml\s*\.\s*load\s*\(`),
		langs:    ".py",
	},
}

// staticScan 对单个文件运行全部静态规则，返回命中的问题列表。
func staticScan(rel string, data []byte) []Issue {
	ext := ""
	if i := strings.LastIndex(rel, "."); i >= 0 {
		ext = strings.ToLower(rel[i:])
	}
	lines := strings.Split(string(data), "\n")
	var out []Issue
	for _, r := range ruleList {
		if r.langs != "" && !strings.Contains(","+r.langs+",", ","+ext+",") {
			continue
		}
		for i, ln := range lines {
			if r.re.MatchString(ln) {
				out = append(out, Issue{
					File:        rel,
					Severity:    r.severity,
					Category:    r.category,
					Line:        i + 1,
					Description: r.desc,
					Suggestion:  r.suggest,
				})
			}
		}
	}
	return out
}
