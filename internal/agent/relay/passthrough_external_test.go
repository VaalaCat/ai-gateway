package relay_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/passthrough"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
)

// TestRelayPassthrough_InvalidRequestBodyJSON_ErrNotPanic 触发
// backend/passthrough/passthrough.go 里 json.Unmarshal(bodyBytes) 失败分支：
// 请求 body 不是合法 JSON → backend.Relay 应该返回 AttemptResult.Err 非 nil，
// dispatcher 把它当作失败处理，不 panic。
//
// 真实业务路径上：ctxBuilder 会把 body 解析过一次再传到 backend，
// 所以走到这里的 body 一般合法。这条测试钉死 backend 自身的防御：
// 万一上游链路改了，也不至于 panic。
//
// 本测试本来在 package relay 内 passthrough_test.go，Task 4 把
// passthroughBackend 拆到 backend/passthrough 子包后，从 package relay 直接 import
// 该子包会形成循环依赖，因此搬到 package relay_test（external test）。
func TestRelayPassthrough_InvalidRequestBodyJSON_ErrNotPanic(t *testing.T) {
	// 上游不会被调用——backend 在 unmarshal 阶段就失败
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when request body is invalid JSON")
		w.WriteHeader(500)
	}))
	defer upstream.Close()

	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"}

	rctx := &state.RelayContext{
		Context: &gin.Context{},
		Input: state.RelayInput{
			Body:         []byte(`not-json-at-all`),
			Model:        "gpt-4o",
			InboundProto: codec.ProtocolOpenAIChat,
			StartTime:    time.Now(),
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	backend := &passthrough.Backend{Agent: nil} // logger/transportPool 走 nil-guard 兜底

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invalid request body 不该 panic，got: %v", r)
		}
	}()

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("invalid JSON body 应该返回 AttemptResult.Err 非 nil")
	}
	if got.Written {
		t.Errorf("Err 分支不该 Written=true，got %+v", got)
	}
}
