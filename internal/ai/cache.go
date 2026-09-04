package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cache 是“语义”缓存：对同一模板/模型的雷同问题（规范化后哈希）命中即跳过 LLM
// 调用，直接从磁盘返回上一次的结果。为了正确性，只在“首轮纯文本回答”（无工具调用）
// 时命中，避免把依赖工具结果的回答错误缓存。
//
// 为简化部署，不引入向量数据库：通过归一化（大小写、空白、标点）捕获常见改写。
// 轻量实现，单进程内内存 + 磁盘持久化两层。
type Cache struct {
	dir  string
	ttl  time.Duration
	mu   sync.Mutex
	mem  map[string]cacheEntry
	miss int // 抖动统计（可选）
}

type cacheEntry struct {
	key       string
	content   string
	createdAt time.Time
}

// NewCache 创建指定目录与 TTL 的缓存；目录不存在会自动创建。
func NewCache(dir string, ttlSeconds int) *Cache {
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	c := &Cache{
		dir: dir,
		ttl: time.Duration(ttlSeconds) * time.Second,
		mem: map[string]cacheEntry{},
	}
	_ = os.MkdirAll(dir, 0o755)
	return c
}

// Enabled 报告缓存是否可用。
func (c *Cache) Enabled() bool { return c != nil && c.ttl > 0 }

// Normalize 对用户输入做轻量归一化，用于生成语义等效的键。
// 保留语义：转小写、合并空白、去掉首尾标点与换行。
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(s)
	s = strings.Join(fields, " ")
	s = strings.Trim(s, " .,!?；，。！？\n\t")
	return s
}

// Key 由归一化后的用户提问 + 模型 + 服务商 + 系统提示 + 工具名哈希共同决定。
func (c *Cache) Key(system, prompt, provider, model string, tools []Tool) string {
	var tb strings.Builder
	for _, t := range tools {
		tb.WriteString(t.Function.Name)
		tb.WriteString(",")
	}
	h := sha256.Sum256([]byte(provider + "|" + model + "|" + Normalize(system) + "|" + Normalize(prompt) + "|" + tb.String()))
	return hex.EncodeToString(h[:])
}

// Cacheable 判断该请求是否适合缓存：仅当它是首轮、无任何工具调用历史，
// 且是纯文本问答（无工具定义参与生成）时。
func Cacheable(messages []Message) bool {
	for _, m := range messages {
		// 只要出现过助手工具调用或工具结果，就不可缓存（结果依赖工具执行）
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			return false
		}
		if m.Role == "tool" {
			return false
		}
		if m.Role == "user" && len(m.ToolCalls) > 0 {
			return false
		}
	}
	return true
}

// Get 命中返回缓存内容；未命中返回 ok=false。
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.mem[key]; ok {
		if time.Since(e.createdAt) < c.ttl {
			return e.content, true
		}
		delete(c.mem, key)
	}
	// 磁盘兜底
	content, err := c.readDisk(key)
	if err != nil {
		return "", false
	}
	c.mem[key] = cacheEntry{key: key, content: content, createdAt: time.Now()}
	return content, true
}

// Put 写入缓存（内存 + 磁盘异步落盘）。
func (c *Cache) Put(key, content string) {
	if content == "" {
		return
	}
	c.mu.Lock()
	c.mem[key] = cacheEntry{key: key, content: content, createdAt: time.Now()}
	dir := c.dir
	c.mu.Unlock()
	// 异步落盘，避免阻塞主流程
	go func() {
		path := filepath.Join(dir, key+".json")
		_ = os.MkdirAll(dir, 0o755)
		data, err := json.Marshal(cacheEntry{key: key, content: content, createdAt: time.Now()})
		if err == nil {
			_ = os.WriteFile(path, data, 0o644)
		}
	}()
}

func (c *Cache) readDisk(key string) (string, error) {
	path := filepath.Join(c.dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return "", err
	}
	if time.Since(e.createdAt) >= c.ttl {
		_ = os.Remove(path)
		return "", fmt.Errorf("expired")
	}
	return e.content, nil
}

// cachedClient 包装底层 LLMClient，在命中时直接返回缓存结果。
type cachedClient struct {
	inner LLMClient
	cache *Cache
}

func (c *cachedClient) Provider() string { return c.inner.Provider() }
func (c *cachedClient) Model() string    { return c.inner.Model() }

// CacheDecorator 为客户端套上一层缓存；cache 为 nil 时原样返回。
func CacheDecorator(inner LLMClient, cache *Cache) LLMClient {
	if cache == nil || !cache.Enabled() {
		return inner
	}
	return &cachedClient{inner: inner, cache: cache}
}

func (c *cachedClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	if Cacheable(req.Messages) {
		key := c.cache.Key(req.System, lastUser(req.Messages), c.Provider(), c.Model(), req.Tools)
		if out, ok := c.cache.Get(key); ok {
			return out, nil
		}
		out, err := c.inner.Chat(ctx, req)
		if err == nil {
			c.cache.Put(key, out)
		}
		return out, err
	}
	return c.inner.Chat(ctx, req)
}

func (c *cachedClient) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) error {
	if Cacheable(req.Messages) {
		key := c.cache.Key(req.System, lastUser(req.Messages), c.Provider(), c.Model(), req.Tools)
		if out, ok := c.cache.Get(key); ok {
			if err := onEvent(StreamEvent{Content: out}); err != nil {
				return err
			}
			return onEvent(StreamEvent{Done: true, Usage: &Usage{}})
		}
		var sb strings.Builder
		err := c.inner.ChatStream(ctx, req, func(ev StreamEvent) error {
			if ev.Content != "" {
				sb.WriteString(ev.Content)
			}
			return onEvent(ev)
		})
		if err == nil {
			c.cache.Put(key, sb.String())
		}
		return err
	}
	return c.inner.ChatStream(ctx, req, onEvent)
}

// lastUser 取最后一个 user 消息文本，作为缓存键的一部分。
func lastUser(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
