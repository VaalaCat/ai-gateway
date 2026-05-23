package private_channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"go.uber.org/zap"
)

// PortalTestRequest 是 PortalTest 的请求体。两个字段都可选——空时沿用
// pickTestModelForPrivate / testPathForType(pc.Type) 默认值，保持与旧调用
// （仅 path 参数无 body）向后兼容。
//
// 校验：传入 Model 必须在 pc.Models 白名单内（防止 SSRF 风格滥用，详见
// spec §2.3）；传入 EndpointType 限定枚举 chat_completions / responses /
// messages。
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
//   - endpoint_type：用户选定的 protocol；空时走 testPathForType(pc.Type)。
//     传入限定为 chat_completions / responses / messages 三个枚举，路径取自
//     pc.Endpoints JSON（若已配）或固定 fallback 路径。
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
	path, err := resolveTestPath(pc, req.EndpointType)
	if err != nil {
		return TestResult{}, api.BadRequestError(err.Error(), nil)
	}
	endpoint := baseURL + path

	body := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`)
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

// testPathForType returns the upstream test endpoint path for a given provider type.
// Anthropic uses /v1/messages; OpenAI / Azure / unknown types fall back to
// /v1/chat/completions. Without this routing, PortalTest hardcoded the chat-completions
// path for every provider, which would 404 against Anthropic endpoints (§4.8).
func testPathForType(t int) string {
	switch t {
	case newAPIConstant.ChannelTypeAnthropic:
		return "/v1/messages"
	case newAPIConstant.ChannelTypeOpenAI, newAPIConstant.ChannelTypeAzure:
		return "/v1/chat/completions"
	default:
		return "/v1/chat/completions"
	}
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

// resolveTestPath 根据 endpointType 选择测试用的上游 path。
//   - 空 endpointType：走 testPathForType(pc.Type) 旧默认逻辑
//   - 非法 endpointType：返回错误
//   - 合法 endpointType（chat_completions / responses / messages）：优先用
//     pc.Endpoints JSON 中该 protocol 对应的 path；JSON 没配或解析失败时
//     fallback 到固定路径
func resolveTestPath(pc *models.PrivateChannel, endpointType string) (string, error) {
	if endpointType == "" {
		return testPathForType(pc.Type), nil
	}
	switch endpointType {
	case "chat_completions", "responses", "messages":
		// 合法
	default:
		return "", fmt.Errorf("invalid endpoint_type: %s", endpointType)
	}
	// 尝试从 pc.Endpoints JSON 取该 protocol 的 path
	if pc.Endpoints != "" {
		var eps map[string]string
		if err := json.Unmarshal([]byte(pc.Endpoints), &eps); err != nil {
			zap.L().Warn("byok PortalTest: failed to parse pc.Endpoints, falling back to fixed path",
				zap.Uint("channel_id", pc.ID),
				zap.String("endpoint_type", endpointType),
				zap.Error(err))
		} else if path, ok := eps[endpointType]; ok && path != "" {
			return path, nil
		}
	}
	// fallback 固定路径
	switch endpointType {
	case "chat_completions":
		return "/v1/chat/completions", nil
	case "responses":
		return "/v1/responses", nil
	case "messages":
		return "/v1/messages", nil
	}
	return "", fmt.Errorf("unreachable")
}
