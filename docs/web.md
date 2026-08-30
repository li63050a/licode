# licode Web 界面与 API

## 界面

DeepSeek 设计语言：蓝紫主色 `#4D6BFE`、白底留白、圆角卡片、胶囊渐变按钮、毛玻璃导航。浅色默认，深色在设置切换；多语言（简体中文默认 + English）；动画开关默认关。

布局：
- 左侧：会话列表（可折叠，`☰` 展开/收起）
- 中间：对话区（空状态 Hero、建议 chips、流式消息、工具调用块）
- 右侧：信息（消息数/tokens/模型/工作目录）与文件（浏览/编辑）面板（`☷` 收起）
- 顶部：模型徽标、状态岛（思考中…）、主题切换

## 登录

`--password` 或 `LICODE_PASSWORD` 设置密码后启用登录页（默认用户名 `licode`）。会话 Cookie（HMAC 签名）保护网页与 WebSocket。未启用登录时页面顶部显示提醒横幅。

## 文件与工作目录 API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/files?path=` | 列出目录 |
| GET | `/api/file?path=` | 读取文件 |
| POST | `/api/file` | 写文件 `{path, content}` |
| POST | `/api/mkdir` | 建目录 `{path}` |
| POST | `/api/delete` | 删除 `{path}` |
| GET/POST | `/api/workspace` | 获取/设置工作目录 |
| GET | `/api/version` | 程序版本（0.0.0.x） |
| GET | `/api/auth` | 登录状态 |
| GET | `/api/models` | 模型列表（支持参数覆盖） |

路径安全：所有文件操作限制在工作目录内（防越界）。

## 工具权限

设置里「工具规则」：`工具:allow|ask|deny`（逗号分隔）。风险工具（write_file/run_shell）默认 `ask`，前端弹出「拒绝 / 允许 / 始终允许」；「自动允许」打开后不再询问；「始终允许」仅当前对话生效。

## WebSocket 协议

`/ws`，客户端消息：`message` / `settings_get` / `settings_set` / `sessions_get` / `session_new` / `session_switch` / `session_rename` / `session_delete` / `ask_reply`。

服务端事件：`delta`（流式文本）/ `tool_start` / `tool_done` / `done` / `error` / `status` / `settings` / `sessions` / `ask` / `stats`。