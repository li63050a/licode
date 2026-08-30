package cmd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"licode/internal/web"
)

// 登录常量。
const (
	DefaultUsername = "licode"
	EnvUsername     = "LICODE_USERNAME"
	EnvPassword     = "LICODE_PASSWORD"
	SessionCookie   = "licode_auth"
	sessionLifetime = 7 * 24 * time.Hour
	csrfCookie      = "licode_csrf"
	csrfHeader      = "X-CSRF-Token"
)

// authState 是登录认证状态（基于会话 Cookie + HMAC 签名）。
type authState struct {
	user    string
	pass    string
	enabled bool
	secret  []byte
}

// ResolveAuth 解析用户名与密码：环境变量优先，未设置时用户名默认 licode。
// 返回 (用户名, 密码, 是否启用登录)。未设置密码时登录关闭。
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

// newAuthState 构造认证状态。
func newAuthState(user, pass string, enabled bool) *authState {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &authState{user: user, pass: pass, enabled: enabled, secret: secret}
}

// issueToken 签发签名会话令牌：base64url(用户名.过期时间戳.签名)。
func (a *authState) issueToken(user string, exp time.Time) string {
	raw := user + "." + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(raw))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(raw + "." + sig))
}

// verifyToken 校验令牌，返回用户名。
func (a *authState) verifyToken(token string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", false
	}
	parts := strings.Split(string(b), ".")
	if len(parts) != 3 {
		return "", false
	}
	user, expStr, sig := parts[0], parts[1], parts[2]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false
	}
	if time.Now().Unix() > exp {
		return "", false
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(user + "." + expStr))
	if !hmac.Equal(mac.Sum(nil), mustHex(sig)) {
		return "", false
	}
	return user, true
}

func (a *authState) setSession(w http.ResponseWriter, user string) {
	tok := a.issueToken(user, time.Now().Add(sessionLifetime))
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime.Seconds()),
	})
}

func (a *authState) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

// authed 返回当前请求的登录用户名（未登录返回空）。
func (a *authState) authed(r *http.Request) string {
	if !a.enabled {
		return a.user
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	u, ok := a.verifyToken(c.Value)
	if !ok {
		return ""
	}
	return u
}

// require 校验请求是否已登录。未登录时：页面跳转 /login，接口/WS 返回 401。
// 返回 true 表示允许继续处理。
func (a *authState) require(w http.ResponseWriter, r *http.Request) bool {
	if !a.enabled {
		return true
	}
	if a.authed(r) != "" {
		return true
	}
	if r.URL.Path == "/login" {
		return true
	}
	if isAPIRequest(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("401 未登录"))
		return false
	}
	http.Redirect(w, r, "/login", http.StatusFound)
	return false
}

func isAPIRequest(r *http.Request) bool {
	if r.URL.Path == "/ws" {
		return true
	}
	if r.Header.Get("Accept") == "application/json" {
		return true
	}
	return false
}

// handleLogin 处理登录页与登录提交。
func (a *authState) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.enabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if r.Method == http.MethodPost {
		if !validateCSRF(r) {
			http.Redirect(w, r, "/login?error=1", http.StatusFound)
			return
		}
		user := r.FormValue("username")
		pass := r.FormValue("password")
		if user == a.user && pass == a.pass {
			a.setSession(w, user)
			clearCSRF(w)
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	// GET：渲染登录页
	login, err := web.FS.ReadFile("static/login.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	token := generateCSRFToken()
	setCSRFCookie(w, token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(login)
}

func generateCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func setCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionLifetime.Seconds()),
	})
}

func validateCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookie)
	if err != nil {
		return false
	}
	token := r.Header.Get(csrfHeader)
	if token == "" {
		token = r.FormValue("csrf_token")
	}
	return token != "" && token == c.Value
}

func clearCSRF(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   csrfCookie,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

// basicAuthValue 生成 Authorization: Basic 头值（供远程脚本等使用）。
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

func mustHex(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}
