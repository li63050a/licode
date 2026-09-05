# 03 · 后端：REST API 参考

> 所有端点返回 JSON（`/health`、`/api/export`、`/api/download` 例外）。错误统一为 `{"error":"..."}`（**中文可读文本**，前端直接展示）。
> 除标注「无需认证」外均需登录（未登录时若请求头带 `Accept: application/json` 返回 **401 纯文本**，否则 `302 → /login`）。
> 请求体上限：文件保存/删除等 ≤1MB；上传 ≤256MB；导入 ≤64MB。

## 1. 认证 / 元信息

### `GET /health`（无需认证）
```
200 {"status":"ok"}
```

### `GET /ready`（无需认证）
```
200 {"status":"ready"}
503 {"status":"shutting_down"}
503 {"status":"not_ready","problems":["llm_unreachable","docker_unavailable"]}
```
探测项：base_url TCP 拨号（15s 缓存）、docker info。

### `GET /api/auth`（无需认证）
```
200 {"enabled":false,"username":"licode","default_username":"licode"}
```
前端用它判断是否显示登录页。

### `GET /api/version`
```
200 {"version":"0.0.0.2","counter":2}
```

### `GET /api/nodejs`
```
200 {"node":"v22.…","npx":"…","ok":true}
```

## 2. 文件与工作目录（`cmd/files.go`）

### `GET /api/files?path=` 列目录
- `path` 绝对路径或工作目录相对路径；空 = 工作目录；`.` = 工作目录。
```
200 {"root":"D:\\code\\licode","path":"D:\\code\\licode",
     "entries":[{"name":"cmd","path":"D:\\code\\licode\\cmd","isDir":true,"size":0},
                {"name":"go.mod","path":"D:\\code\\licode\\go.mod","isDir":false,"size":1207}]}
```
- 隐藏文件（`.` 开头）不显示；目录排在文件前。
- `root` 是工作目录，`path` 是实际列出的目录（可越出 root 全盘浏览）。

### `GET /api/file?path=` 读文件
```
200 {"path":"绝对路径","content":"..."}
```

### `POST /api/file` 写文件（≤1MB）
```json
{ "path": "相对或绝对路径", "content": "文本内容" }
```
```
200 {"ok":true,"path":"绝对路径"}
```
- 自动创建父目录。

### `POST /api/mkdir` 建目录
```json
{ "path": "..." }
```
```
200 {"ok":true,"path":"绝对路径"}
```

### `POST /api/delete` 删除
```json
{ "path": "...", "recursive": false }
```
```
200 {"ok":true}
```
- 目录非空且未带 `recursive:true` → **409** `{"error":"目录非空…"}`（前端据此二次确认递归删除）。

### `POST /api/chmod` 修改权限
```json
{ "path": "...", "mode": "644" }
```
```
200 {"ok":true,"path":"绝对路径","mode":"644"}
```

### `POST /api/chown` 修改属主
```json
{ "path": "...", "owner": "1000:1000" }
```
```
200 {"ok":true,"path":"绝对路径","uid":1000,"gid":1000}
```
- `-1` 表示该项保持不变。

### `POST /api/upload` 上传（multipart，≤256MB）
- 表单字段：`file`（**单个**文件）、`dir`（目标目录，绝对路径或相对工作目录，**缺省 = 工作目录**）。
- 保留原始文件名（去路径、净化非法字符）；重名自动 `_1/_2` 去重。
- 返回 `path` 为**绝对路径**；**没有 `url` 字段**（旧版 `/uploads/*` 路由也不存在，别用）。
```
200 {"ok":true,"path":"D:\\code\\licode\\nuxtweb\\_x.txt"}
400 {"error":"目录无效"|"目标目录不存在"|"文件过大或格式错误"}
```

### `GET /api/download?path=` 下载
- 文件 → 附件直链（`Content-Disposition: attachment`，兼容中文文件名）。
- 目录 → 实时打包 zip 流式下载（文件名 `<目录名>.zip`）。

### `GET/POST /api/workspace` 工作目录
```
GET 200 {"root":"D:\\code\\licode"}
POST {"path":"D:\\xxx"} → 200 {"ok":true,"root":"D:\\xxx"}
```

## 3. 备份（`cmd/backup.go`）

### `GET /api/export`
- zip 下载，`Content-Disposition: attachment; filename="licode-backup.zip"`。

### `POST /api/import`（原始 zip 字节，≤64MB）
- 请求体直接是 zip 二进制（**不是 FormData**），`Accept: application/json`。
```
200 {"ok":true}
200 {"ok":false,"error":"..."}
```

## 4. 联网搜索（`cmd/search.go`，`internal/search`）

> 搜索服务初始化失败时全部返回 500 `{"error":"搜索服务不可用"}`。

### `GET /api/search/engines`
```
200 {"engines":["bing","baidu","duckduckgo"]}
```

### `GET /api/search?q=&engines=&local=&max=` 搜索
参数：
- `q`（必填）关键词。
- `engines`：逗号分隔引擎名，空 = 全部。
- `local`：`1`（默认，联网+本地）、`0`（仅联网）、`only`（仅本地库）。
- `max`：1~30，默认 10。
```
200 {"q":"Go语言","results":[
  {"engine":"bing","title":"Go 语言 教程 | 菜鸟教程","url":"https://…","snippet":"…","local":false}
]}
```
- `local=true` 的结果会并入本地库命中（`local:true`）。
- 25s 超时；失败 → 400 `{"error":...}`。

### `POST /api/search/fetch` 网页预览
```json
{ "url": "https://…" }
```
```
200 {"url":"…","title":"…","text":"…（≤30000 字，超出加截断提示）"}
502 {"error":"抓取失败..."}
```

### `POST /api/search/save` 收藏收录
```json
{ "url": "https://…" }
```
```
200 {"ok":true,"url":"…","title":"…"}
502 {"error":"..."}
```
- 抓取全文并写入本地索引库。

### `GET /api/search/catalog?q=` 本地库列表
```
200 {"docs":[{"url":"…","title":"…","fetched_at":1788579179,"len":4024}],"total":1}
```
- `len` 是正文 rune 数（约等于 KB 级大小）。

### `POST /api/search/delete` 删除收录
```json
{ "url": "https://…" }
```
```
200 {"ok":true,"url":"…"}
400 {"error":"..."}
```

### `GET /api/search/stats` 统计
```
200 {"docs":0,"terms":0,"text_bytes":0,"engines":["bing","baidu","duckduckgo"],"enabled":["bing","baidu","duckduckgo"]}
```
- `enabled` = 当前启用的引擎集合。

## 5. 代码审计（`cmd/audit.go`，`internal/audit`）

### `GET /api/audit/status`
```
200 {"enabled":true,"running":false,"latest":"task-…","summary":{...}|null,"scan_dirs":["."],"exclude":[...]}
```

### `POST /api/audit/start`
```json
{ "scan_dirs": [".","cmd"], "exclude": [] }
```
```
202 {"task_id":"…"}
403 {"error":"审计未启用"}     // settings.audit_enabled=false
409 {"error":"审计已在运行"}   // 已有一个任务
```

### `GET /api/audit/result?task_id=`（空 = 最近一次）
Report JSON：
```jsonc
{
  "task_id":"…","root":"D:\\code\\licode","status":"done","progress":100,
  "scanned_files":120,"issues":[
    {"id":"…","file":"cmd/serve.go","line":42,"severity":"high",
     "category":"…","description":"…","suggestion":"…"}
  ],
  "created_at":"…","finished_at":"…","error":"","static_hits":N,"llm_hits":N,
  "static_files":N,"llm_files":N
}
```
- severity：`critical|high|medium|low`。
- `progress` 0-100（运行中）。

### `POST /api/audit/fix` 一键修复
第一步（预览）：
```json
{ "task_id":"…", "issue_ids":["id1","id2"] }
```
```
200 {"preview":{"path":"abs\\file.go":"+…\n-…\n"},"files":["path1","path2"]}
```
第二步（确认）——带 `?confirm=true`：
```
200 {"applied":true,"files":["…"],"backed_up":2,"backup_path":"…","patch":"…"}
```
- 修复前给每个文件写 `<原文件>.bak` 备份。
- 修复完成后**后端向所有 WS 客户端广播 `audit_log` 事件**（content 是 JSON 字符串，见 WS 协议）。

## 6. 模型列表

### `GET /api/models?type=&base=&provider=`（20s 超时）
- 使用**当前激活厂商的 api_key**（来自 settings 快照）；query 仅覆盖 `type`/`base_url`/`provider`。
- `type` 支持 `openai`（含 Ollama 兼容）、`google`、`claude`（无列表接口返回空）、以及 `ollama`/`gemini`（会被归一）。
- 各厂商真实行为：openai → `GET {base}/models`；google → `GET {base}/v1beta/models?key=`（仅保留含 gemini 的名字）；claude → 空。
```
200 {"provider":"openai","type":"openai","models":["gpt-4o-mini",...]}
502 {"error":"..."}
```
- **前端注意**：给「新厂商」取模型前必须先把该厂商设为激活（`settings_set`），否则用的是旧激活厂商的 key（见 06）。

## 7. 其他（旧界面 HTMX 片段，新前端不使用）

| 路径 | 说明 |
| --- | --- |
| `GET /fragment/settings` | 设置弹窗 HTML |
| `GET /fragment/files?path=` | 文件树 HTML |
| `GET /fragment/audit?sev=` | 审计面板 HTML（运行中每 1.5s 自刷） |
| `POST /fragment/audit/start` | 启动审计返回面板 |

## 8. 通用约定
- 错误一律 `{"error":"中文说明"}` + 4xx/5xx。
- 未登录：带 `Accept: application/json` → 401 纯文本「401 未登录」；否则 302 → `/login`。前端 `useApi` 始终带 `Accept: application/json`。
- `GET /`：需要登录时返回 index.html（旧界面）。
