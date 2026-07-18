package channel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
)

func (h *Handler) FetchModels(c *app.Context, req FetchModelsRequest) (FetchModelsResponse, error) {
	baseURL := req.BaseURL
	if baseURL == "" {
		if req.Type > 0 && req.Type < len(newAPIConstant.ChannelBaseURLs) {
			baseURL = newAPIConstant.ChannelBaseURLs[req.Type]
		}
		if baseURL == "" {
			return FetchModelsResponse{Models: []string{}, Error: "该渠道类型必须填写 URL"}, nil
		}
	}

	// Resolve proxy: channel-level > DB setting > config file
	dbProxy := ""
	if setting, found, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Setting().Lookup("proxy_url"); err == nil && found {
		dbProxy = setting.Value
	}
	resolved := httputil.ResolveProxyURL(req.ProxyURL, dbProxy, c.Settings.Master.ProxyURL)

	// Route to remote agent via WS RPC
	if req.AgentID != "" && req.AgentID != "embedded" {
		if h.Hub == nil {
			return FetchModelsResponse{Models: []string{}, Error: "hub not available"}, nil
		}
		if !h.Hub.IsOnline(req.AgentID) {
			return FetchModelsResponse{Models: []string{}, Error: "agent not connected"}, nil
		}
		params := map[string]any{
			"base_url":  baseURL,
			"key":       req.Key,
			"type":      req.Type,
			"endpoints": req.Endpoints,
			"proxy_url": resolved,
		}
		result, err := h.Hub.Call(req.AgentID, consts.RPCChannelFetchModels, params, 20*time.Second)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("agent rpc failed: %v", err)}, nil
		}
		var resp FetchModelsResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("invalid agent response: %v", err)}, nil
		}
		return resp, nil
	}

	// Local execution
	return doFetchModels(req.Type, baseURL, req.Key, req.Endpoints, resolved)
}

// doFetchModels performs the actual HTTP call to fetch models from upstream.
// Shared between master local execution and agent RPC handler.
func doFetchModels(channelType int, baseURL, key, endpoints, proxyURL string) (FetchModelsResponse, error) {
	// Provider-specific model fetching
	switch channelType {
	case newAPIConstant.ChannelTypeGemini:
		models, err := gemini.FetchGeminiModels(baseURL, key, proxyURL)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("fetch gemini models failed: %v", err)}, nil
		}
		return FetchModelsResponse{Models: models}, nil
	case newAPIConstant.ChannelTypeOllama:
		ollamaModels, err := ollama.FetchOllamaModels(baseURL, key)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("fetch ollama models failed: %v", err)}, nil
		}
		models := make([]string, 0, len(ollamaModels))
		for _, m := range ollamaModels {
			models = append(models, m.Name)
		}
		return FetchModelsResponse{Models: models}, nil
	}

	client := httputil.NewClient(proxyURL, 15*time.Second)

	modelsPath := "/v1/models"
	if endpoints != "" {
		var eps map[string]string
		if err := json.Unmarshal([]byte(endpoints), &eps); err == nil {
			if p, ok := eps["models"]; ok && p != "" {
				modelsPath = p
			}
		}
	}
	modelsURL, err := codec.JoinUpstreamURL(baseURL, modelsPath)
	if err != nil {
		return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("build models url: %v", err)}, nil
	}
	httpReq, _ := http.NewRequest("GET", modelsURL, nil)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+key)

	resp, err := client.Do(httpReq)
	if err != nil {
		return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("connection failed: %v", err)}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body))}, nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return FetchModelsResponse{Models: []string{}, Error: "failed to parse response"}, nil
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return FetchModelsResponse{Models: models}, nil
}
