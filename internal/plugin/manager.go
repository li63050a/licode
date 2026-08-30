package plugin

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Dirs 返回插件加载目录（项目内与用户级）。
func Dirs() []string {
	dirs := []string{".licode/plugins", ".opencode/plugins"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".licode", "plugins"))
	}
	return dirs
}

// Manager 管理插件目录，支持运行时热加载。
type Manager struct {
	mu      sync.RWMutex
	plugins map[string]*Plugin
	dirs    []string
	host    *HostAPI
}

// Default 是全局插件管理器单例。
var Default = &Manager{
	plugins: map[string]*Plugin{},
	host:    &HostAPI{Logf: log.Printf},
}

// SetDirs 设置插件加载目录（.licode/plugins 等）。
func (m *Manager) SetDirs(dirs ...string) {
	m.mu.Lock()
	m.dirs = append([]string{}, dirs...)
	m.mu.Unlock()
}

// Start 加载现有插件并启动目录监听（热更新）。
func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	dirs := append([]string{}, m.dirs...)
	m.mu.RUnlock()
	for _, dir := range dirs {
		m.scan(dir)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[plugin] 监听失败: %v", err)
		return
	}
	for _, dir := range dirs {
		_ = os.MkdirAll(dir, 0o755)
		_ = watcher.Add(dir)
	}
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				// 仅关心 .wasm/.json
				if !strings.HasSuffix(ev.Name, ".wasm") && !strings.HasSuffix(ev.Name, ".json") {
					continue
				}
				// 防抖：等待写盘完成
				time.Sleep(120 * time.Millisecond)
				m.scan(filepath.Dir(ev.Name))
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
}

// scan 扫描目录：加载新增 .wasm，卸载被删除的插件。
func (m *Manager) scan(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".wasm") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		seen[name] = true
		m.mu.RLock()
		_, exists := m.plugins[name]
		m.mu.RUnlock()
		if exists {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		p, err := Load(ctx, path, m.host)
		cancel()
		if err != nil {
			log.Printf("[plugin] 加载 %s 失败: %v", name, err)
			continue
		}
		m.mu.Lock()
		m.plugins[name] = p
		m.mu.Unlock()
		log.Printf("[plugin] 已加载: %s", name)
	}
	// 卸载已删除的
	m.mu.Lock()
	for name, p := range m.plugins {
		if !seen[name] {
			delete(m.plugins, name)
			go p.Close(context.Background())
			log.Printf("[plugin] 已卸载: %s", name)
		}
	}
	m.mu.Unlock()
}

// Plugins 返回当前已加载插件。
func (m *Manager) Plugins() []*Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		out = append(out, p)
	}
	return out
}

// CloseAll 卸载全部插件。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, p := range m.plugins {
		_ = p.Close(context.Background())
		delete(m.plugins, name)
	}
}
