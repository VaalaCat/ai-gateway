package llmkit_test

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

// 一轮工具调用一个原生签名都没写时，开了 gemini 兜底才给第一个调用补占位签名。
func TestToolSignatureFallback(t *testing.T) {
	for _, tt := range []struct {
		name, signature, messageSignature string
		fallback                          llmkit.ToolSignatureFallback
		wantFallback, splitCalls          bool
	}{
		{name: "off", fallback: llmkit.ToolSignatureFallbackNone},
		{name: "unsigned parallel calls", fallback: llmkit.ToolSignatureFallbackGemini, wantFallback: true},
		{name: "real Google signature on second call", signature: `,"extra_content":{"google":{"thought_signature":"real-google-signature"}}`, fallback: llmkit.ToolSignatureFallbackGemini},
		{name: "real tool signature", signature: `,"signature":"real-tool-signature"`, fallback: llmkit.ToolSignatureFallbackGemini},
		{name: "real message signature", messageSignature: `,"signature":"real-message-signature"`, fallback: llmkit.ToolSignatureFallbackGemini},
		{name: "split unsigned calls", fallback: llmkit.ToolSignatureFallbackGemini, wantFallback: true, splitCalls: true},
		{name: "split signed calls", signature: `,"extra_content":{"google":{"thought_signature":"real-google-signature"}}`, fallback: llmkit.ToolSignatureFallbackGemini, splitCalls: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// 1. 使用者自带的签名在 ProviderData 里没有模型记录，发给 Chat 上游时算作原生签名。
			body := `{"model":"gemini-4-argon","messages":[{"role":"assistant"` + tt.messageSignature + `,"tool_calls":[{"id":"first","type":"function","function":{"name":"read","arguments":"{}"}},{"id":"second","type":"function","function":{"name":"shell","arguments":"{}"}` + tt.signature + `}]},{"role":"tool","tool_call_id":"first","content":"ok"},{"role":"tool","tool_call_id":"second","content":"ok"}]}`
			codec := llmkit.NewCodec()
			decoded, err := codec.DecodeRequest(llmkit.DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: []byte(body)})
			require.NoError(t, err)
			if tt.splitCalls {
				assistant := decoded.Request.Messages[0]
				second := assistant
				second.ToolCalls = assistant.ToolCalls[1:]
				assistant.ToolCalls = assistant.ToolCalls[:1]
				decoded.Request.Messages = append([]llmkit.Message{assistant, second}, decoded.Request.Messages[1:]...)
			}
			original, err := json.Marshal(decoded.Request)
			require.NoError(t, err)

			// 2. 编码不改调用方的请求，兜底只写进上游请求体。
			options := llmkit.ConversionOptions{ToolSignatureFallback: tt.fallback}
			target := llmkit.Target{Protocol: llmkit.ProtocolOpenAIChat, Model: decoded.Request.Model}
			encoded, err := codec.EncodeRequest(llmkit.EncodeRequestInput{Request: decoded.Request, Target: target, Options: options})
			require.NoError(t, err)
			unchanged, err := json.Marshal(decoded.Request)
			require.NoError(t, err)
			require.Equal(t, original, unchanged)

			// 3. 核对 ID、结果对应关系和占位签名只出现在第一个调用上。
			var result struct {
				Messages []struct {
					Signature  string
					ToolCallID string `json:"tool_call_id"`
					ToolCalls  []struct {
						ID           string
						Signature    string
						ExtraContent struct {
							Google struct {
								ThoughtSignature string `json:"thought_signature"`
							}
						} `json:"extra_content"`
					} `json:"tool_calls"`
				}
			}
			require.NoError(t, json.Unmarshal(encoded.Body, &result))
			calls := result.Messages[0].ToolCalls
			if tt.messageSignature != "" {
				require.Equal(t, "real-message-signature", result.Messages[0].Signature)
			}
			require.Len(t, calls, 2)
			require.Equal(t, "first", calls[0].ID)
			require.Equal(t, "second", calls[1].ID)
			require.Equal(t, calls[0].ID, result.Messages[1].ToolCallID)
			require.Equal(t, calls[1].ID, result.Messages[2].ToolCallID)
			switch {
			case tt.wantFallback:
				require.Equal(t, "context_engineering_is_the_way_to_go", calls[0].ExtraContent.Google.ThoughtSignature)
				require.Empty(t, calls[1].ExtraContent.Google.ThoughtSignature)
			case tt.signature != "":
				require.Empty(t, calls[0].ExtraContent.Google.ThoughtSignature)
				require.True(t, calls[1].ExtraContent.Google.ThoughtSignature == "real-google-signature" || calls[1].Signature == "real-tool-signature")
			default:
				require.Empty(t, calls[0].ExtraContent.Google.ThoughtSignature)
			}

			// 4. 同一个请求再编码一次，结果相同。
			again, err := codec.EncodeRequest(llmkit.EncodeRequestInput{Request: decoded.Request, Target: target, Options: options})
			require.NoError(t, err)
			require.JSONEq(t, string(encoded.Body), string(again.Body))
		})
	}
}
