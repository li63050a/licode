package plugin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// samplePlugin 是标准 Go 的 CLI 模式插件：os.Args[1] 读参数，stdout 返回结果。
const samplePlugin = `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	args := ""
	if len(os.Args) > 1 {
		args = os.Args[1]
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(args), &req)
	action, _ := req["action"].(string)
	text, _ := req["text"].(string)
	path, _ := req["path"].(string)
	switch action {
	case "file":
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Print("ERR:" + err.Error())
			return
		}
		fmt.Print(string(data))
	case "sum":
		a, _ := req["a"].(float64)
		b, _ := req["b"].(float64)
		out, _ := json.Marshal(map[string]any{"sum": a + b})
		fmt.Print(string(out))
	default:
		out, _ := json.Marshal(map[string]any{"echo": text})
		fmt.Print(string(out))
	}
}
`

func buildWasm(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(samplePlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module plugintest\n\ngo 1.24.0\n"), 0o644)
	out := filepath.Join(t.TempDir(), "plugin.wasm")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build wasm: %v\n%s", err, b)
	}
	return out
}

func loadPlugin(t *testing.T, wasm string) *Plugin {
	t.Helper()
	host := &HostAPI{Logf: t.Logf}
	ctx := context.Background()
	p, err := Load(ctx, wasm, host)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { p.Close(context.Background()) })
	return p
}

func TestPluginLoadAndCall(t *testing.T) {
	p := loadPlugin(t, buildWasm(t, t.TempDir()))
	if p.mode != "cli" {
		t.Errorf("mode = %s", p.mode)
	}
	res, err := p.Call(context.Background(), `{"text":"你好"}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res), &got); err != nil {
		t.Fatalf("result not json: %q (%v)", res, err)
	}
	if got["echo"] != "你好" {
		t.Errorf("echo = %v", got["echo"])
	}
}

func TestPluginArgsSum(t *testing.T) {
	p := loadPlugin(t, buildWasm(t, t.TempDir()))
	res, err := p.Call(context.Background(), `{"action":"sum","a":2,"b":3}`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res), &got); err != nil {
		t.Fatalf("result not json: %q", res)
	}
	if got["sum"] != float64(5) {
		t.Errorf("sum = %v", got["sum"])
	}
}

func TestPluginFileRead(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	f, err := os.CreateTemp(dir, "data.txt")
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(dir, f.Name())
	f.WriteString("文件内容")
	f.Close()
	p := loadPlugin(t, buildWasm(t, t.TempDir()))
	res, err := p.Call(context.Background(), `{"action":"file","path":"`+rel+`"}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !strings.Contains(res, "文件内容") {
		t.Errorf("result = %q", res)
	}
}
