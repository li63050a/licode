# licode Nuxt 前端（nuxtweb）

licode 的**独立新前端**：Nuxt 3 + Tailwind CSS v4 + [fuxsto-design@0.1.0](https://npmmirror.com/package/fuxsto-design)（黑白锌色工业风）+ lucide-vue-next。

支持两种形态：**Node 代理/中继形态**（dev / `.output`，不改后端）与 **静态内嵌形态**（`npm run generate` → `dist/` → `go:embed` 进 Go 二进制，同源直连，替代后端旧界面）。静态内嵌详见 [docs/13-静态生成与内嵌Go后端.md](docs/13-静态生成与内嵌Go后端.md)。

## 文档

- [开发文档索引](docs/INDEX.md)：完整开发文档（后端构建/REST/WS/认证/设置/子系统 + 前端架构/状态/组件 + 静态生成与内嵌 + 陷阱边界 + 验证调试）
- [日常运维文档](docs/ops-guide.md)：拉取后端最新源码 → 对比差异 → 适配前端 → 验证发布的标准流程

## 运行

```bash
cd nuxtweb
npm install
npm run dev          # http://localhost:3000，需 licode 后端跑在 127.0.0.1:8080
```

后端地址用 `LICODE_BACKEND` 环境变量覆盖（默认 `http://127.0.0.1:8080`）：

```bash
# Windows
set LICODE_BACKEND=http://127.0.0.1:9000&& npm run dev
```

生产模式：

```bash
npm run build
set LICODE_BACKEND=http://127.0.0.1:8080&& node .output/server/index.mjs
```

## 静态产物（供内嵌到 licode 后端）

```bash
npm run generate     # 产物输出到 dist/
```

`dist/` 为纯静态 SPA 壳（约 470 KB，gzip 约 140 KB），可直接拷贝进 Go 二进制（`go:embed`）或任意静态托管：

```
dist/
├── index.html       # 主页 SPA 加载器（根路径）
├── 200.html / 404.html  # 静态托管的 SPA fallback（Go 内嵌时可选）
├── login/index.html # 登录页 SPA 加载器
└── _nuxt/           # JS/CSS 产物
```

内嵌到 Go 后要求：

- **同源 serve**：`/_nuxt/*` 由 Go 直接提供，`/api/*`、`/ws` 直连同源后端（无需 CORS/代理/中继）
- **SPA fallback**：对非 `/_nuxt`、`/api`、`/ws` 的 GET 请求返回 `index.html`，前端路由接管（如 `/login`）
- **登录走原生表单**：`login.vue` 以 `<form action="/login" method="post">` 提交，Go 后端的 302 + `Set-Cookie` 由浏览器原生处理；密码错误时 Go 302 到 `/login?error=1`，前端据此显示错误
- **WS 直连**：前端以 `location.host` 连 `/ws`，同源内嵌天然满足

## 与后端的对接方式（不改后端）

| 流量 | 开发（nuxt dev） | 生产（node .output） | 静态内嵌（go:embed） |
| --- | --- | --- | --- |
| REST `/api/*` | nitro `devProxy`（注意它是挂载点语义会剥前缀，所以 target 需带同名前缀） | nitro `routeRules.proxy` | **同源直连**（无代理） |
| WebSocket `/ws` | CLI 把 upgrade 转发给 worker，由 `server/routes/ws.get.ts` 的 crossws 中继连接后端（带 cookie） | 同左（node-server preset 原生支持 crossws） | **同源直连**（无中继） |
| 登录 `POST /login` | `server/routes/signin.post.ts` 用 Node `http.request` 直连后端（**不跟随 302**），读取 302 上的 `licode_auth` Set-Cookie 并原样回传给浏览器（`$fetch` 跟随重定向会丢掉中间响应的 cookie，导致登录后无法建立 WS） | 同左 | 原生 form `POST /login`（浏览器自动跟随 302 保存 cookie，无需 `/signin`） |

中继消息排队：后端连接就绪前到达的消息会缓存，避免竞态丢消息。

## 功能（对齐原有界面）

- 聊天：流式 `delta`、markdown（代码块复制）、工具调用折叠块、`/clear`、停止（`interrupt`）、会话分支
- 工具权限：`ask` 事件 → 拒绝 / 允许 / 始终允许（`ask_reply`）
- 会话：新建 / 切换 / 重命名 / 删除（受后端限制，切换不回放历史消息）
- 设置：全量读取后 patch 回传（`settings_set` 整体替换，未表单化的字段保留原值）；厂商管理 + 一键获取模型（`/api/models`，会临时激活该厂商以使用其 key）
- 文件面板：浏览（面包屑/上级）、新建文件/文件夹、上传、chmod/chown、删除（目录非空二次确认递归）、内置编辑器（Tab 缩进、Ctrl+S 保存、行号）、工作目录切换
- 信息面板：stats（上下文 tokens / 输入输出 / 缓存命中率 / 始终允许工具）、版本、备份导出导入、联系链接
- 审计面板：启动（扫描目录）、1.5s 轮询进度、严重级别筛选汇总、勾选问题、修复预览（着色 diff）→ 确认修复
- 登录：`/api/auth` 检测 + 未登录 401 自动跳 `/login`
- 主题：亮/暗（`html.dark`，fuxsto 双主题），记忆在 `localStorage`

## 已知边界（后端行为所致）

- 后端每次 WS 连接是全新会话管理器：页面刷新后会话列表为空、切换会话不回放历史
- `/api/upload` 返回的 `url` 无路由（已忽略，只用 `path`）
- `stats.provider/model` 后端当前恒为空，回退显示设置里的厂商/模型
- 后端无心跳，前端 2s 自动重连
- 后端未启用密码时不需登录；启用密码后走 `/login` 登录页，登录 cookie 经 `/signin` 中继（Node 形态）或原生 form `POST /login`（静态内嵌形态）回写
- 保存设置依赖 WS（`settings_set`）；WS 未连接时保存按钮禁用并提示，避免静默失败或误清空配置
