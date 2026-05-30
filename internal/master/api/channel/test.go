package channel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
)

func (h *Handler) Test(c *app.Context, req TestRequest) (TestResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	channelPtr, err := q.Channel().GetByID(uint(id))
	if err != nil {
		return TestResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	channel := *channelPtr

	model := pickTestModel(req.Model, channel)
	if model == "" {
		return TestResponse{}, api.BadRequestError("no model available for testing", nil)
	}

	// Remote agent test via WS RPC
	if req.AgentID != "" && req.AgentID != "embedded" {
		if h.Hub == nil {
			return TestResponse{}, api.InternalError("hub not available", nil)
		}
		if !h.Hub.IsOnline(req.AgentID) {
			return TestResponse{}, api.BadRequestError("agent not connected", nil)
		}
		params := map[string]any{
			"channel_id":    req.ID,
			"model":         model,
			"endpoint_type": req.EndpointType,
			"stream":        req.Stream,
		}
		result, err := h.Hub.Call(req.AgentID, consts.RPCChannelTest, params, 35*time.Second)
		if err != nil {
			return TestResponse{
				Success: false,
				Error:   fmt.Sprintf("agent rpc failed: %v", err),
				Model:   model,
			}, nil
		}
		var resp TestResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return TestResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid agent response: %v", err),
				Model:   model,
			}, nil
		}
		return resp, nil
	}

	// Local test
	daoCtx2 := dao.NewContext(c.App)
	token, err := getOrCreateTestToken(daoCtx2, c.GetBus())
	if err != nil {
		return TestResponse{}, api.InternalError("failed to get test token", err)
	}
	// apply-if-present 语义下 push 不会主动 warm 新 token；
	// channel test 是 admin 主动触发，直接用 SetToken（FullSync 路径）写入 embedded agent cache。
	if h.AgentStore != nil {
		h.AgentStore.SetToken(token)
	}
	tokenKey := token.Key

	relayPath, reqBody, err := codec.BuildConnectivityTestRequest(
		channel.Endpoints, channel.SupportedAPITypes, req.EndpointType, model, req.Stream)
	if err != nil {
		return TestResponse{}, api.BadRequestError(err.Error(), nil)
	}
	bodyBytes, _ := json.Marshal(reqBody)

	client, base := netaddr.SelfClient(h.MasterListen)
	testURL := base + relayPath

	httpReq, err := http.NewRequest("POST", testURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return TestResponse{}, api.InternalError("failed to build relay request", err)
	}
	httpReq.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+tokenKey)
	httpReq.Header.Set(consts.HeaderXChannelID, req.ID)

	start := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(start).Seconds()

	if err != nil {
		return TestResponse{
			Success:  false,
			Error:    fmt.Sprintf("request failed: %v", err),
			TimeCost: elapsed,
			Model:    model,
		}, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	return TestResponse{
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
		StatusCode: resp.StatusCode,
		Response:   string(respBody),
		TimeCost:   elapsed,
		Model:      model,
	}, nil
}

func pickTestModel(requested string, ch models.Channel) string {
	if requested != "" {
		return requested
	}
	if ch.TestModel != "" {
		return ch.TestModel
	}
	for _, p := range strings.Split(ch.Models, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			return p
		}
	}
	return ""
}

func getOrCreateTestToken(daoCtx dao.Context, bus app.EventBus) (*models.Token, error) {
	db := daoCtx.GetDB()
	m := dao.NewAdminMutation(daoCtx)

	var existing models.Token
	if err := db.Where("name = ?", "__system_test__").First(&existing).Error; err == nil {
		return &existing, nil
	}

	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	key := "sk-test-" + hex.EncodeToString(b)

	token := models.Token{
		Name:      "__system_test__",
		Key:       key,
		Status:    1,
		ExpiredAt: -1,
	}
	if err := m.Token().Create(&token); err != nil {
		// Race: another request may have created it concurrently
		if err2 := db.Where("name = ?", "__system_test__").First(&existing).Error; err2 == nil {
			return &existing, nil
		}
		return nil, err
	}
	if bus != nil {
		events.PublishTokenCreate(context.Background(), bus, token)
	}
	return &token, nil
}
