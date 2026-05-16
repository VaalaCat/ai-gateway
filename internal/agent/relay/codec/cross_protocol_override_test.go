package codec_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// TestCrossProtocolOverrideMatrix 验证 3 inbound × 3 override target = 9 组合。
// 在 endpoints 全开的 channel 上，override 命中后短路返回正确目标。
func TestCrossProtocolOverrideMatrix(t *testing.T) {
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
				override := map[codec.Protocol]codec.Protocol{in: out}
				got := codec.NegotiateOutboundProtocol(in, 0, "", endpointsAll, override)
				if got != out {
					t.Errorf("inbound=%s, override target=%s: got %s, want %s", in, out, got, out)
				}
			})
		}
	}
}

// TestCrossProtocolOverrideEncoding 端到端验证 override 命中后 inbound→IR→outbound
// 链路 well-formed。fixture 复用 testdata/golden 里已经过验证的最小 inbound 请求体。
func TestCrossProtocolOverrideEncoding(t *testing.T) {
	chatBody := []byte(`{
		"model": "gpt-5",
		"messages": [{"role":"user","content":"Hello"}]
	}`)
	respBody := []byte(`{
		"model": "gpt-5",
		"input": "Hello"
	}`)
	claudeBody := []byte(`{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 64,
		"messages": [{"role":"user","content":"Hello"}]
	}`)

	cases := []struct {
		inboundProto codec.Protocol
		body         []byte
		path         string
	}{
		{codec.ProtocolOpenAIChat, chatBody, "/v1/chat/completions"},
		{codec.ProtocolOpenAIResponses, respBody, "/v1/responses"},
		{codec.ProtocolClaude, claudeBody, "/v1/messages"},
	}

	outProtos := []codec.Protocol{
		codec.ProtocolOpenAIChat,
		codec.ProtocolOpenAIResponses,
		codec.ProtocolClaude,
	}

	for _, c := range cases {
		for _, out := range outProtos {
			c, out := c, out
			t.Run(fmt.Sprintf("%s_to_%s", c.inboundProto, out), func(t *testing.T) {
				inboundCodec := codec.GetInbound(c.inboundProto)
				if inboundCodec == nil {
					t.Skipf("no inbound codec for %s", c.inboundProto)
				}
				outboundCodec := codec.GetOutbound(out)
				if outboundCodec == nil {
					t.Skipf("no outbound codec for %s", out)
				}

				httpReq, _ := http.NewRequest(http.MethodPost, c.path, bytes.NewReader(c.body))
				httpReq.Header.Set("Content-Type", "application/json")

				ir, err := inboundCodec.DecodeRequest(httpReq)
				if err != nil {
					t.Fatalf("decode (%s): %v", c.inboundProto, err)
				}
				cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: ir.Model}
				outReq, err := outboundCodec.EncodeRequest(ir, cfg)
				if err != nil {
					t.Fatalf("encode (%s -> %s): %v", c.inboundProto, out, err)
				}
				if outReq == nil || outReq.Body == nil {
					t.Fatalf("nil outbound request/body (%s -> %s)", c.inboundProto, out)
				}
				outBody, err := io.ReadAll(outReq.Body)
				if err != nil {
					t.Fatalf("read outbound body (%s -> %s): %v", c.inboundProto, out, err)
				}
				if len(outBody) == 0 {
					t.Fatalf("empty outbound body (%s -> %s)", c.inboundProto, out)
				}
				var parsed any
				if err := json.Unmarshal(outBody, &parsed); err != nil {
					t.Fatalf("invalid json output (%s -> %s): %v\n%s",
						c.inboundProto, out, err, string(outBody))
				}
			})
		}
	}
}
