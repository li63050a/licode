# licode 前端开发文档索引（静态内嵌形态）

> 本目录是 licode Nuxt 前端（`nuxtweb/`）**静态内嵌 Go 后端**形态的开发文档。
> 前端静态化为 `dist/` 后 `go:embed` 进 Go 二进制，单二进制交付，无 Node 运行时、无代理/中继。
> 核心入口：[13-静态生成与内嵌Go后端](13-静态生成与内嵌Go后端.md)。

## 阅读顺序

| # | 文档 | 内容 | 适合谁 |
| --- | --- | --- | --- |
| 13 | [静态生成与内嵌Go后端](13-静态生成与内嵌Go后端.md) | `npm run generate` → `dist/` → `go:embed`、SPA fallback、同源直连、form 登录、构建与验证 | **所有人，先读** |
| 03 | [后端-REST-API参考](03-后端-REST-API参考.md) | 全部 HTTP 端点：请求/响应/错误码（前端 `useApi` 调用依据） | 前后端 |
| 04 | [后端-WebSocket协议](04-后端-WebSocket协议.md) | WS 消息/事件完整规格（`useLicode` 依据） | 前后端 |
| 06 | [后端-设置对象](06-后端-设置对象.md) | Settings 全字段、默认值、保存链路 | 前后端 |
| 05 | [认证与登录](05-认证与登录.md) | cookie/会话令牌、静态内嵌 form 登录链路、登录守卫 | 前后端 |
| 09 | [前端-状态与数据流](09-前端-状态与数据流.md) | useLicode 状态机、事件映射、动作、useApi/useTheme/markdown | 前端 |
| 10 | [前端-组件参考](10-前端-组件参考.md) | 15 个组件逐一：职责/交互/注意点 | 前端 |
| 11 | [陷阱与边界](11-陷阱与边界.md) | 已知坑（含静态化/内嵌专坑） | 所有人，必读 |
| 12 | [验证与调试](12-验证与调试.md) | 静态内嵌冒烟验证与交付检查单 | 前后端 |

## 一页速查（最常用）

```bash
# 前端静态产物（nuxtweb/ 下）
cd D:\code\licode\nuxtweb
npm install
npm run generate         # 静态产物 → dist/
# 改动产物同步进 Go 二进制：bash ../scripts/sync-nuxt.sh

# 后端编译（内嵌目录已在 git，无需 node）
cd D:\code\licode
go build -o licode.exe .

# 运行
licode.exe --host 0.0.0.0 --port 8080 [--password 123]
```

| 关键事实 | 值 |
| --- | --- |
| 页面/API/WS | **同源直连**（单二进制，无代理/中继/CORS） |
| 后端端口 | 8080（`--port` 可改） |
| WS 端点 | `/ws`（同源直连） |
| 登录 | 原生 form `POST /login`（302 + `Set-Cookie` 由浏览器处理）；`/_nuxt/` 静态资源不要求认证 |
| 静态产物 | `dist/` 约 470 KB（gzip 140 KB）→ 嵌入 `internal/web/nuxt` |
| 登录 cookie | `licode_auth`（HttpOnly、SameSite=Lax、7 天） |
| 数据目录 | `~/.licode`（`LICODE_HOME` 可覆盖） |
| 设置文件 | `~/.licode/config.json`（`settings_set` 全量替换落盘） |
| 版本号 | `0.0.0.x`（`~/.licode` 计数，百进制进位） |
| 心跳 | 无 → 前端 2s 自动重连 |
| 会话历史 | 每次 WS 连接全新管理器，切换不回放 |
