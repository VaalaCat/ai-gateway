package private_channel

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
)

// PortalTestRequest 是 PortalTest 的请求体。两个字段都可选——空时沿用
// pickTestModelForPrivate 默认 model，以及按 pc.Endpoints/SupportedAPITypes 推导
// 主协议作为 endpoint。
//
// 校验：传入 Model 必须在 pc.Models 白名单内（防止 SSRF 风格滥用，详见
// spec §2.3）；传入 EndpointType 接受 chat_completions / responses / messages
// 及别名 anthropic，统一由 codec.ResolveTestEndpoint 处理。
type PortalTestRequest struct {
	api.IDPathRequest
	Model        string `json:"model" form:"model"`
	EndpointType string `json:"endpoint_type" form:"endpoint_type"`
}

type TestResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	LatencyMs  int    `json:"latency_ms"`
	Detail     string `json:"detail,omitempty"`
}

// PortalTest sends a minimal spike request to the channel's BaseURL with the user's key,
// to confirm the key works. Not billed; not logged in usage_log.
//
// 可选 body 字段：
//   - model：用户选定的测试 model；空时走 pickTestModelForPrivate。传入必须
//     在 pc.Models 白名单内，防止任意上游 model 滥用。
//   - endpoint_type：用户选定的 protocol；空时按 pc.Endpoints/SupportedAPITypes
//     推导主协议。接受 chat_completions / responses / messages 及别名 anthropic，
//     统一由 codec.ResolveTestEndpoint 处理。
func (h *Handler) PortalTest(c *app.Context, req PortalTestRequest) (TestResult, error) {
	if c.UserInfo == nil {
		return TestResult{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return TestResult{}, api.NotFoundError("private channel not found")
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	pc, err := q.PrivateChannel().GetByID(uint(id))
	if err != nil || pc == nil || pc.OwnerID != c.UserInfo.UserID {
		return TestResult{}, api.NotFoundError("private channel not found")
	}

	cipher := h.Provider.GetCipher()
	if cipher == nil {
		return TestResult{}, api.InternalError("byok cipher not configured", nil)
	}
	plaintext, err := cipher.Open(pc.KeyCipher, pc.OwnerID)
	if err != nil {
		return TestResult{}, api.InternalError(byokcrypto.SanitizeDecryptErr(err).Error(), nil)
	}

	// 选 model：优先 request 中的 Model（须在白名单），fallback 到内部默认
	model := pickTestModelForPrivate(pc)
	if req.Model != "" {
		if !modelInChannelWhitelist(pc, req.Model) {
			return TestResult{}, api.BadRequestError("model not in channel models", nil)
		}
		model = req.Model
	}

	// 选 endpoint path：优先 request 中的 EndpointType（限定枚举）
	baseURL := strings.TrimRight(pc.BaseURL, "/")
	if baseURL == "" {
		return TestResult{OK: false, Detail: "no base_url configured"}, nil
	}
	// path 与 body 都由 codec.BuildConnectivityTestRequest 统一产出，body 按解析出的
	// protocol 成形（responses → {input}；chat/claude → {messages}），与 public/agent
	// 测试路径同源，避免对 responses 类渠道发出 chat 形态 body。
	path, reqBody, err := codec.BuildConnectivityTestRequest(pc.Endpoints, pc.SupportedAPITypes, req.EndpointType, model, false)
	if err != nil {
		return TestResult{}, api.BadRequestError(err.Error(), nil)
	}
	endpoint, err := codec.JoinUpstreamURL(pc.BaseURL, path)
	if err != nil {
		return TestResult{OK: false, Detail: "invalid endpoint: " + err.Error()}, nil
	}

	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return TestResult{}, api.InternalError("build test request", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+plaintext)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return TestResult{OK: false, Detail: err.Error()}, nil
	}
	defer resp.Body.Close()
	detailBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return TestResult{
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 300,
		StatusCode: resp.StatusCode,
		LatencyMs:  int(time.Since(start).Milliseconds()),
		Detail:     string(detailBytes),
	}, nil
}

// pickTestModelForPrivate prefers TestModel field, falls back to first Models entry,
// falls back to "gpt-3.5-turbo" as a safe default.
func pickTestModelForPrivate(pc *models.PrivateChannel) string {
	if pc.TestModel != "" {
		return pc.TestModel
	}
	if len(pc.Models) > 0 {
		return pc.Models[0]
	}
	return "gpt-3.5-turbo"
}

// modelInChannelWhitelist 校验用户传入的 model 是否在 channel 的 Models 列表
// 白名单内。pc.Models 是用户在编辑表单里显式配置的可用 model 集合，作为防止
// 任意上游 model 调用的边界。
func modelInChannelWhitelist(pc *models.PrivateChannel, model string) bool {
	for _, m := range pc.Models {
		if m == model {
			return true
		}
	}
	return false
}

// resolveTestPath 选择测试用上游 path，统一走 codec 解析器（基于 Endpoints/Protocol，
// 不再用已弃用的 numeric channel type）。空 endpointType 时按渠道 Endpoints 推导主协议。
func resolveTestPath(pc *models.PrivateChannel, endpointType string) (string, error) {
	_, _, path, err := codec.ResolveTestEndpoint(pc.Endpoints, pc.SupportedAPITypes, endpointType)
	if err != nil {
		return "", err
	}
	return path, nil
}
