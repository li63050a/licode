# licode 配置与数据目录

## 启动参数

`./licode` 直接运行即启动 Web 服务器，参数直接跟在其后。

| 参数 | 说明 | 示例 |
| --- | --- | --- |
| `--host` | 监听主机（默认 `127.0.0.1`） | `--host 0.0.0.0` |
| `--port` | 端口（默认 `8080`） | `--port 8080` |
| `--addr` | 直接指定监听地址（优先） | `--addr 0.0.0.0:9000` |
| `--provider` | 厂商：openai/claude/google/ollama | `--provider ollama` |
| `--base-url` | API 地址 | `--base-url http://localhost:11434/v1` |
| `--api-key` | API 密钥 | `--api-key sk-xxx` |
| `--model` | 模型名 | `--model gpt-4o-mini` |
| `--username` | 登录用户名（默认 `licode`） | `--username admin` |
| `--password` | 登录密码（设置后才启用登录） | `--password 123` |
| `--https` | 启用 HTTPS（自动生成自签名证书） | `--https` |
| `--tls-cert`/`--tls-key` | 指定 TLS 证书/私钥 | `--tls-cert cert.pem --tls-key key.pem` |
| `--no-subagents` | 禁用子代理编排 | `--no-subagents` |
| `-h`/`--help` | 显示简体中文帮助 | `./licode --help` |

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `LICODE_PROVIDER` | 厂商（同 `--provider`） |
| `LICODE_BASE_URL` | API 地址 |
| `LICODE_API_KEY` | API 密钥 |
| `LICODE_MODEL` | 模型名 |
| `LICODE_USERNAME` | 登录用户名 |
| `LICODE_PASSWORD` | 登录密码 |
| `LICODE_HOME` | 数据目录（默认 `~/.licode`） |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | 各厂商标准密钥（未设 `LICODE_API_KEY` 时自动读取） |

优先级：命令行 > 环境变量 > 配置文件 > 默认值。

## 数据目录 `~/.licode`

首次使用自动生成：

```
~/.licode/
├── config.json       设置（含 providers、tool_rules、mcp_servers 等）
├── system-prompt.md  系统提示词（可直接编辑，首次自动生成默认内容）
├── md/               附加提示词：递归读取里面所有 .md 追加到系统提示词（默认空）
├── skills/           技能（markdown，frontmatter: name/description）
├── plugins/          WASM 插件（见 docs/plugins.md）
├── mcp/              MCP 服务器配置
├── sessions/         对话记录（实时保存，每会话一个 json）
├── logs/             日志
├── cache/            缓存
└── ssl/              HTTPS 自签名证书
```

项目内可放 `licode.json` 覆盖用户级配置。数据按系统用户隔离（各用户家目录独立）。

## config.json 写法

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
  "auto_allow": false,
  "tool_rules": {"write_file": "ask", "run_shell": "ask"},
  "providers": [
    {"provider": "openai", "name": "OpenAI", "type": "openai", "base_url": "https://api.openai.com/v1", "api_key": "", "model": "gpt-4o-mini"}
  ],
  "mcp_servers": [
    {"name": "fs", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]}
  ]
}
```

TUI 时代遗留说明：所有设置都可在网页端「设置」里实时修改并自动写回本文件，无需手改。