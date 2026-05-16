package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"go.uber.org/zap"
)

type ChannelTestParams struct {
	ChannelID    string `json:"channel_id"`
	Model        string `json:"model"`
	EndpointType string `json:"endpoint_type"`
	Stream       bool   `json:"stream"`
}

type ChannelTestResult struct {
	Success    bool    `json:"success"`
	StatusCode int     `json:"status_code,omitempty"`
	Response   string  `json:"response,omitempty"`
	Error      string  `json:"error,omitempty"`
	TimeCost   float64 `json:"time_cost"`
	Model      string  `json:"model"`
}

func HandleChannelTest(ctx context.Context, params json.RawMessage, store *cache.Store, listenAddr string, logger *zap.Logger) (any, error) {
	var p ChannelTestParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	relayPath, reqBody := buildTestRequest(p.EndpointType, p.Model, p.Stream)
	bodyBytes, _ := json.Marshal(reqBody)

	testURL := fmt.Sprintf("http://localhost%s%s", listenAddr, relayPath)

	// Get test token from store
	token := store.GetSystemTestToken()
	if token == nil {
		return &ChannelTestResult{
			Success: false,
			Error:   "no system test token available",
			Model:   p.Model,
		}, nil
	}

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", testURL, bytes.NewReader(bodyBytes))
	httpReq.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+token.Key)
	httpReq.Header.Set(consts.HeaderXChannelID, p.ChannelID)

	client := &http.Client{Timeout: 30 * time.Second}
	start := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(start).Seconds()

	if err != nil {
		return &ChannelTestResult{
			Success:  false,
			Error:    fmt.Sprintf("request failed: %v", err),
			TimeCost: elapsed,
			Model:    p.Model,
		}, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	return &ChannelTestResult{
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
		StatusCode: resp.StatusCode,
		Response:   string(respBody),
		TimeCost:   elapsed,
		Model:      p.Model,
	}, nil
}

func buildTestRequest(endpointType, model string, stream bool) (string, map[string]any) {
	if endpointType == "" {
		endpointType = "chat-completion"
	}
	var relayPath string
	var reqBody map[string]any
	switch endpointType {
	case "responses":
		relayPath = "/v1/responses"
		reqBody = map[string]any{"model": model, "input": "Say 'ok' and nothing else."}
	case "anthropic":
		relayPath = "/v1/messages"
		reqBody = map[string]any{
			"model": model, "max_tokens": 10,
			"messages": []map[string]string{{"role": "user", "content": "Say 'ok' and nothing else."}},
		}
	default:
		relayPath = "/v1/chat/completions"
		reqBody = map[string]any{
			"model": model, "max_tokens": 10,
			"messages": []map[string]string{{"role": "user", "content": "Say 'ok' and nothing else."}},
		}
	}
	if stream {
		reqBody["stream"] = true
	}
	return relayPath, reqBody
}
