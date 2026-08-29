# licode —— 终端里的 AI 编程助手

用 Go 编写的 AI 编程助手：单二进制、静态编译、跨平台。在本地终端（TUI）或浏览器（Web）中使用，也支持远程瘦客户端模式。

- **本地 TUI**：直接运行 `licode`（无需参数）
- **Web 网页端**：`licode serve`，手机/电脑浏览器均可访问
- **远程连接**：`licode tui --remote ws://服务器:8080/ws`，瘦客户端只渲染界面，AI 推理全部在服务器执行

## 功能一览

- 多 AI 提供商一键切换：**OpenAI / Claude / Ollama / Gemini**，均使用各自原生接口，工厂模式 + 统一 `LLMClient` 接口
- 工具调用（Function Calling）：内置读写文件、目录、代码搜索、shell 执行等工具，Agent 自主调用并回填结果
- 子代理系统：`explorer` / `builder` / `planner` 专用子代理，支持 DAG 依赖并行调度
- 运行时设置：**无需任何配置文件**，在 TUI / Web / 远程界面中实时修改、立即生效
- 访问认证：网页与远程连接均支持用户名密码（默认用户名 `licode`）
- 会话管理：多轮历史 + 上下文窗口截断，每个连接独立会话
- 完全静态编译：`CGO_ENABLED=0`，不依赖任何动态库

## 快速开始

### 一键安装（推荐）

```bash
# Linux / macOS（脚本会自动下载对应平台二进制并安装到 /usr/local/bin 或 ~/.local/bin）
curl -sSL https://gitee.com/li63050a/licode/raw/main/install.sh | bash

# 指定从 GitHub 下载
curl -sSL https://raw.githubusercontent.com/li63050a/licode/main/install.sh | bash -s github
```

Windows：双击 `install.bat`，或

```bat
install.bat
```

安装后直接运行：

```bash
licode            # 进入 TUI（默认模式）
licode serve      # 启动服务器
```

### 源码构建
make build-host     # 编译本机版本（输出 build/licode）
./build/licode --help

# 直接进入 TUI（默认模式）
./build/licode

# 指定提供商
export LICODE_API_KEY=sk-...
./build/licode --provider openai
./build/licode --provider claude --api-key $ANTHROPIC_API_KEY
./build/licode --provider ollama          # 本地 Ollama，无需密钥

# 启动服务器（浏览器 + 远程 TUI 均可连接）
./build/licode serve --addr :8080

# 另一台机器 / 手机以远程瘦客户端连接
./build/licode tui --remote ws://192.168.1.10:8080/ws
```

安装到系统：

```bash
make install         # 安装到 /usr/local/bin/licode
# 或自定义目录
make install PREFIX=$HOME
licode               # 直接进入 TUI
```

## 设置

无需配置文件。设置可在 **TUI（按 `s`）**、**网页端（设置按钮）**、**远程连接** 中实时修改：

提供商、模型、API 地址、API 密钥、温度、最大输出 tokens、最大迭代次数、子代理开关、需确认/禁用的工具。

启动时可用环境变量或命令行参数初始化：

| 环境变量 | 说明 |
| --- | --- |
| `LICODE_PROVIDER` | 提供商（openai/claude/ollama/gemini） |
| `LICODE_BASE_URL` | API 地址 |
| `LICODE_API_KEY` | API 密钥 |
| `LICODE_MODEL` | 模型名 |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | 各提供商标准密钥 |

命令行参数：`--provider`、`--base-url`、`--api-key`、`--model`。

### 配置文件与数据目录

用户数据目录为 `~/.licode`，**首次使用自动生成**，包含：

```
~/.licode/
├── config.json    配置文件（设置、MCP 等）
├── skills/        技能（markdown）
├── mcp/           MCP 服务器配置
├── sessions/      对话记录（每个会话一个 JSON 文件）
├── logs/          日志
└── cache/         缓存
```

配置文件写法（`~/.licode/config.json`，TUI 与网页端修改设置时会自动写回）：

```json
{
  "provider": "claude",
  "model": "claude-sonnet-4-20250514",
  "base_url": "https://api.anthropic.com",
  "api_key": "sk-ant-xxx",
  "temperature": 0.7,
  "max_tokens": 4096,
  "max_iterations": 16,
  "subagents": true,
  "compaction": true,
  "title_gen": true,
  "ask_tools": [],
  "deny_tools": [],
  "providers": [
    {"provider": "claude", "base_url": "https://api.anthropic.com", "api_key": "sk-ant-xxx", "model": "claude-sonnet-4-20250514"}
  ],
  "mcp_servers": [
    {"name": "fs", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]}
  ]
}
```

- 项目内也可放 `licode.json` 覆盖用户级配置
- 在 TUI 按 `s` 或网页端「设置」修改的任何配置都会实时同步写回 `~/.licode/config.json`
- 在项目根目录输入 `/init` 会生成项目级 `.licode/` 目录（agents/ commands/ skills/ 等，用法同 opencode 的 `.opencode`）

### 提供商默认值

| 提供商 | 默认地址 | 默认模型 |
| --- | --- | --- |
| openai | `https://api.openai.com/v1` | `gpt-4o-mini` |
| claude | `https://api.anthropic.com` | `claude-sonnet-4-20250514` |
| ollama | `http://localhost:11434` | `llama3.1:8b` |
| gemini | `https://generativelanguage.googleapis.com` | `gemini-2.0-flash` |

各提供商使用原生接口：

- **OpenAI**：`POST /v1/chat/completions`（SSE 流式 + 工具调用增量）
- **Claude (Anthropic)**：`POST /v1/messages`（原生路径与请求体）
- **Gemini (Google)**：`POST /v1/models/{模型名}:generateContent`（原生 RPC 风格）

## 访问认证

`serve` 支持用户名密码认证，网页端与远程 TUI 均受保护。

- 默认用户名：`licode`
- 未设置密码时认证关闭；设置后网页与远程连接都要求登录

```bash
# 环境变量方式
export LICODE_USERNAME=myname
export LICODE_PASSWORD=mypass
./build/licode serve

# 启动参数方式
./build/licode serve --username myname --password mypass

# 远程 TUI 使用相同凭据
./build/licode tui --remote ws://192.168.1.10:8080/ws --username myname --password mypass
```

## 界面操作

**TUI 快捷键**

| 按键 | 作用 |
| --- | --- |
| enter | 发送消息 |
| `/` | 命令面板（/clear /settings /help /files /exit） |
| tab | 切换 消息区 / 文件树 |
| ↑↓ →← | 文件树移动、展开/折叠目录 |
| enter（文件） | 将所选文件路径放入输入框 |
| `s` | 打开设置 |
| `?` | 快捷键帮助 |
| esc | 取消当前输出 / 返回 |
| ctrl+c | 取消输出；再次按下退出 |

**Web**

浏览器打开 `http://<host>:8080`：Enter 发送、Shift+Enter 换行、`/clear` 清空、设置按钮修改配置。

## 工具调用

内置工具（`internal/agent/tools.go`）：

| 工具 | 说明 |
| --- | --- |
| `read_file` | 读取文件（支持 offset/limit） |
| `write_file` | 写入/替换文件 |
| `list_dir` | 列出目录 |
| `grep` | 正则搜索代码 |
| `glob` | 按通配符查找文件 |
| `run_shell` | 执行 shell 命令（构建/测试/git） |

Agent 循环：调用 LLM → 若返回工具调用则执行并回填结果 → 重复，直到模型直接给出答案。工具的权限可在设置中设为「需确认」或「禁用」。

## 子代理系统

主 Agent 面对复杂任务时，把工作拆解成多个专门子任务交给**专门的子代理**并行执行，例如「先探索代码 → 再制定计划 → 最后实施修改」。子代理之间通过 **DAG 依赖**约束先后关系。

### 子代理（SubAgent）

每个子代理是一个**独立的 Agent 实例**：拥有自己的 System Prompt、自己的工具集（默认工具的子集）、独立的会话历史，共用同一个 AI 提供商。

内置子代理：

| 名称 | 职责 | 工具 |
| --- | --- | --- |
| `explorer` | 探索代码库，给出带 文件:行号 的结论 | read_file, list_dir, glob, grep |
| `builder` | 实施改动并用构建/测试验证 | read_file, write_file, list_dir, grep, glob, run_shell |
| `planner` | 制定分步实施计划（不写代码） | 无 |

### 任务（Task）

```json
{
  "name": "t1",
  "agent": "explorer",
  "prompt": "找出配置解析相关代码",
  "depends_on": []
}
```

- `name`：任务唯一标识，供其它任务引用
- `agent`：使用哪个子代理
- `depends_on`：依赖的任务名列表，依赖任务完成后当前任务才能启动

### DAG 调度（Scheduler）

1. 校验：任务名唯一、子代理存在、依赖引用有效
2. 分层：每一轮选出「依赖全部完成」的任务作为当前层
3. 并行：同层任务用 goroutine 并行执行（WaitGroup 同步）
4. 循环：执行完一层再选下一层，直到全部完成
5. 死锁检测：某轮没有可执行任务但仍有剩余 → 报告依赖环

```
    ┌─ t1(explorer) ──────────┐
    │          ┌─ t3(builder)  ── 汇总
    └─ t2(planner) ───────────┘
        depends_on: t1 ──► t3
```

### 与主 Agent 的集成

主 Agent 注册 `dispatch_subagents` 工具（参数：`tasks` 数组，含 `depends_on`）。主 Agent 决定拆解策略 → 工具执行调度 → 返回各任务 JSON 结果 → 主 Agent 汇总成最终回答。

### 扩展子代理

```go
agent.SubAgentSpec{
    Name:   "tester",
    Prompt: "你是测试子代理…",
    Tools:  []string{"run_shell", "read_file"},
    Client: client,
}
```

通过 `agent.RegisterSubAgents(specs)` 挂到主 Agent 即可，`dispatch_subagents` 工具会自动把这些名字暴露给模型。

## 远程连接机制

- `serve` 监听 `/ws`，每个连接拥有独立 Agent + Session（会话隔离）
- 远程 `tui --remote ws://…` 变成瘦客户端：只渲染，所有推理在服务器
- 事件协议：`delta`（流式文本）/ `tool_start` / `tool_done` / `done` / `error` / `status` / `ask` / `settings`
- 认证：HTTP Basic Auth，WebSocket 握手时携带凭据

## 架构

```
├── main.go                # Cobra 根命令（无参数默认进入 TUI）
├── cmd/
│   ├── tui.go             # 本地/远程 Bubble Tea 终端界面（文件树/命令面板/设置）
│   ├── serve.go           # Web 服务器 + WebSocket 端点
│   └── auth.go            # 用户名密码认证
├── internal/
│   ├── ai/                # LLMClient 接口 + 工厂 + openai/claude/ollama/gemini
│   ├── agent/             # 主 Agent、工具注册与执行、子代理 DAG 调度
│   ├── session/           # 多轮会话 + 上下文窗口截断
│   ├── settings/          # 运行时设置（无配置文件，界面内实时修改）
│   ├── websocket/         # Hub + Client 连接管理、事件协议
│   └── web/               # go:embed 嵌入的静态页面
├── Makefile               # build / build-all / install / upx
├── build.sh               # 47 平台交叉编译脚本
└── go.mod
```

## 构建

一键脚本：在项目根目录执行

```bash
./build.sh
```

产物输出到 `build/`。脚本会做 47 平台全量交叉编译：`CGO_ENABLED=0` 静态编译优先，个别强制要求 cgo 的平台自动回退动态编译，无法编译的平台自动跳过。

约束：完全静态编译，不依赖 glibc/libc 等任何动态库；单二进制约 7 MB（UPX 后 < 10 MB）；空闲内存 < 30 MB；复制到任意同架构 Linux/macOS/Windows 直接运行。

## 开发

```bash
go build ./...     # 编译检查
go vet ./...       # 静态检查
go test ./...      # 单元测试（AI 流式解析 / Agent 工具循环 / DAG 调度 / 会话截断）
```

## 联系与交流

- GitHub 仓库：https://github.com/li63050a/licode
- Gitee 仓库：https://gitee.com/li63050a/licode
- 开发者 B 站：小帅5656
- QQ 交流群：1026939741
- 开发者邮箱：li63050@qq.com