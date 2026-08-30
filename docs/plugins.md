# licode 插件开发指南（WebAssembly）

licode 的插件系统基于 **WebAssembly + wazero**：纯 Go、无 CGO、沙箱隔离、运行时热加载。插件崩溃不影响主进程；放一个 `.wasm` 到插件目录即可被 AI 调用。

## 1. 插件放哪里

```
~/.licode/plugins/<插件名>/    用户级（每台机器）
.licode/plugins/<插件名>/      项目级
.opencode/plugins/<插件名>/    兼容目录
```

目录结构：

```
~/.licode/plugins/my-plugin/
├── my-plugin.wasm    编译产物
└── my-plugin.json    清单：{name, description, schema}
```

`my-plugin.json`：

```json
{
  "name": "my-plugin",
  "description": "计算两个数相加",
  "schema": {
    "type": "object",
    "properties": {
      "a": {"type": "number"},
      "b": {"type": "number"}
    }
  }
}
```

`name` 会注册成 AI 工具 `plugin_my-plugin`，`description` 告诉 AI 何时调用，`schema` 是参数 JSON Schema。

## 2. 两种插件形态

| 形态 | 适用编译工具 | 特点 |
| --- | --- | --- |
| **CLI 模式（推荐，标准 Go）** | 官方 Go 工具链 `GOOS=wasip1 GOARCH=wasm` | 最简单；每次调用以 `os.Args[1]` 传参、stdout 返回结果；绝对可靠 |
| **reactor 模式** | TinyGo / 导出 `allocate`+`execute`+`_initialize` | 进程内常驻、低延迟；宿主注入 `log/http_get/file_read` |

## 3. CLI 模式插件（标准 Go，10 分钟上手）

编写 `main.go`：

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// licode 把 LLM 的工具调用参数 JSON 作为第 1 个命令行参数传入
	args := ""
	if len(os.Args) > 1 {
		args = os.Args[1]
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(args), &req)

	text, _ := req["text"].(string)

	// 业务逻辑……最终把 JSON 结果写到 stdout
	out, _ := json.Marshal(map[string]any{"echo": text})
	fmt.Print(string(out))
}
```

编译：

```bash
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o my-plugin.wasm .
```

> 注意：主模块 `go.mod` 的 go 版本需 ≥ 1.24（`//go:wasmexport` 依赖）。licode 使用 `//go:wasmexport` 需要 Go 1.24+ 工具链。

拷贝进插件目录即可生效：

```bash
mkdir -p ~/.licode/plugins/my-plugin
cp my-plugin.wasm ~/.licode/plugins/my-plugin/
# 再写 my-plugin.json 清单
```

licode 启动时会加载，运行中放入也能**热加载**（fsnotify 监听，无需重启）。

### 参数与结果约定

- 入参：`os.Args[1]` 是一个 JSON 字符串（LLM 按 `schema` 生成的参数对象）
- 出参：stdout 输出 JSON 字符串（或纯文本），作为工具结果回填给 LLM
- 文件系统：插件以**只读**方式访问 licode 服务器的工作目录（沙箱），绝对路径会被拒绝
- 网络：CLI 模式插件可自行用 `net/http`（WASI sockets 需 wazero 支持）；更推荐用 reactor 模式宿主注入的 `http_get`

## 4. reactor 模式插件（TinyGo / 低延迟）

插件导出：

```go
//export allocate
func allocate(size uint32) uint32      // 分配参数缓冲区，返回指针

//export execute
func execute(argsPtr, argsSize uint32) uint64  // 处理参数，返回 (resultPtr<<32)|resultSize
```

宿主会先调用 `_initialize` 初始化运行时，之后 `allocate → 写入参数 → execute → 读结果`。宿主还向模块 `env` 注入：

```go
//go:wasmimport env log
func log(ptr, size uint32)

//go:wasmimport env http_get
func http_get(urlPtr, urlSize, bufPtr uint32) uint32   // 返回写入 buf 的字节数

//go:wasmimport env file_read
func file_read(pathPtr, pathSize, bufPtr uint32) uint32
```

## 5. 生命周期与热加载

- licode 启动时扫描插件目录；`fsnotify` 监听 `.wasm/.json` 变化
- 新增/修改 → 重新加载；删除 → 卸载
- 每次 LLM 工具调用 → `plugin_<name>` 工具 → 沙箱内执行 → 结果回填

## 6. 调试

- 插件进程在 wazero 沙箱内，`log.Printf("[plugin] ...")` 输出到 licode 日志（`~/.licode/logs/licode.log`）
- CLI 模式：先在本地直接跑 `./my-plugin.wasm '{"text":"hi"}'` 等价于 `go run . '{"text":"hi"}'`（wasm 与主机行为一致），排查逻辑
- reactor 模式：确认导出了 `_initialize`、`allocate`、`execute`

## 7. 更多示例

见 `internal/plugin/plugin_test.go` 中的样例插件（echo / 求和 / 读文件）。