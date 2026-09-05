# 前端依赖清单（全部本地化 / 离线可用）

licode 前端**完全不引用任何外部 CDN**。所有第三方资源都下载到 `internal/web/static/`，
经 `//go:embed static` 与二进制打包，页面只从本地 `/static/*` 加载，可离线运行。

## 第三方依赖

| 资源 | 版本 | 来源 | 校验 (sha256) |
| --- | --- | --- | --- |
| `internal/web/static/htmx.min.js` | htmx.org 1.9.12 | <https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js>（备选 jsdelivr） | `449317ade7881e949510db614991e195c3a099c4c791c24dacec55f9f4a2a452` |

重新拉取/校验：`sh scripts/vendor-frontend-deps.sh`

## 页面结构（Go html/template + HTMX）

- `internal/web/templates/index.html` —— 页面外壳模板（标题带 `{{.Version}}`），
  引用 `/static/css/style.css`、`/static/htmx.min.js`、`/static/app.js`。
- `internal/web/templates/frag_*.html` —— HTMX 片段，由服务端渲染：
  - `frag_settings.html`：设置弹窗（WebSocket `settings_set` 保存）。
  - `frag_files.html`：文件树（`/fragment/files`）。
  - `frag_audit.html`：代码审计面板（`/fragment/audit`，运行中自动轮询）。
- `internal/web/static/app.js` —— 业务逻辑（聊天流式输出、会话、设置、工具调用、审计）。
- `internal/web/static/css/style.css` —— 样式。

约束：模板/JS 中禁止出现 `<script src="http://…"` / `<link href="http://…"` 外部资源引用；
页面内仅允许展示性 `<a href="https://…">` 社交链接。

## 构建

```
go build ./...
# 或 ./build.sh           # 打 9 平台 release，产物在 build/（gitignore）
```