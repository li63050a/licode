# licode 日常运维文档

> 日常运维流程 = **从 GitHub 拉取后端最新源码 → 与上一次拉取对比差异 → 根据差异添加/修改前端（nuxtweb）→ 构建与验证 → 发布**。
> 本文档把每一步的精确命令、关注点、验证方法与常见故障处理都写清楚，照做即可。
> 协议/字段/实现的深水区细节见 [开发文档索引](INDEX.md)（01~12 分册）。

---

## 1. 运维流程总览

```
┌──────────────────────────────────────────────────────────────┐
│ 1. 拉取最新后端源码（git fetch + 对比）                       │
│ 2. 对比与上一次的差异（git diff / 哈希）                       │
│ 3. 识别「会波及前端」的变更（API / WS / 设置 / 新功能）        │
│ 4. 适配前端（nuxtweb）：新增/修改接口调用与 UI                 │
│ 5. 构建 + 类型检查 + 前后端联调验证                            │
│ 6. 提交 / 发布                                                │
└──────────────────────────────────────────────────────────────┘
```

---

## 2. 前置环境

| 工具 | 版本要求 | 说明 |
| --- | --- | --- |
| Git | 任意 | 拉取后端源码 |
| Go | ≥1.22（插件 wasmexport 需 1.24+） | 编译后端；国内需 `GOPROXY` |
| Node.js | `^22.18.0 || >=24.12.0` | 前端 fuxsto-design 的 engine 要求 |
| npm | 随 Node | 前端依赖 |
| （可选）Edge/Chrome | 任意 | 无头浏览器验证（puppeteer-core） |

后端代码目录（示例）：
- 最新源码副本：`D:\licode`（git 仓库，remote = `https://github.com/li63050a/licode.git`）
- 前端项目：`D:\code\licode\nuxtweb`

---

## 3. 拉取最新后端源码

两个仓库是**镜像**，同一提交，拉任一即可：
- `https://github.com/li63050a/licode`
- `https://github.com/li63050a6/licode`

```bash
cd D:\licode

# 确认 remote（应指向上述任一仓库）
git remote -v

# 拉取远程最新（不自动改动工作区）
git fetch origin main

# 看本次带来了多少提交
git log --oneline HEAD..origin/main

# 更新工作区到最新
git reset --hard origin/main
#   或保留本地未提交改动：git merge origin/main（有冲突时解决）

# 记录当前版本提交号，供下一次对比
git rev-parse HEAD
```

> 如果 `git fetch` 因网络失败（GitHub 偶发挂起），重试即可；也可用 `git ls-remote https://github.com/li63050a6/licode.git HEAD` 确认远程可达。

---

## 4. 对比差异

### 4.1 与「上一次拉取」对比

保存好上一次拉取的提交号（例如 `f3748a2`），然后：

```bash
# 最近一次拉取到现在的全部提交
git log --oneline f3748a2..HEAD

# 变更文件统计
git diff --stat f3748a2 HEAD

# 完整差异
git diff f3748a2 HEAD

# 只看新增文件（本次新增了哪些文件）
git diff --name-status f3748a2 HEAD
```

> 若不知道上一次的提交号：`git log --oneline -20` 里找最近一次我们自己拉取/适配的提交即可。

### 4.2 重点关注文件

后端目录里，**会波及前端**的只有一小部分，重点 diff：

| 文件 | 影响前端的原因 |
| --- | --- |
| `cmd/serve.go` | 路由注册、WS 消息/事件处理、`/api/models`、认证 |
| `cmd/files.go` | 文件 API 的请求/响应字段 |
| `cmd/search.go`、`internal/search/*` | 搜索 API（新功能时整包新增） |
| `cmd/audit.go`、`internal/audit/*` | 审计 API 结构 |
| `internal/websocket/websocket.go` | **WS 协议**（消息/事件类型、字段名）——变更要同步改前端 |
| `internal/settings/settings.go` | **设置对象字段**（新增字段要在设置界面补） |
| `internal/agent/*` | 新增工具（可能出现在 ask/工具块展示） |
| `internal/web/static/app.js`、`templates/*.html` | 官方旧 UI 是怎么调新 API 的 → 照抄其请求/响应格式 |

### 4.3 如何判断「差异是否影响前端」

按优先级检查：

1. **WS 协议变了没有？** grep `internal/websocket/websocket.go` 的 `Type*` / `Evt*` 常量、`ClientMessage` / `ServerEvent` 的 json tag。变了 → 改 `nuxtweb/composables/useLicode.ts` 的 `send()`/`handleEvent()`。
2. **REST 变了没有？** 新增/删除 `mux.HandleFunc`；变了字段。前端 REST 调用在 `composables/useApi.ts` + 各 Panel 组件；`/api/**` 代理是通配的，**新增端点无需改代理配置**。
3. **设置对象加了字段？** `internal/settings/settings.go` 的 `Settings` struct → 在 `SettingsDialog.vue` 补对应表单；同时保证 `settings_set` 仍是全量回传。
4. **新功能（新模块/新 API 组）？** 参考官方旧 UI（`internal/web/static/app.js` + `templates/`）的实现方式，在 nuxtweb 里新增组件/页面，并在 `RightPanel.vue` 或 `TopBar.vue` 挂入口。
5. **仅是内部实现/README/文档改动？** 前端无需改动。

---

## 5. 根据差异适配前端（nuxtweb）

### 5.1 新增一个 REST API

1. 在对应组件里用 `useApi<T>` 调用（自动带 `Accept: application/json`；401 自动跳登录；错误抛 `error` 字段）。
2. 例：
   ```ts
   const data = await useApi<{ ... }>('/api/xxx', { method:'POST', headers:{'content-type':'application/json'}, body: JSON.stringify({...}) })
   ```
3. 前端不用改代理：dev 的 `devProxy '/api'` 与 prod 的 `routeRules '/api/**'` 都覆盖所有 `/api/*`。

### 5.2 新增/变更 WS 消息或事件

`composables/useLicode.ts`：
- 发消息：`send('新type', { 字段: 值 })`，字段名以 `ClientMessage` json tag 为准。
- 收事件：在 `handleEvent` 的 `switch` 加 `case '新事件': ...`。
- 需要新 UI 状态就在 `state` 里加字段；新面板入口加到 `RightPanel` 的 tabs 和 `rightTab` 联合类型。

### 5.3 设置对象新增字段

`components/SettingsDialog.vue`：
- 在对应 tab（基础/厂商/高级）加表单控件，`v-model`/`:model-value` 绑定 `local.xxx`。
- **数字**字段记得在 `buildSettings()` 的 `NUM_KEYS` 里加并 `Number(...)` 转换；**逗号列表**（数组）用 `splitList`；**JSON**（如 mcp）用文本域 parse。
- 永远**全量回传**：`save()` 用 `JSON.parse(JSON.stringify(state.settings))` 起步再覆盖字段，切勿只发局部。

### 5.4 新功能面板（以搜索面板为例的套路）

1. 新增 `components/XxxPanel.vue`，用 `useApi` 调新端点。
2. `RightPanel.vue` 的 `tabs` 数组加 `{ label:'xxx', value:'xxx' }`；在面板切换处加 `<XxxPanel v-else-if="state.rightTab === 'xxx'" />`；把 `'xxx'` 加进 `useLicode.ts` 的 `rightTab` 联合类型。
3. 需要常驻事件（如审计完成广播）时在 `handleEvent` 里加 `case` 并 `state.xxxTick++` 触发面板刷新。

### 5.5 改动后端本地副本并重新编译

```bash
cd D:\licode
set GOPROXY=https://goproxy.cn,direct
go build -o %TEMP%\opencode\licode-new.exe .
%TEMP%\opencode\licode-new.exe --host 127.0.0.1 --port 8080 [--password 123]
```

---

## 6. 构建与验证（每次运维必做）

```bash
# 前端类型检查 + 生产构建
cd D:\code\licode\nuxtweb
npx nuxt typecheck
npm run build

# 联调（后端已启动）
npm run dev        # 访问 http://localhost:3000
```

### 验证清单

| # | 项目 | 命令/操作 | 期望 |
| --- | --- | --- | --- |
| 1 | REST 代理 | `curl http://localhost:3000/api/auth` | JSON（不是 HTML） |
| 2 | 版本 | `curl http://localhost:3000/api/version` | `{"version":"0.0.0.x",...}` |
| 3 | 文件 | `curl "http://localhost:3000/api/files?path="` | 工作目录列表 JSON |
| 4 | 搜索 | `curl "http://localhost:3000/api/search/stats"` | `{engines:["bing","baidu","duckduckgo"],...}` |
| 5 | WS | node 脚本 `POST /signin` 拿 cookie → `new WebSocket('ws://localhost:3000/ws',{headers:{Cookie}})` → 发 `settings_get`/`sessions_get` | 收到 `settings`/`sessions` 事件；**后端日志出现「客户端已连接」** |
| 6 | 登录（启用密码时） | 浏览器走 `/login` | 登录成功进主界面；后端日志有连接 |
| 7 | 聊天 | 发一条消息 | 流式返回 |
| 8 | 设置保存 | 改一个字段保存再重开 | 值持久化（`~/.licode/config.json`） |
| 9 | 无头浏览器全流程 | puppeteer-core + Edge：登录→聊天→设置→文件→搜索→审计 | 控制台 0 error/warn |

> 注意：验证设置保存会真实写 `~/.licode/config.json`，**改完要恢复原值**（尤其 api_key/model）。

---

## 7. 常见问题与处理

| 症状 | 可能原因 | 处理 |
| --- | --- | --- |
| 页面纯文本 / 无样式 | 前端缺 `@tailwindcss/vite` | nuxt.config `vite.plugins:[tailwindcss()]` |
| 大量 `Failed to resolve component: Button/Input/...` | fuxsto 组件没 import | 对应 SFC 显式 import |
| 登录总报「用户名或密码错误」（密码正确） | 302 Set-Cookie 被 fetch 丢弃 | 用 `server/routes/signin.post.ts`（http.request 读 302 cookie） |
| 登录成功但后端无「客户端已连接」 | WS 未建立（登录 cookie 未落、或中继没转发 cookie） | 检查 `/signin` 返回 set-cookie；`ws.get.ts` 透传 `peer.request.headers.cookie` |
| 设置无法保存 / 保存后变默认 | WS 未连接；或保存时 `state.settings` 为 null | 保存按钮已禁用+提示；确认后端可达 |
| `/api/*` 返回 HTML 而不是 JSON | devProxy 剥前缀 | `target: '${backend}/api'` |
| WS 1006 打不开 | dev 未开 `nitro.experimental.websocket`；或双 upgrade 处理 | 开 experimental.websocket；hooks 隧道仅在无转发时挂载 |
| 搜索报错/无结果 | 环境无法访问 bing/baidu/ddg | 属网络问题；`local=only` 可只查本地库 |
| 模型只回复文本不调工具 | 模型行为（如 deepseek-v4-flash） | 非前端问题；排查 ask 流程注意 `auto_allow:true` 跳过询问 |
| 后端改了 WS 协议但前端无响应 | 前端 `handleEvent` 没同步 | 对比 4.2 的 websocket.go 差异改 `useLicode.ts` |
| 新 API 404 | 后端没重新编译/没重启 | 重新 `go build` 并重启后端 |
| 前端连不上后端 | `LICODE_BACKEND` 未设或端口不符 | 默认 `http://127.0.0.1:8080`，用 `set LICODE_BACKEND=http://127.0.0.1:端口&& npm run dev` 覆盖 |

---

## 8. 提交与发布

```bash
cd D:\code\licode\nuxtweb
git add -A
git status
git commit -m "feat: 同步后端 <提交号>：<变更摘要>"
git push
```

提交前检查：
- `git status` 只含预期文件；**绝不提交密钥**（`~/.licode/config.json` 不在仓库；检查是否有 `.env`/日志被误加）。
- 前后端版本对应关系写进提交说明（后端提交号 + 适配内容）。

发布形态（当前约定）：
- **开发/日常使用**：后端二进制 + `npm run dev`（端口 3000）。
- **生产**：`npm run build` 后 `node .output/server/index.mjs`（`LICODE_BACKEND` 指向后端；REST 走 routeRules 代理、WS 走 crossws 中继）。
- 后端自带的旧界面（`internal/web`）保留不动，作为兜底。

---

## 9. 运维要点提醒

1. **后端代码仓库有两个镜像**（`li63050a` 与 `li63050a6`），HEAD 一致，任一即可。
2. **协议变化集中检查点**：`internal/websocket/websocket.go`、`internal/settings/settings.go`、`cmd/serve.go`、`cmd/*.go`。
3. **代理是通配的**，新增 REST 端点一般不需要改前端网络层。
4. **设置保存必须全量回传**，这是最容易「改坏后丢配置」的地方；验证时保护好真实 api_key。
5. **验证 WS 时看后端日志**「客户端已连接（当前 N 个）」——这是「前端是否真正连上」的唯一铁证。
6. 每次运维后更新本仓库 README 的「已知边界」，把新发现的坑记下来。
