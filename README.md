

# licode —— 终端里的 AI 编程助手

用 Go 编写的 AI 编程助手，单二进制、静态编译、跨平台。

- **本地 TUI**：`licode tui`
- **Web 网页端**：`licode serve`，手机/电脑浏览器均可访问
- **远程连接**：`licode tui --remote ws://服务器:8080/ws`，瘦客户端只渲染界面，推理全部在服务器执行

## 功能

- 多 AI 提供商一键切换：**OpenAI / Claude / Ollama**，工厂模式 + 统一 `LLMClient` 接口，改配置即可切换，业务代码零改动
- 工具调用（Function Calling）：内置 `read_file` / `write_file` / `list_dir` / `grep` / `glob` / `run_shell`，Agent 自主调用并回填结果
- 子代理系统：`explorer` / `builder` / `planner` 三个专用子代理，支持 `depends_on` 依赖形成 DAG，同层任务并行执行
- 会话管理：多轮历史 + 上下文窗口截断，会话隔离（每个连接独立 Session）
- 远程机制：服务器执行推理，客户端只渲染，WebSocket 转发流式结果

## 快速开始

```bash
make build          # 一键编译（多平台静态二进制）
./dist/licode --help

# 本地 TUI（默认 provider=openai，可用环境变量覆盖）
export LICODE_API_KEY=sk-...
./dist/licode tui

# 指定提供商
./dist/licode tui --provider claude --api-key $ANTHROPIC_KEY
./dist/licode tui --provider ollama          # 本地 Ollama，无需密钥

# 启动服务器（浏览器 + 远程 TUI）
./dist/licode serve --addr :8080

# 另一台机器 / 手机以远程瘦客户端连接
./dist/licode tui --remote ws://192.168.1.10:8080/ws
```

## 设置

无需任何配置文件。设置可在 **TUI（按 s 键）/ 网页端（设置按钮）/ 远程连接** 中实时修改并立即生效：提供商、模型、API 地址、API 密钥、温度、最大输出 tokens、最大迭代次数、子代理开关、需确认/禁用的工具。

启动时也可用命令行参数或环境变量初始化：

- 环境变量：`LICODE_PROVIDER`、`LICODE_BASE_URL`、`LICODE_API_KEY`、`LICODE_MODEL`（以及标准的 `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY`）
- 命令行：`--provider`、`--base-url`、`--api-key`、`--model`

内置默认值：

| 提供商 | 默认地址 | 默认模型 |
| --- | --- | --- |
| openai | `https://api.openai.com/v1` | `gpt-4o-mini` |
| claude | `https://api.anthropic.com` | `claude-sonnet-4-20250514` |
| ollama | `http://localhost:11434` | `llama3.1:8b` |
| gemini | `https://generativelanguage.googleapis.com` | `gemini-2.0-flash` |

各提供商使用其原生接口：

- OpenAI：`POST /v1/chat/completions`（SSE 流式 + 工具调用增量）
- Claude (Anthropic)：`POST /v1/messages`（原生路径与请求体）
- Gemini (Google)：`POST /v1/models/{模型名}:generateContent`（原生 RPC 风格）

## 构建

```bash
make build      # Linux amd64/arm64 + macOS + Windows，静态编译
make build-host # 仅当前主机版本（./dist/licode）
make upx        # UPX 压缩（可选）
make size       # 查看产物体积
./build.sh      # 47 平台全量交叉编译（见下方说明）
```

约束：

- `CGO_ENABLED=0` 完全静态编译，二进制不依赖 glibc/libc 等任何动态库
- 单二进制约 7 MB，UPX 后可 < 10 MB
- 空闲内存 < 30 MB
- 复制到任意同架构 Linux/macOS/Windows 机器直接运行

> `build.sh`：47 平台全量交叉编译脚本，静态编译优先，个别强制要求 cgo 的平台自动回退动态编译，无法编译的平台自动跳过。

## 访问认证

`serve` 支持用户名/密码认证，网页端与远程 TUI 均受保护。

- 默认用户名：`licode`
- 未设置密码时认证关闭；设置后网页与远程连接都要求登录
- 配置方式（环境变量或启动参数二选一）：

```bash
# 方式一：环境变量
export LICODE_USERNAME=myname
export LICODE_PASSWORD=mypass
./dist/licode serve

# 方式二：启动参数
./dist/licode serve --username myname --password mypass

# 远程 TUI 使用相同凭据（同样支持环境变量）
./dist/licode tui --remote ws://192.168.1.10:8080/ws --username myname --password mypass
```

## 界面操作

**TUI**

| 按键 | 作用 |
| --- | --- |
| Enter | 发送 |
| Tab | 切换 聊天 / 文件栏 焦点 |
| ↑↓ | 文件栏中移动选择；Enter 将所选路径放入输入框 |
| PageUp / PageDown | 回看 / 回到底部 |
| /clear | 清空会话 |
| Esc | 取消当前输出 |
| Ctrl+C | 取消输出；再按一次退出 |

**Web**

浏览器打开 `http://<host>:8080`，Enter 发送、Shift+Enter 换行、`/clear` 清空。

## 架构

```
├── main.go                # Cobra 根命令
├── cmd/
│   ├── tui.go             # 本地/远程 Bubble Tea 终端界面
│   └── serve.go           # Web 服务器 + WebSocket 端点
├── internal/
│   ├── ai/                # LLMClient 接口 + 工厂 + openai/claude/ollama/gemini
│   ├── agent/             # 主 Agent、工具注册与执行、子代理调度
│   ├── session/           # 多轮会话 + 上下文窗口截断
│   ├── settings/          # 运行时设置（无配置文件，界面内实时修改）
│   ├── websocket/         # Hub + Client 连接管理、事件协议、认证
│   └── web/               # go:embed 嵌入的静态页面
├── Makefile
├── build.sh               # 47 平台交叉编译脚本
└── go.mod
```

`licode` 直接运行（不带参数）即进入 TUI；`licode serve` 启动服务器。

### AI 提供商（工厂模式）

统一接口 `ai.LLMClient`（`internal/ai/ai.go`）：`Chat`、`ChatStream`（SSE 流式 + 工具调用增量解析）。`ai.New(cfg)` 按 `provider` 字段创建实现，切换提供商只改配置，业务代码零改动。

### 工具调用

`internal/agent/tools.go` 内置：`read_file`、`write_file`、`list_dir`、`grep`、`glob`、`run_shell`。Agent 循环：调用 LLM → 若返回工具调用则执行并把结果回填 → 重复，直到模型直接给出答案。

### 子代理系统

见 [docs/subagents.md](docs/subagents.md)。主 Agent 通过 `dispatch_subagents` 工具把任务拆解给专门的子代理（`explorer` / `builder` / `planner`），支持 `depends_on` 依赖形成 DAG，同一层的任务并行执行。

### 远程连接机制

- `serve` 监听 `/ws`，每个连接拥有独立 Agent + Session（会话隔离）
- 远程 `tui --remote ws://…` 变成瘦客户端：只渲染，所有推理在服务器
- 事件协议：`delta`（流式文本）/ `tool_start` / `tool_done` / `done` / `error` / `status`

## 开发

```bash
go build ./...     # 编译检查
go vet ./...       # 静态检查
go test ./...      # 单元测试（AI 流式解析 / Agent 工具循环 / DAG 调度 / 会话截断）
```

## 说明

- 所有面向用户的文字均为简体中文
- 仅依赖 `bubbletea`、`lipgloss`、`gorilla/websocket`、`cobra` 与 Go 标准库

## 联系与交流

- GitHub 仓库：https://github.com/li63050a/licode
- Gitee 仓库：https://gitee.com/li63050a/licode
- 开发者 B 站：小帅5656
- QQ 交流群：1026939741
- 开发者邮箱：li63050@qq.com