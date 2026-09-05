# licode 开发文档索引

> 本目录是 **licode 独立前端（nuxtweb）+ Go 后端** 的完整开发文档，按主题拆分，任何关键点均已写明。
> 后端源码仓库（镜像，HEAD 一致，拉取任一即可）：
> - `https://github.com/li63050a/licode`
> - `https://github.com/li63050a6/licode`
>
> 日常运维（拉后端最新源码 → 对比差异 → 适配前端）见 [ops-guide.md](ops-guide.md)。

## 阅读顺序

| # | 文档 | 内容 | 适合谁 |
| --- | --- | --- | --- |
| 01 | [概览与架构](01-概览与架构.md) | 三层架构、通信拓扑、技术栈、仓库 | 所有人，先读 |
| 02 | [后端-构建与运行](02-后端-构建与运行.md) | 目录结构、依赖、构建、启动参数、数据目录、健康检查 | 后端/联调 |
| 03 | [后端-REST-API参考](03-后端-REST-API参考.md) | 全部 HTTP 端点：请求/响应/错误码 | 前后端 |
| 04 | [后端-WebSocket协议](04-后端-WebSocket协议.md) | WS 消息/事件完整规格与行为 | 前后端 |
| 05 | [认证与登录](05-认证与登录.md) | cookie/会话令牌、登录链路、前端 /signin 中继 | 前后端 |
| 06 | [后端-设置对象](06-后端-设置对象.md) | Settings 全字段、默认值、保存链路 | 前后端 |
| 07 | [后端-核心子系统](07-后端-核心子系统.md) | Agent/工具/子代理/会话/插件/搜索/审计/RAG/缓存 | 深入开发 |
| 08 | [前端-架构与对接](08-前端-架构与对接.md) | nuxtweb 结构、代理/中继实现、nuxt.config 逐段 | 前端 |
| 09 | [前端-状态与数据流](09-前端-状态与数据流.md) | useLicode 状态机、事件映射、动作、useApi/useTheme/markdown | 前端 |
| 10 | [前端-组件参考](10-前端-组件参考.md) | 15 个组件逐一：职责/交互/注意点 | 前端 |
| 11 | [陷阱与边界](11-陷阱与边界.md) | 所有已知坑：现象/根因/修复/位置 | 所有人，必读 |
| 12 | [验证与调试](12-验证与调试.md) | 构建、冒烟、WS 脚本、无头浏览器全流程验证 | 前后端 |

运维：[ops-guide.md](ops-guide.md)（拉取最新后端源码 → 对比差异 → 适配前端 → 验证发布）。

## 一页速查（最常用）

```bash
# 后端（国内网络先设代理）
cd D:\licode
set GOPROXY=https://goproxy.cn,direct&& go build -o %TEMP%\opencode\licode-new.exe .
%TEMP%\opencode\licode-new.exe --host 127.0.0.1 --port 8080 [--password 123]

# 前端
cd D:\code\licode\nuxtweb
npm run dev                 # http://localhost:3000
npx nuxt typecheck          # 类型检查
npm run build               # 生产构建 → node .output/server/index.mjs

# 环境变量
LICODE_BACKEND=http://127.0.0.1:8080   # 前端代理目标（默认 8080）
```

| 关键事实 | 值 |
| --- | --- |
| 前端端口 | 3000（Nuxt dev / .output） |
| 后端端口 | 8080（`--port` 可改） |
| WS 端点 | `/ws`（前端 origin → crossws 中继 → 后端） |
| 登录 cookie | `licode_auth`（HttpOnly、SameSite=Lax、7 天，设在前端 origin） |
| 数据目录 | `~/.licode`（`LICODE_HOME` 可覆盖） |
| 设置文件 | `~/.licode/config.json`（`settings_set` 全量替换落盘） |
| 搜索索引 | `~/.licode/search/index.json` |
| 版本号 | `0.0.0.x`（`~/.licode` 计数，百进制进位） |
| 心跳 | 无 → 前端 2s 自动重连 |
| 会话历史 | 每次 WS 连接全新管理器，切换不回放 |
