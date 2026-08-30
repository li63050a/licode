// Package plugin 实现基于 WebAssembly（wazero）的插件系统，支持运行时热加载。
//
// 两种插件形态：
//
//  1. reactor 持久模式（allocate/execute 导出）：
//     插件导出 allocate(size i32) i32 与 execute(argsPtr, argsSize i32) i64，
//     宿主调用前先调用导出的 _initialize 初始化运行时。适合 TinyGo /
//     WASI reactor 编译的插件（低延迟，进程内常驻）。
//
//  2. CLI 每调用模式（标准 Go）：
//     插件是一个普通 Go 程序（GOOS=wasip1 GOARCH=wasm），main 里用
//     os.Args[1] 读取 JSON 参数，把 JSON 结果写到 stdout。宿主每次调用
//     都以 argv=[插件名, 参数JSON]、stdout 收集器实例化并运行 _start。
//     对标准 Go 最可靠（无需 TinyGo）。
//
// 宿主注入模块 "env"（供 reactor 插件使用）：
//
//	log(ptr, size i32)
//	http_get(urlPtr, urlSize, bufPtr i32) i32
//	file_read(pathPtr, pathSize, bufPtr i32) i32
//
// 插件同目录可放 plugin.json：{name, description, schema}
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	wasi "github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// maxBuffer 宿主写入插件内存的最大字节数（http_get/file_read 用）。
const maxBuffer = 1 << 20

// Manifest 是插件清单（与 .wasm 同目录的 plugin.json）。
type Manifest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// HostAPI 供宿主函数使用（日志等）。
type HostAPI struct {
	Logf func(format string, args ...any)
}

// Plugin 是一个已加载的 wasm 插件实例。
type Plugin struct {
	Name        string
	Description string
	Schema      map[string]any

	mode string // reactor | cli
	path string

	rt       wazero.Runtime
	mod      api.Module            // reactor 模式常驻实例
	compiled wazero.CompiledModule // cli 模式复用编译产物
	host     *HostAPI

	mu sync.Mutex
}

func (p *Plugin) String() string { return p.Name }

// Call 把 argsJSON 传给插件，返回 JSON 结果。
func (p *Plugin) Call(ctx context.Context, argsJSON string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.mode {
	case "reactor":
		return p.callReactor(ctx, argsJSON)
	default:
		return p.callCLI(ctx, argsJSON)
	}
}

// callReactor 持久模式：allocate -> 写参 -> execute -> 读结果。
func (p *Plugin) callReactor(ctx context.Context, argsJSON string) (string, error) {
	mem := p.mod.Memory()
	res, err := p.mod.ExportedFunction("allocate").Call(ctx, uint64(len(argsJSON)))
	if err != nil {
		return "", fmt.Errorf("plugin %s allocate: %w", p.Name, err)
	}
	ptr := uint32(res[0])
	if !mem.Write(ptr, []byte(argsJSON)) {
		return "", fmt.Errorf("plugin %s: 写入参数越界", p.Name)
	}
	out, err := p.mod.ExportedFunction("execute").Call(ctx, uint64(ptr), uint64(len(argsJSON)))
	if err != nil {
		return "", fmt.Errorf("plugin %s execute: %w", p.Name, err)
	}
	packed := out[0]
	resPtr := uint32(packed >> 32)
	resSize := uint32(packed & 0xffffffff)
	if resSize == 0 || resSize > maxBuffer {
		return "", fmt.Errorf("plugin %s: 非法结果长度 %d", p.Name, resSize)
	}
	data, ok := mem.Read(resPtr, resSize)
	if !ok {
		return "", fmt.Errorf("plugin %s: 读取结果越界", p.Name)
	}
	return string(data), nil
}

// callCLI 每调用模式：argv 传参，stdout 返回。
func (p *Plugin) callCLI(ctx context.Context, argsJSON string) (string, error) {
	var stdout bytes.Buffer
	cfg := wazero.NewModuleConfig().
		WithName(p.Name).
		WithArgs(p.Name, argsJSON).
		WithStdout(&stdout).
		WithStderr(os.Stderr).
		WithFS(os.DirFS(".")) // 只读访问服务器工作目录（沙箱）
	mod, err := p.rt.InstantiateModule(ctx, p.compiled, cfg)
	if err != nil {
		return "", fmt.Errorf("plugin %s 实例化: %w", p.Name, err)
	}
	defer mod.Close(ctx)
	if f := mod.ExportedFunction("_start"); f != nil {
		// 运行 main；Go 运行时结束后模块以 exit_code(0) 关闭，属正常，忽略。
		_, _ = f.Call(ctx)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Close 释放插件资源。
func (p *Plugin) Close(ctx context.Context) error {
	if p.mod != nil {
		_ = p.mod.Close(ctx)
	}
	if p.rt != nil {
		return p.rt.Close(ctx)
	}
	return nil
}

// Load 读取 .wasm 与同目录 plugin.json，实例化插件。
func Load(ctx context.Context, wasmPath string, host *HostAPI) (*Plugin, error) {
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, err
	}
	manifest, err := loadManifest(wasmPath)
	if err != nil {
		return nil, err
	}
	name := manifest.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	}

	rt := wazero.NewRuntime(ctx)
	if _, err := wasi.NewBuilder(rt).Instantiate(ctx); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasi: %w", err)
	}
	if _, err := newHostModule(rt, host); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("编译插件 %s: %w", name, err)
	}

	p := &Plugin{
		Name: name, Description: manifest.Description, Schema: manifest.Schema,
		path: wasmPath, rt: rt, compiled: compiled, host: host,
	}
	if err := p.initMode(ctx); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	return p, nil
}

// initMode 探测插件形态：有 _initialize 且导出 allocate/execute → reactor。
func (p *Plugin) initMode(ctx context.Context) error {
	exports := p.compiled.ExportedFunctions()
	if _, hasInit := exports["_initialize"]; hasInit {
		if _, hasAlloc := exports["allocate"]; hasAlloc {
			if _, hasExec := exports["execute"]; hasExec {
				mod, err := p.rt.InstantiateModule(ctx, p.compiled,
					wazero.NewModuleConfig().WithName(p.Name).WithStartFunctions())
				if err != nil {
					return fmt.Errorf("插件 %s 实例化: %w", p.Name, err)
				}
				// 初始化运行时（reactor）
				if f := mod.ExportedFunction("_initialize"); f != nil {
					if _, err := f.Call(ctx); err != nil {
						_ = mod.Close(ctx)
						return fmt.Errorf("插件 %s 初始化失败: %w", p.Name, err)
					}
				}
				p.mod = mod
				p.mode = "reactor"
				return nil
			}
		}
	}
	// CLI 模式：标准 Go 程序，argv/stdout 通信
	p.mode = "cli"
	return nil
}

func loadManifest(wasmPath string) (Manifest, error) {
	dir := filepath.Dir(wasmPath)
	base := strings.TrimSuffix(filepath.Base(wasmPath), filepath.Ext(wasmPath))
	var m Manifest
	for _, cand := range []string{base + ".json", filepath.Join(dir, "plugin.json")} {
		data, err := os.ReadFile(cand)
		if err != nil {
			continue
		}
		if json.Unmarshal(data, &m) == nil {
			return m, nil
		}
	}
	return m, nil
}

// newHostModule 构建宿主模块 env，注入 log/http_get/file_read。
func newHostModule(rt wazero.Runtime, host *HostAPI) (api.Module, error) {
	logf := host.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	b := rt.NewHostModuleBuilder("env")
	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) {
		if data, ok := mod.Memory().Read(ptr, size); ok {
			logf("[plugin] %s", string(data))
		}
	}).Export("log")
	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, urlPtr, urlSize, bufPtr uint32) uint32 {
		url, ok := mod.Memory().Read(urlPtr, urlSize)
		if !ok {
			return 0
		}
		body, err := httpGet(ctx, string(url))
		if err != nil {
			logf("[plugin] http_get: %v", err)
			return 0
		}
		n := uint32(len(body))
		if n > maxBuffer {
			n = maxBuffer
		}
		if !mod.Memory().Write(bufPtr, body[:n]) {
			return 0
		}
		return n
	}).Export("http_get")
	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, pathPtr, pathSize, bufPtr uint32) uint32 {
		path, ok := mod.Memory().Read(pathPtr, pathSize)
		if !ok {
			return 0
		}
		pathStr := string(path)
		if strings.Contains(pathStr, "..") {
			return 0
		}
		abs, err := filepath.Abs(pathStr)
		if err != nil {
			return 0
		}
		clean := filepath.Clean(abs)
		wd, err := os.Getwd()
		if err == nil {
			cleanWD := filepath.Clean(wd)
			rel, err := filepath.Rel(cleanWD, clean)
			if err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
				data, err := os.ReadFile(clean)
				if err != nil {
					return 0
				}
				n := uint32(len(data))
				if n > maxBuffer {
					n = maxBuffer
				}
				if !mod.Memory().Write(bufPtr, data[:n]) {
					return 0
				}
				return n
			}
		}
		return 0
	}).Export("file_read")
	return b.Instantiate(context.Background())
}

// 带超时的简单 HTTP GET（插件宿主能力）。
func httpGet(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := newGetRequest(reqCtx, url)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readAllLimit(resp.Body, maxBuffer)
}
