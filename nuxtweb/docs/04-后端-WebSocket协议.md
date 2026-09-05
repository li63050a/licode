# 04 · 后端：WebSocket 协议

> 端点 `GET /ws`（需登录，Origin 校验**始终放行**）。
> 所有消息为 **JSON 文本帧**（无二进制帧、无心跳、无 ping/pong、无读写超时）。
> 常量定义：`internal/websocket/websocket.go`；消息分发：`cmd/serve.go`（`OnUserMessage` 的 switch）。

## 1. 连接生命周期

```
HTTP GET /ws (携带 licode_auth cookie)
  │  认证失败 → 401
  ▼
Upgrade → 注册到 Hub → 后端打日志「客户端已连接（当前 N 个）」
  │
  ├─ 客户端可立即发送 settings_get / sessions_get / message 等
  ├─ 服务端随时推送事件
  ▼
断开（网络/关停/超时）→ 注销 + 关 send 通道
```

- 每个连接拥有独立的：`connState`（会话管理器）、Agent 实例。
- 客户端发送队列 `send chan`（容量 256，**满了丢弃事件而不是阻塞 Agent**）。
- 消息队列 `msgQueue chan`（容量 50，**满了回 `error`「消息队列已满，请稍候」**）。
- **无心跳** → 前端必须实现自动重连（licode 前端 2s 重连）。

## 2. 客户端 → 服务端消息（ClientMessage）

```jsonc
{
  "type": "<TypeXxx>",
  "content": "",       // message / session_rename / audit_log
  "system": "",        // message 可选：角色系统提示词覆盖，前置到默认系统提示词
  "settings": {},      // settings_set：完整 Settings 对象
  "askId": "", "askApprove": false, "askAlways": false,  // ask_reply
  "sessionId": "",     // session_switch/rename/delete/branch
  "index": 0           // session_branch：分支点消息序号（-1 = 复制整段）
}
```

| type | 行为 |
| --- | --- |
| `message` | 运行 Agent 流式回复。`content=="/clear"` 清空当前会话并回 `done`。**若上一条消息仍在处理中 → `error`「上一条消息仍在处理中，请稍候」**。`system` 非空时作为角色提示词覆盖。 |
| `interrupt` | 取消当前运行，回 `done`。 |
| `settings_get` | 回 `settings`（完整快照）。 |
| `settings_set` | 校验（`Validate()`=创建 LLM 客户端）+ 应用 + **写盘 `~/.licode/config.json`**，回 `settings`；失败回 `error`。 |
| `ask_reply` | 应答 `ask`；`askApprove` 是否允许；`askAlways` 加入「当前会话始终允许」列表。 |
| `sessions_get` | 回 `sessions`。 |
| `session_new` | 新建并切换，回 `sessions`。 |
| `session_switch` | 切换，回 `sessions`。 |
| `session_rename` | 重命名，回 `sessions`。 |
| `session_delete` | 删除，回 `sessions`。 |
| `session_branch` | 复制会话为分支（`index` 为截断点，-1=整段复制），回 `sessions`；失败回 `error`「无法创建分支」。 |
| `session_history` | 请求某会话完整历史（`sessionId`），回 `history`（`messages` + `sessionId`）。会话不存在回 `messages: []`。 |
| `audit_log` | 把 `content`（审计摘要文本）追加为一条助手消息，回 `done`。 |
| `ping` | **已声明但后端未处理**（no-op）——不要依赖。 |

## 3. 服务端 → 客户端事件（ServerEvent）

```jsonc
{
  "type": "<EvtXxx>",
  "content": "",      // delta / status / audit_log
  "toolName": "", "toolArgs": "", "toolOut": "",  // tool_start / tool_done / ask
  "error": "",
  "settings": {},
  "sessions": [{"id":"","title":"","count":0}], "sessionId": "",
  "messages": [{"role":"assistant","content":"","tool_calls":[]}],  // history
  "stats": {},
  "askId": ""
}
```

| type | 说明 |
| --- | --- |
| `delta` | 流式文本增量（`content`）。`streaming=false` 时全部文本作为**一次 delta** 在 `done` 前到达。 |
| `tool_start` | 工具开始：`toolName` + `toolArgs`（**原始 JSON 字符串**）。 |
| `tool_done` | 工具完成：`toolName` + `toolOut`（文本）。 |
| `done` | 一次回复完成（也用于 /clear、interrupt、audit_log）。收到后应重新 `sessions_get`（标题可能自动生成）。 |
| `error` | `error` 字段为可展示文本（busy / 校验失败 / 队列满 / 关停等）。 |
| `status` | 状态文案（如「思考中 (2)」），`content`。 |
| `settings` | 完整设置快照。 |
| `sessions` | `sessions`（id/title/count）+ `sessionId`（当前）。**前端收到后应立刻 `session_history` 拉取当前会话历史回放。** |
| `history` | 会话历史：`messages`（`ai.Message[]`：`role`/`content`/`tool_calls`/`tool_call_id`/`tool_name`）+ `sessionId`。前端仅当 `sessionId` 等于当前会话时才应用（防切换竞态）。 |
| `ask` | 工具权限询问：`askId` + `toolName` + `toolArgs`；前端弹权限条，用户选择后回 `ask_reply`。 |
| `stats` | 会话统计，字段见下。 |
| `audit_log` | **`content` 是一个 JSON 字符串**（二次 parse）：`{"critical":..,"high":..,"medium":..,"low":..,...}`，审计完成时广播给所有客户端。 |

### stats 对象
```jsonc
{
  "messages": 0, "context_tokens": 0, "context_max": 0, "context_pct": 0,
  "provider": "", "model": "",              // 后端当前恒为空字符串！
  "conversation_in": 0, "conversation_out": 0,
  "usage_cached": 0, "cache_hit_rate": 0,
  "always_allow": ["Write", "Shell"]
}
```
- 前端展示 token 应读 `context_tokens` / `context_pct`（**不要读 `tokens`**，旧 UI 的 bug）。
- `provider`/`model` 恒为空 → 前端回退显示 `settings.provider`/`settings.model`。

## 4. 认证
- 升级请求自动携带 `licode_auth` cookie；无 cookie → 401（升级失败）。
- Origin 校验放行（代理/局域网/反代场景）。

## 5. 前端实现要点（`composables/useLicode.ts`）
- `send(type, payload)` 组装 `{type, ...payload}` 发送。
- `handleEvent` 按 `evt.type` 分派：
  - `delta` → 追加到**最后一个 assistant 消息**的 text 块。
  - `tool_start` → 在最后 assistant 消息推 tool 块（running）。
  - `tool_done` → 反向查找**最后一个同名 running 块**写入 out。
  - `status` → `state.statusText`；`done` → busy=false + 重新 `sessions_get`；`error` → busy=false + Message。
  - `ask` → `state.ask`（AskBar 渲染）。
  - `settings`/`stats` → 更新对应状态；`sessions` → 更新列表 + 发 `session_history`；`history` → 重建 `state.messages`（`message.role=='tool'` 并入上一个 assistant 的工具块）。
  - `audit_log` → `auditTick++`（触发审计面板刷新）+ `JSON.parse(content)` 弹摘要 toast。
- 断线 → 2s 自动重连；重连成功后 `onopen` 发 `settings_get` + `sessions_get`。
