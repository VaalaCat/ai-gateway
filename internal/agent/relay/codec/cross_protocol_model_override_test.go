package codec_test

import (
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// TestNegotiateOutboundProtocol_ModelOverride_Matrix 验证：在 codec 层 NegotiateOutboundProtocol
// 视角看，无论 override 是 channel-level 还是 model-level（模型级在 relay 层 resolve 后传入），
// 行为都应该一致 —— 因为 codec 包不感知模型，只看到一份 map[Protocol]Protocol。
//
// 9 组合（3 inbound × 3 outbound）：每个 override 命中即应短路返回该目标 outbound。
func TestNegotiateOutboundProtocol_ModelOverride_Matrix(t *testing.T) {
	// 注：endpoints 形态与 cross_protocol_override_test.go 一致 —— JSON 格式
	endpointsAll := `{"chat_completions":"/v1/chat/completions","responses":"/v1/responses","messages":"/v1/messages"}`

	protocols := []codec.Protocol{
		codec.ProtocolOpenAIChat,
		codec.ProtocolOpenAIResponses,
		codec.ProtocolClaude,
	}

	for _, in := range protocols {
		for _, out := range protocols {
			in, out := in, out
			t.Run(fmt.Sprintf("%s_to_%s", in, out), func(t *testing.T) {
				// upstream.ResolveOverride 在 relay 层把模型级规则展开成这种 flat map；
				// codec 层只看到这层 — 行为应与 channel-level override 一致。
				override := map[codec.Protocol]codec.Protocol{in: out}
				got := codec.NegotiateOutboundProtocol(
					in,
					/*channelType=*/ 0,
					/*supportedAPITypes=*/ "",
					endpointsAll,
					override,
				)
				if got != out {
					t.Fatalf("inbound=%s, override=%v → outbound=%s, want %s", in, override, got, out)
				}
			})
		}
	}
}
