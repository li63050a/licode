// Package web embeds the frontend (templates + static assets) served by cmd/serve.
// 静态资源（CSS/JS/HTMX 等）与 HTML 模板全部打包进二进制，用户运行后从
// 本地 /static/ 与 /fragment/ 加载，完全不依赖外网/CDN。
package web

import (
	"embed"
	"html/template"
	"io"
	"io/fs"
	"strings"
)

// FS 嵌入的静态资源（根为 internal/web，含 static/ 子目录）。
// cmd/auth.go 通过 FS.ReadFile("static/login.html") 读取登录页。
//
//go:embed static
var FS embed.FS

// Templates 嵌入的 Go 模板（页面外壳 + HTMX 片段）。
//
//go:embed templates
var templates embed.FS

var funcMap = template.FuncMap{
	// 审计严重级别样式类与中文标签（frag_audit.html 使用）
	"sevClass": func(s string) string { return "sev " + strings.ToLower(s) },
	"sevLabel": func(s string) string {
		switch strings.ToLower(s) {
		case "critical":
			return "严重"
		case "high":
			return "高"
		case "medium":
			return "中"
		case "low":
			return "低"
		}
		return "未知"
	},
	"join": func(sep string, items []string) string { return strings.Join(items, sep) },
}

var tmpl = template.Must(template.New("").Funcs(funcMap).ParseFS(templates, "templates/index.html", "templates/frag_*.html"))

// StaticFS 返回挂载在 /static/ 下的静态资源文件系统。
func StaticFS() fs.FS {
	sub, err := fs.Sub(FS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// PageData 是页面外壳模板（templates/index.html）的渲染数据。
type PageData struct {
	Version string
}

// RenderIndex 渲染页面外壳（index.html）。
func RenderIndex(w io.Writer, data PageData) error {
	return tmpl.ExecuteTemplate(w, "index.html", data)
}

// RenderFragment 渲染一个 HTMX 片段模板（frag_*.html），data 为模板数据。
func RenderFragment(w io.Writer, name string, data any) error {
	return tmpl.ExecuteTemplate(w, name, data)
}
