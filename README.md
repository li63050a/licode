# licode —— AI 编程助手（Web 版）

用 Go 编写的 AI 编程助手，单二进制、静态编译、跨平台。浏览器打开即用，手机/电脑均可使用。

> **重要声明：该分支为beta版本，无法保证稳定性，如有介意请去使用main主分支。**

## 功能

- 多 AI 提供商一键切换：**OpenAI / Claude / Ollama / Gemini**（均用各自原生接口），可添加多个厂商并一键获取模型列表
- 工具调用：读写文件、目录、代码搜索、shell 执行，Agent 自主调用并回填；工具规则可配置（允许/询问/拒绝），可"始终允许"，可自动允许
- 子代理系统：explorer / builder / planner，DAG 依赖并行调度
- 多对话：会话列表、自动标题、切换、删除，实时保存到 `~/.licode/sessions/`
- MCP 接入（stdio JSON-RPC，可多个）与 Skills 技能（可多个，`skills/*.md`）
- 上下文压缩 compaction、自动标题 title_gen
- 运行时健壮性：LLM 调用指数退避重试（处理 429/503/网络抖动）、子代理硬超时、上下文窗口滑动保护（token 预算）
- 安全纵深：工具输出敏感信息脱敏（sk-* API Key 等）、可选 Docker 沙箱隔离执行 Shell
- 可观测性：结构化 JSON 日志（trace_id 串联 Agent→Tool 调用链，`LICODE_JSON_LOG=1`）
- 备份迁移：一键导出/导入 zip（配置 + 会话 + 技能 + 附加提示词）
- 热重载：SIGHUP 重载配置（修改 `~/.licode/config.json` 后 `kill -HUP <pid>` 即时生效，不中断服务）
- 浅色/深色主题切换，流式工具调用渲染（参数/结果折叠卡片），生成中可随时"停止"
- 系统提示词读取 `~/.licode/system-prompt.md`；`md/` 目录递归读取所有 `.md` 作为附加提示词
- 离线前端：Nuxt 3 + Vue 静态 SPA（由 `nuxtweb/` 生成），JS/CSS 全部随二进制打包（`go:embed`），无任何 CDN 外部依赖，内网/离线环境开箱即用
- 登录认证（默认用户名 `licode`，密码自行设置；未启用登录时页面会提醒如何启用）
- HTTPS（`--https`，无证书时自动生成自签名证书）
- 数据按系统用户隔离：每个系统用户在自己的家目录 `~/.licode` 运行，互不影响

## 快速开始

```bash
./build.sh                  # 一键编译（9 平台，产物在 build/）
./build/licode-<os>-<arch>  # 启动（如 ./build/licode-linux-amd64）

# 参数
./build/licode-linux-amd64 --host 0.0.0.0 --port 8080    # 局域网/手机访问
./build/licode-linux-amd64 --password mypass             # 启用登录（默认用户名 licode）
```

浏览器打开 `http://127.0.0.1:8080` 使用；手机访问请用 `--host 0.0.0.0` 并打开 `http://服务器IP:8080`。

## 工具与权限

| 工具 | 说明 | 默认权限 |
| --- | --- | --- |
| `Read` | 读取文件（支持 offset/limit） | 允许 |
| `Write` | 写入/替换文件 | **需审批** |
| `Edit` | 查找替换编辑文件（精确修改） | 允许 |
| `ListDirectory` | 列出目录内容 | 允许 |
| `Grep` | 正则搜索代码 | 允许 |
| `Glob` | 按通配符查找文件 | 允许 |
| `Shell` | 执行 shell 命令（构建/测试/git） | **需审批** |
| `Delete` | 删除文件或空目录 | **需审批** |
| `Move` | 移动/重命名文件 | 允许 |
| `Dispatch` | 并行调度子代理执行任务 | 允许 |
| `WebSearch` | 自建联网搜索（必应/百度/DuckDuckGo + 本地收录库） | 允许 |
| `WebFetch` | 抓取网页正文并自动收录到本地库 | 允许 |

> **Skills / MCP 不是"需要手动加载"的东西**：把它们放进对应目录就自动加载、运行中热更新。`WebSearch` / `WebFetch` 同理——搜索功能可用时自动注册，无需手动开启。

**权限配置**（设置 → 工具规则，如 `Read:allow, Write:ask, Bash:deny`）：

- `allow` 允许 · `ask` 避审批 · `deny` 禁用
- 需审批时前端弹出「拒绝 / 允许 / **始终允许**」——始终允许**仅当前对话生效**

## 设置

无需重启，网页端「设置」里实时修改并自动写回 `~/.licode/config.json`：

### 启动参数说明（全部为可选项）

| 参数 | 说明 | 示例 |
| --- | --- | --- |
| `--host` | 监听主机。默认 `127.0.0.1`（仅本机）。手机/局域网访问请用 `0.0.0.0` | `--host 0.0.0.0` |
| `--port` | 监听端口，默认 `8080` | `--port 8080` |
| `--username` | 登录用户名，默认 `licode` | `--username admin` |
| `--password` | 登录密码；**设置后才启用登录** | `--password mypass` |
| `--https` | 启用 HTTPS（无证书时自动生成自签名证书） | `--https` |
| `--tls-cert` / `--tls-key` | 指定 TLS 证书/私钥文件 | `--tls-cert cert.pem --tls-key key.pem` |
| `--no-subagents` | 禁用子代理编排 | `--no-subagents` |
| `-c` / `--config` | 配置文件路径（默认 `config.toml`） | `-c /path/to/config.toml` |

### 环境变量说明

| 环境变量 | 说明 |
| --- | --- |
| `LICODE_USERNAME` | 登录用户名（同 `--username`） |
| `LICODE_PASSWORD` | 登录密码（同 `--password`） |
| `LICODE_HOME` | 用户数据目录（默认 `~/.licode`） |
| `LICODE_JSON_LOG` | 置为 `1` 输出结构化 JSON 日志（含 trace_id） |

### 提供商默认值

| 提供商 | 默认地址 | 默认模型 |
| --- | --- | --- |
| openai | `https://api.openai.com/v1` | `gpt-4o-mini` |
| claude | `https://api.anthropic.com` | `claude-sonnet-4-20250514` |
| ollama | `http://localhost:11434` | `llama3.1:8b` |
| gemini | `https://generativelanguage.googleapis.com` | `gemini-2.0-flash` |

## 登录

`--password` 或 `LICODE_PASSWORD` 设置密码后启用登录界面（默认用户名 `licode`）。登录基于会话 Cookie（HMAC 签名），网页与 WebSocket 均受保护。

## 数据目录

`~/.licode` 首次使用自动生成：

```
~/.licode/
├── config.json      配置文件（AI 设置等）
├── skills/          技能（markdown）
├── mcp/             MCP 服务器配置
├── sessions/        对话记录（实时保存）
├── logs/            日志
│   └── audit/       代码审计报告（JSON，按 task_id 保存）
├── cache/           缓存
├── md/              附加提示词（递归读取其中所有 .md）
└── system-prompt.md 系统提示词（首次自动生成默认内容）
```

## 代码审计与一键修复

「审计」面板（Web 界面右侧第三个标签页）可对整个工作区执行静态规则 + LLM 双重扫描，并支持「生成修复预览 → 人工确认 → 一键修复」流程：

1. **静态扫描**：内置 12 类规则（硬编码密钥、SQL 拼接、eval、命令注入、弱哈希、777 权限、HTTP 明文、DOM 注入、不安全的 unsafe 调用、忽略错误、TODO/FIXME 标记、yaml.load 等），对全部受支持源码文件进行。
2. **LLM 深度分析**：对体积较小的文件（≤ 64 KB，默认最多 8 个文件、3 并发）交由模型分析，并给出修复建议与代码补丁。
3. **人工确认修复**：勾选问题 → 「生成修复预览」，以高亮 diff 展示模型的修改建议；点击「确认修复」后才会写入磁盘，且 **修改前自动生成 `.bak` 备份**，随时可回滚。
4. **会话留痕**：修复完成后会向当前对话追加一条审计记录。

设置项（`config.json`，Web 设置面板可改）：

| 键 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `audit_enabled` | bool | `true` | 是否启用审计功能 |
| `audit_auto_fix` | bool | `true` | 修复前是否自动生成预览 |
| `audit_scan_dirs` | string[] | `["."]` | 扫描目录（相对工作区根） |
| `audit_exclude` | string[] | `vendor/`、`node_modules/`、`.git/`、`dist/` | 排除路径正则 |

API：`GET /api/audit/status`、`POST /api/audit/start`、`GET /api/audit/result?task_id=…`、`POST /api/audit/fix`（`?confirm=true` 时落盘）。审计报告同时以 JSON 保存到 `~/.licode/logs/audit/<task_id>.json`。

## 联网搜索（自建，多引擎）

Web 界面「搜索」面板可同时检索多个引擎（必应 / 百度 / DuckDuckGo，均为自建解析，无第三方搜索 API）与本地已收录库；每条结果支持**网页预览**与**收藏收录**。本地库用倒排索引（中文 bigram 分词 + BM25），持久化在 `~/.licode/search/index.json`，支持增、删、站内检索。

已接入 Agent 工具（`WebSearch` 多引擎合成检索 / `WebFetch` 抓单页全文并自动收录），对话中即可联网查询。

API：`GET /api/search?q=…&engines=bing,baidu,duckduckgo&local=1&max=…`、`GET/POST /api/search/fetch`、`POST /api/search/save`、`GET /api/search/catalog`、`POST /api/search/delete`、`GET /api/search/engines|stats`。

## 文档

- [⚙️ 配置与数据目录](docs/config.md)
- [🧠 多厂商与模型](docs/providers.md)
- [🤖 子代理系统](docs/agents.md)
- [🌐 Web 界面与 API](docs/web.md)
- [🛠 开发与构建](docs/develop.md)
- [❓ 常见问题](docs/faq.md)

## 构建

```bash
./build.sh      # 9 平台交叉编译，产物在 build/（CGO_ENABLED=0 静态编译）
```

单二进制约 7 MB；完全静态编译，不依赖 glibc；空闲内存 < 30 MB。

## 架构

```
├── main.go                # 入口
├── cmd/
│   ├── serve.go           # Web 服务器 + WebSocket + 路由
│   ├── auth.go            # 登录认证
│   ├── files.go           # 文件浏览/编辑 API
│   └── audit.go           # 代码审计 API
├── internal/
│   ├── ai/                # LLMClient 接口 + openai/claude/ollama/gemini
│   ├── agent/             # 主 Agent、工具、子代理 DAG、MCP、Skills
│   ├── session/           # 多会话 + 实时落盘
│   ├── settings/          # 设置 + ~/.licode 数据目录
│   ├── audit/             # 代码审计（静态规则 + LLM 分析 + 修复）
│   ├── websocket/         # Hub + 事件协议
│   └── web/               # go:embed Nuxt 静态前端 + 旧版资源
└── build.sh               # 9 平台交叉编译
```

## 开发

```bash
go build ./...     # 编译检查
go vet ./...       # 静态检查
go test ./...      # 单元测试
```

## 联系与交流

- **GitHub**：https://github.com/li63050a/licode
- **Gitee**：https://gitee.com/li63050a/licode
- **开发者 B 站**：[小帅5656](https://b23.tv/nDqj0DT) — 关注获取最新动态、教程、演示
- **QQ 技术交流群**：[点击加入](https://qun.qq.com/universal-share/share?ac=1&authKey=zq9BYcTtBQm6GbvWiEWiBvDWNWbqhw2%2F%2BRnGM21c0jcL%2FofGqBFeXLr%2BtYT3SkO6&busi_data=eyJncm91cENvZGUiOiIxMDI2OTM5NzQxIiwidG9rZW4iOiJxNkNWUTUxYXVxSmRHZXRvdWtkZnhaN25INzJrMmNaNFpVTjJ5ZTVLYmRvWTFuOEZTd093UXBtQi8vQWk2T1JyIiwidWluIjoiMzYzNTczNjE4MCJ9&data=073ZrPEFZXFvoEDWatbWTidAitiN4OIbiaVDWoR7hVIwJurEPC7Swm6OREVpn6omzobXLn3SRErNKxKbYDTZQA&svctype=4&tempid=h5_group_info)（群号：1026939741）— 提问、反馈 bug、讨论功能
- **开发者邮箱**：li63050@qq.com

## 👥 贡献者

感谢所有开发者（排名不分先后）：

[![贡献者列表](https://contrib.rocks/image?repo=li63050a/licode)](https://github.com/li63050a/licode/graphs/contributors)

---

