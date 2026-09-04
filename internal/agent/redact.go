package agent

import (
	"regexp"
)

var (
	apiKeyPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9]{8,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{20,})`)
	tokenPattern  = regexp.MustCompile(`(?i)(token|secret|password|passwd|apikey|api_key|authorization|bearer)\s*[=:]\s*["']?([A-Za-z0-9_\-\.]{12,})["']?`)
)

// RedactSecrets 把工具输出 / 文件内容中的常见密钥格式替换为占位符，避免
// 泄露给模型或写进日志。
func RedactSecrets(s string) string {
	s = apiKeyPattern.ReplaceAllString(s, "[REDACTED]")
	return tokenPattern.ReplaceAllString(s, "${1}=[REDACTED]")
}
