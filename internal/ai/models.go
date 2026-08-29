package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ListModels 获取某厂商可用模型列表（各厂商原生接口）。
//   - openai: GET {base}/models
//   - ollama: GET {base}/api/tags
//   - gemini: GET {base}/v1beta/models?key=...
//   - claude: 无公开列表接口，返回空
func ListModels(ctx context.Context, cfg Config) ([]string, error) {
	if err := cfg.Resolve(); err != nil {
		return nil, err
	}
	switch cfg.Provider {
	case "openai":
		return listOpenAIModels(ctx, cfg)
	case "ollama":
		return listOllamaModels(ctx, cfg)
	case "gemini":
		return listGeminiModels(ctx, cfg)
	case "claude":
		return nil, nil
	}
	return nil, fmt.Errorf("不支持的提供商 %q", cfg.Provider)
}

func httpGetJSON(ctx context.Context, url, apiKey string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	return client.Do(req)
}

func listOpenAIModels(ctx context.Context, cfg Config) ([]string, error) {
	resp, err := httpGetJSON(ctx, strings.TrimRight(cfg.BaseURL, "/")+"/models", cfg.APIKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var names []string
	for _, d := range out.Data {
		names = append(names, d.ID)
	}
	return names, nil
}

func listOllamaModels(ctx context.Context, cfg Config) ([]string, error) {
	resp, err := httpGetJSON(ctx, strings.TrimRight(cfg.BaseURL, "/")+"/api/tags", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

func listGeminiModels(ctx context.Context, cfg Config) ([]string, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	url := base + "/v1beta/models"
	if cfg.APIKey != "" {
		url += "?key=" + cfg.APIKey
	}
	resp, err := httpGetJSON(ctx, url, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("gemini %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range out.Models {
		if strings.Contains(m.Name, "gemini") {
			names = append(names, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	return names, nil
}
