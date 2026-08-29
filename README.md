# licode —— AI 编程助手（Web 版）

用 Go 编写的 AI 编程助手，单二进制、静态编译、跨平台。**只有 Web 界面**：浏览器打开即用，手机/电脑均可。

## 功能

- 多 AI 提供商一键切换：**OpenAI / Claude / Ollama / Gemini**（均用各自原生接口），可添加多个厂商并一键获取模型列表
- 工具调用：读写文件、目录、代码搜索、shell 执行，Agent 自主调用并回填；工具规则可配置（允许/询问/拒绝），可"始终允许"，可自动允许
- 子代理系统：explorer / builder / planner，DAG 依赖并行调度，可一次性提交多个独立任务并行执行
- 多对话：会话列表、自动标题、切换、删除，实时保存到 `~/.licode/sessions/`
- MCP 接入（stdio JSON-RPC，可多个）与 Skills 技能（可多个，`skills/*.md`）
- **文件浏览与编辑**：Web 内浏览/编辑项目文件，可把某个文件夹设为工作目录
- 上下文压缩 compaction、自动标题 title_gen
- 多语言界面（默认简体中文，支持 English）；浅色/深色主题（默认浅色）；动画开关（默认关）
- 工具详情默认展开/折叠可设；移动端横竖屏适配
- 系统提示词读取 `~/.licode/system-prompt.md`（首次自动生成默认内容，可直接编辑）；`md/` 目录递归读取所有 `.md` 作为附加提示词
- 登录认证（默认用户名 `licode`，密码自行设置；未启用登录时页面会提醒如何启用）
- SSH 公钥/私钥可配置（`--ssh-pubkey` / `--ssh-privkey`，设置里也可改）
- 数据按系统用户隔离：每个系统用户在自己的家目录 `~/.licode` 运行，互不影响
- 插件系统（开发中，见下方方案）

## 快速开始

```bash
./build.sh                  # 一键编译（47 平台交叉编译，产物在 build/）
./build/licode              # 启动 Web 服务器（默认监听 127.0.0.1:8080）

# 参数
./build/licode --host 0.0.0.0 --port 8080    # 局域网/手机访问
./build/licode --password mypass             # 启用登录（默认用户名 licode）
./build/licode --provider ollama             # 指定 AI 提供商
```

浏览器打开 `http://127.0.0.1:8080` 使用；手机访问请用 `--host 0.0.0.0` 并打开 `http://服务器IP:8080`。

### 一键安装

```bash
curl -sSL https://gitee.com/li63050a/licode/raw/main/install.sh | bash
```

Windows 双击 `install.bat`。安装后直接运行 `licode` 启动服务器。

## 设置

无需重启，网页端「设置」里实时修改并自动写回 `~/.licode/config.json`：提供商、模型、API 地址/密钥、温度、tokens、迭代次数、子代理、上下文压缩、自动标题、需确认/禁用工具、MCP 服务器、主题、语言、工作目录。

启动参数：`--provider` `--base-url` `--api-key` `--model` `--host` `--port` `--username` `--password`

环境变量：`LICODE_PROVIDER` `LICODE_BASE_URL` `LICODE_API_KEY` `LICODE_MODEL` `LICODE_USERNAME` `LICODE_PASSWORD` `LICODE_HOME`

### 启动参数说明（全部为可选项）

| 参数 | 说明 | 示例 |
| --- | --- | --- |
| `--host` | 监听主机。默认 `127.0.0.1`（仅本机）。手机/局域网访问请用 `0.0.0.0` | `--host 0.0.0.0` |
| `--port` | 监听端口，默认 `8080` | `--port 8080` |
| `--addr` | 直接指定监听地址（优先于 host/port） | `--addr 0.0.0.0:9000` |
| `--provider` | AI 提供商：`openai` / `claude` / `google` / `ollama` | `--provider ollama` |
| `--base-url` | AI 提供商 API 地址 | `--base-url http://localhost:11434/v1` |
| `--api-key` | AI 提供商 API 密钥 | `--api-key sk-xxx` |
| `--model` | 模型名 | `--model gpt-4o-mini` |
| `--username` | 登录用户名，默认 `licode` | `--username admin` |
| `--password` | 登录密码；**设置后才启用登录** | `--password mypass` |
| `--https` | 启用 HTTPS（无证书时自动生成自签名证书） | `--https` |
| `--tls-cert` / `--tls-key` | 指定 TLS 证书/私钥文件（配合 HTTPS） | `--tls-cert cert.pem --tls-key key.pem` |
| `--no-subagents` | 禁用子代理编排 | `--no-subagents` |

> 说明：`licode` 不带子命令直接带参数运行，等价于 `licode web <参数>`（`serve` 是别名）。例如
> `licode --host 0.0.0.0 --port 8080 --password 123` 即可启动并启用登录。

### 环境变量说明（与参数等价，参数优先级更高）

| 环境变量 | 说明 |
| --- | --- |
| `LICODE_PROVIDER` | AI 提供商（同 `--provider`） |
| `LICODE_BASE_URL` | API 地址（同 `--base-url`） |
| `LICODE_API_KEY` | API 密钥（同 `--api-key`） |
| `LICODE_MODEL` | 模型名（同 `--model`） |
| `LICODE_USERNAME` | 登录用户名（同 `--username`） |
| `LICODE_PASSWORD` | 登录密码（同 `--password`） |
| `LICODE_HOME` | 用户数据目录（默认 `~/.licode`） |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | 各厂商标准密钥，未设 `LICODE_API_KEY` 时自动读取 |

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
├── config.json    配置文件（设置/MCP 等）
├── skills/        技能（markdown）
├── mcp/           MCP 服务器配置
├── sessions/      对话记录（实时保存）
├── logs/          日志
├── cache/         缓存
├── md/            附加提示词（递归读取其中所有 .md，默认空）
└── plugins/       插件（见下方方案）
```

项目内也可放 `licode.json` 覆盖用户级配置；`/init`（项目内）生成 `.licode/` 目录。

## 构建

```bash
./build.sh      # 47 平台交叉编译，产物在 build/（CGO_ENABLED=0 静态编译）
```

单二进制约 7 MB；完全静态编译，不依赖 glibc/libc；空闲内存 < 30 MB；复制到任意同架构 Linux/macOS/Windows 直接运行。

> **重要声明：发行版本（GitHub/Gitee Releases）不一定是最新的。** 如果你想使用最新代码，请自行 `./build.sh` 编译。

## 架构

```
├── main.go                # 入口（无参数默认启动服务器）
├── cmd/
│   ├── serve.go           # web 命令：Web 服务器 + WebSocket + 路由（serve 为别名）
│   ├── auth.go            # 登录认证（Cookie 会话）
│   └── files.go           # 文件浏览/编辑 + 工作目录 API
├── internal/
│   ├── ai/                # LLMClient 接口 + 工厂 + openai/claude/ollama/gemini
│   ├── agent/             # 主 Agent、工具、子代理 DAG、压缩、MCP、Skills
│   ├── session/           # 多会话 + 上下文截断 + 实时落盘
│   ├── settings/          # 设置 + ~/.licode 数据目录
│   ├── websocket/         # Hub + 事件协议
│   └── web/               # go:embed 静态页面（DeepSeek 风格，浅色默认）
├── install.sh / install.bat
└── build.sh               # 47 平台交叉编译
```

## Web 界面

模仿 DeepSeek 网页版对话界面：浅色默认、居中对话、左侧会话栏、底部大输入框；「文件」标签页可浏览/编辑项目文件并把文件夹设为工作目录；「设置」里切换主题/语言；「关于」查看开发者信息。

## 开发文档

<details>
<summary><b>文件浏览与编辑 / 工作目录（点击展开）</b></summary>

Web 内「文件」标签页：浏览目录树、点击文件在右侧编辑器打开、编辑后保存。点击「设为工作目录」把当前文件夹设为工作目录。

HTTP API：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/files?path=` | 列出目录 |
| GET | `/api/file?path=` | 读取文件 |
| POST | `/api/file` | 写文件 `{path, content}` |
| POST | `/api/mkdir` | 建目录 `{path}` |
| POST | `/api/delete` | 删除 `{path}` |
| GET/POST | `/api/workspace` | 获取/设置工作目录 |

路径安全：所有访问限制在工作目录内（防越界）。Agent 的工具（read_file/write_file/run_shell 等）以服务器当前工作目录为基准运行。
</details>

<details>
<summary><b>子代理系统（点击展开）</b></summary>

主 Agent 通过 `dispatch_subagents` 工具拆解任务给子代理（explorer/builder/planner），支持 `depends_on` 依赖形成 DAG，同层并行。每个子代理独立 System Prompt 与工具集。自定义子代理：在 `.licode/agents/*.md` 或 `~/.licode/agents/*.md` 写 markdown（frontmatter: name/description/tools，正文为提示词）。自定义命令：`.licode/commands/*.md`。
</details>

<details>
<summary><b>插件系统方案（点击展开，开发中）</b></summary>

插件以目录为单位，放在 `~/.licode/plugins/<插件名>/` 或项目 `.licode/plugins/<插件名>/`，每个插件包含 `plugin.json` 清单：

```json
{
  "name": "my-plugin",
  "description": "插件说明",
  "tools": [
    {
      "name": "weather",
      "description": "查询天气",
      "command": ["python3", "weather.py"],
      "schema": { "type": "object", "properties": { "city": { "type": "string" } } }
    }
  ],
  "skills": ["skills/foo.md"],
  "commands": [
    { "name": "build", "description": "构建", "prompt": "请帮我构建并运行测试" }
  ]
}
```

- **tools**：给 LLM 注册工具，调用时把参数 JSON 写入 stdin 传给 `command`，输出作为工具结果
- **skills**：引用插件内的技能 markdown
- **commands**：注册到界面的命令模板

加载目录：`~/.licode/plugins/`、项目 `.licode/plugins/`、`.opencode/plugins/`（兼容）。

> 插件系统方案尚未最终确定，接口可能调整；确定后会在本文件更新开发文档。
</details>

<details>
<summary><b>开发（点击展开）</b></summary>

```bash
go build ./...     # 编译检查
go vet ./...       # 静态检查
go test ./...      # 单元测试
```
</details>

## 联系与交流

- GitHub：https://github.com/li63050a/licode
- Gitee：https://gitee.com/li63050a/licode
- 开发者 B 站：小帅5656
- QQ 交流群：1026939741
- 开发者邮箱：li63050@qq.com