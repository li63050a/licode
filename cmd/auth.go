package cmd

import (
	"encoding/base64"
	"net/http"
	"os"
)

// AuthUser 默认用户名与环境变量名。
const (
	DefaultUsername = "licode"
	EnvUsername     = "LICODE_USERNAME"
	EnvPassword     = "LICODE_PASSWORD"
)

// ResolveAuth 解析用户名与密码：环境变量优先，未设置时用户名默认 licode。
// 返回 (用户名, 密码, 是否启用认证)。未设置密码时认证关闭。
func ResolveAuth(username, password string) (string, string, bool) {
	if username == "" {
		username = os.Getenv(EnvUsername)
	}
	if username == "" {
		username = DefaultUsername
	}
	if password == "" {
		password = os.Getenv(EnvPassword)
	}
	return username, password, password != ""
}

// basicAuthValue 生成 Authorization: Basic 头值。
func basicAuthValue(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// checkAuth 校验 HTTP Basic 认证；未启用认证时直接放行。
func checkAuth(r *http.Request, user, pass string, enabled bool) bool {
	if !enabled {
		return true
	}
	u, p, ok := r.BasicAuth()
	return ok && u == user && p == pass
}

// unauthorized 返回 401 并提示重试。
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="licode"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("401 未授权：请输入用户名和密码"))
}