package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// CommandToolDef 描述一个通过外部命令实现的工具（~/.licode/tools/*.json）。
// 工具执行时会把 JSON 参数对象作为 stdin 传给 command，输出文本即工具结果。
type CommandToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      map[string]any  `json:"schema"`
	Command     string          `json:"command"`     // 可执行程序或带参数的模板命令
	TimeoutSec  int             `json:"timeout_sec"` // 单次执行超时（默认 60s）
	Env         []string        `json:"env"`         // 额外环境变量 K=V
	ArgsMode    string          `json:"args_mode"`   // "stdin"（默认）或 "env"（子进程）
	raw         json.RawMessage `json:"-"`
}

// ExternalTools 是跨 Agent 的共享注册表，保存从 ~/.licode/tools/ 热加载的
// 外部命令工具。每次 BuildAgent 都会把其中的工具合并进新 Agent。
var ExternalTools = NewRegistry()

// StartExternalToolWatcher 启动（或复用）对 tools 目录的外部工具热加载监视。
// 返回在服务关闭时调用 Close 的清理函数。
func StartExternalToolWatcher(dir string) (func(), error) {
	w, err := StartToolWatcher(dir, ExternalTools, true)
	if err != nil {
		return func() {}, err
	}
	return w.Close, nil
}

// ToolWatcher 用 fsnotify 监视工具目录，新增/修改/删除 *.json 文件时动态
// 注册/卸载工具，实现“热加载”。
type ToolWatcher struct {
	dir    string
	reg    *Registry
	mu     sync.Mutex
	loaded map[string]bool
	watch  *fsnotify.Watcher
	stop   chan struct{}
	once   sync.Once
}

// StartToolWatcher 启动对 dir 目录的工具热加载。reg 为接收工具的目标注册表，
// initNow 为 true 时先加载目录中已有的定义（重建 agent 时使用）。
func StartToolWatcher(dir string, reg *Registry, initNow bool) (*ToolWatcher, error) {
	_ = os.MkdirAll(dir, 0o755)
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	t := &ToolWatcher{
		dir:    dir,
		reg:    reg,
		loaded: map[string]bool{},
		watch:  w,
		stop:   make(chan struct{}),
	}
	if initNow {
		t.loadAll()
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, err
	}
	go t.run()
	return t, nil
}

func (t *ToolWatcher) run() {
	for {
		select {
		case <-t.stop:
			return
		case ev, ok := <-t.watch.Events:
			if !ok {
				return
			}
			if strings.HasSuffix(ev.Name, ".json") {
				// 简单去抖：等待文件写完
				time.Sleep(80 * time.Millisecond)
				t.handle(ev.Name)
			}
		case _, ok := <-t.watch.Errors:
			if !ok {
				return
			}
		}
	}
}

func (t *ToolWatcher) handle(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// 文件被删除 → 卸载以该文件定义的工具
		def, err := readCommandToolDef(path)
		if err == nil && def.Name != "" {
			t.reg.Unregister(def.Name)
		}
		return
	}
	def, err := readCommandToolDef(path)
	if err != nil {
		return
	}
	if def.Name == "" || def.Command == "" {
		return
	}
	t.reg.Register(toolFromCommand(def))
}

// loadAll 加载目录中当前所有工具定义。
func (t *ToolWatcher) loadAll() {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t.handle(filepath.Join(t.dir, e.Name()))
	}
}

// Close 停止监视并释放资源。
func (t *ToolWatcher) Close() {
	t.once.Do(func() {
		close(t.stop)
		_ = t.watch.Close()
	})
}

func readCommandToolDef(path string) (*CommandToolDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var def CommandToolDef
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}
	def.raw = data
	return &def, nil
}

// toolFromCommand 把一个外部命令工具定义转为可被 LLM 调用的 Tool。
func toolFromCommand(def *CommandToolDef) Tool {
	name := def.Name
	return Tool{
		Name:        name,
		Description: def.Description,
		Schema:      def.Schema,
		Run: func(ctx context.Context, args map[string]any) (string, error) {
			argsJSON, err := json.Marshal(args)
			if err != nil {
				return "", err
			}
			timeout := def.TimeoutSec
			if timeout <= 0 {
				timeout = 60
			}
			cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, "/bin/sh", "-c", def.Command)
			cmd.Env = append(os.Environ(), def.Env...)
			if def.ArgsMode == "env" {
				cmd.Env = append(cmd.Env, "TOOL_ARGS="+string(argsJSON))
			} else {
				cmd.Stdin = bytes.NewReader(argsJSON)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), err
			}
			return string(out), nil
		},
	}
}
