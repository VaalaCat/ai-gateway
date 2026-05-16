package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
)

type FetchModelsParams struct {
	BaseURL   string `json:"base_url"`
	Key       string `json:"key"`
	Type      int    `json:"type"`
	Endpoints string `json:"endpoints"`
	ProxyURL  string `json:"proxy_url"`
}

type FetchModelsResult struct {
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

func HandleFetchModels(_ context.Context, params json.RawMessage) (any, error) {
	var p FetchModelsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	baseURL := p.BaseURL
	if baseURL == "" {
		if p.Type > 0 && p.Type < len(newAPIConstant.ChannelBaseURLs) {
			baseURL = newAPIConstant.ChannelBaseURLs[p.Type]
		}
		if baseURL == "" {
			return &FetchModelsResult{Models: []string{}, Error: "该渠道类型必须填写 URL"}, nil
		}
	}

	// Provider-specific model fetching
	switch p.Type {
	case newAPIConstant.ChannelTypeGemini:
		models, err := gemini.FetchGeminiModels(baseURL, p.Key, p.ProxyURL)
		if err != nil {
			return &FetchModelsResult{Models: []string{}, Error: fmt.Sprintf("fetch gemini models failed: %v", err)}, nil
		}
		return &FetchModelsResult{Models: models}, nil
	case newAPIConstant.ChannelTypeOllama:
		ollamaModels, err := ollama.FetchOllamaModels(baseURL, p.Key)
		if err != nil {
			return &FetchModelsResult{Models: []string{}, Error: fmt.Sprintf("fetch ollama models failed: %v", err)}, nil
		}
		models := make([]string, 0, len(ollamaModels))
		for _, m := range ollamaModels {
			models = append(models, m.Name)
		}
		return &FetchModelsResult{Models: models}, nil
	}

	client := httputil.NewClient(p.ProxyURL, 15*time.Second)

	modelsPath := "/v1/models"
	if p.Endpoints != "" {
		var eps map[string]string
		if err := json.Unmarshal([]byte(p.Endpoints), &eps); err == nil {
			if ep, ok := eps["models"]; ok && ep != "" {
				modelsPath = ep
			}
		}
	}

	modelsURL := strings.TrimRight(baseURL, "/") + modelsPath
	httpReq, _ := http.NewRequest("GET", modelsURL, nil)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+p.Key)

	resp, err := client.Do(httpReq)
	if err != nil {
		return &FetchModelsResult{Models: []string{}, Error: fmt.Sprintf("connection failed: %v", err)}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return &FetchModelsResult{Models: []string{}, Error: fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body))}, nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return &FetchModelsResult{Models: []string{}, Error: "failed to parse response"}, nil
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return &FetchModelsResult{Models: models}, nil
}
