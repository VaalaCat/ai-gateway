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
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
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

	relayPath, reqBody, err := codec.BuildConnectivityTestRequest("", "", p.EndpointType, p.Model, p.Stream)
	if err != nil {
		return &ChannelTestResult{Success: false, Error: err.Error(), Model: p.Model}, nil
	}
	bodyBytes, _ := json.Marshal(reqBody)

	client, base := netaddr.SelfClient(listenAddr)
	testURL := base + relayPath

	// Get test token from store
	token := store.GetSystemTestToken()
	if token == nil {
		return &ChannelTestResult{
			Success: false,
			Error:   "no system test token available",
			Model:   p.Model,
		}, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", testURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return &ChannelTestResult{
			Success: false,
			Error:   fmt.Sprintf("failed to build request: %v", err),
			Model:   p.Model,
		}, nil
	}
	httpReq.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+token.Key)
	httpReq.Header.Set(consts.HeaderXChannelID, p.ChannelID)

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

