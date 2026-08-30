# licode 多厂商与模型

## 协议类型

| 类型 | 说明 | 默认地址 | 默认模型 |
| --- | --- | --- | --- |
| `openai` | OpenAI 兼容（含 Ollama 等任意兼容端点） | `https://api.openai.com/v1` | `gpt-4o-mini` |
| `claude` | Anthropic 原生 `/v1/messages` | `https://api.anthropic.com` | `claude-sonnet-4-20250514` |
| `google` | Gemini 原生 `:generateContent` | `https://generativelanguage.googleapis.com` | `gemini-2.0-flash` |
| `ollama` | 属 openai 兼容，地址给 `/v1` | `http://localhost:11434/v1` | `llama3.1:8b` |

## 自定义厂商

可以随意添加厂商：填 **类型 / 名称 / API 地址 / 密钥 / 模型** 即可。API 地址支持多种格式（以 `/v1` 结尾、完整路径、OpenAI 兼容端点等）。

**一键添加**：网页端「设置 → 新增厂商」：

1. 选类型（openai/claude/google）
2. 填名称、API 地址、密钥
3. 点「获取模型」→ 自动拉取该端点模型列表（`/api/models`）
4. 选模型 → 「添加厂商」

添加后自动成为激活厂商，可在输入区上方的厂商下拉里随时切换。

## 厂商列表（providers）

配置里 `providers` 数组保存所有已配置厂商；`provider` + `model` 字段表示当前激活。切换厂商 = 更新这两个字段并写回 `~/.licode/config.json`。

## 获取模型列表 API

```
GET /api/models
GET /api/models?type=openai&base=https://...&key=...&provider=...
```

- openai：`GET {base}/models`
- google：`GET {base}/v1beta/models?key=...`
- claude：无公开列表接口，返回空

## HTTPS

```bash
./licode --https                      # 自动生成自签名证书（~/.licode/ssl/）
./licode --https --tls-cert c.pem --tls-key k.pem   # 用自有证书
```