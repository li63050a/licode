# licode 常见问题（FAQ）

## 在 Android / Termux 上运行？

用 `build/api-gateway-linux-arm64`（静态 aarch64），或从发行版下载 linux-arm64 产物：

```bash
chmod +x ./licode && ./licode --host 0.0.0.0 --port 8080
```

然后手机浏览器打开 `http://127.0.0.1:8080`。

## 登录打不开/要登录

- 未设置密码时无需登录（页面顶部有提醒横幅，并提示如何启用）
- 启用：`./licode --password 你的密码` 或 `export LICODE_PASSWORD=...`；默认用户名 `licode`

## 局域网/手机访问不了

默认监听 `127.0.0.1`（仅本机）。用 `--host 0.0.0.0` 后访问 `http://服务器IP:8080`。记得开 HTTPS：`--https`。

## 如何开启 HTTPS

```bash
./licode --https
```

无证书时自动生成自签名证书（`~/.licode/ssl/`）；浏览器会提示不安全，点击继续即可（或导入证书）。

## `./licode --host ...` 报 unknown flag

旧版本需要 `./licode serve`。当前版本无子命令，`./licode --host 0.0.0.0 --port 8080` 直接运行即可。请更新到最新版。

## 切换 AI 厂商

```bash
./licode --provider ollama                     # 本地 Ollama
./licode --provider claude --api-key sk-ant-.. # Anthropic
./licode --provider google --api-key AIza..    # Gemini
```

或在网页端「设置 → 新增厂商」任意添加，一键获取模型列表。

## 系统提示词怎么改

直接编辑 `~/.licode/system-prompt.md`（首次运行自动生成默认内容）；`~/.licode/md/` 下所有 .md 会递归附加为额外提示词。

## 插件怎么开发

见 [插件开发指南](plugins.md)。把编译好的 `.wasm` 放进 `~/.licode/plugins/<名字>/` 即自动热加载。

## 发行版本不是最新的？

发行版（GitHub/Gitee Releases）可能滞后于仓库代码；要最新请 `./build.sh` 自己编译。

## 推送两个仓库

`git push origin main` 会同时推到 GitHub 与 Gitee（origin 配了双 push URL）。GitHub 偶发挂起时可用 token 直推（见开发说明）。