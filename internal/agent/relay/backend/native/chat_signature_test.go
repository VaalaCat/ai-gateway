package native

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

func TestRelayChatSignatureSurvivesThinkingStrip(t *testing.T) {
	for _, protocol := range []llmkit.Protocol{llmkit.ProtocolOpenAIChat, llmkit.ProtocolOpenAIResponses} {
		for _, toolCall := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/tool=%t", protocol, toolCall), func(t *testing.T) {
				requests := make(chan map[string]any, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					var received map[string]any
					if err != nil || json.Unmarshal(body, &received) != nil {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					requests <- received
					w.Header().Set("Content-Type", "application/json")
					if toolCall {
						_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","signature":"text-signature","tool_calls":[{"id":"","type":"function","function":{"name":"shell","arguments":"{}"},"signature":"tool-signature"}]},"finish_reason":"tool_calls"}]}`)
						return
					}
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"收到","signature":"text-signature"},"finish_reason":"stop"}]}`)
				}))
				defer server.Close()
				channel := makeNativeChannel(server.URL)
				channel.SupportedAPITypes = `["chat-completion"]`
				backend := &Backend{}
				body := `{"model":"gemini-4-argon","messages":[{"role":"user","content":"hi"}]}`
				if protocol == llmkit.ProtocolOpenAIResponses {
					body = `{"model":"gemini-4-argon","input":"hi"}`
				}
				relay, recorder := newNativeTestCtx(t, []byte(body), protocol, false)
				result := backend.Relay(relay, state.Attempt{Channel: channel, RealModel: "gemini-4-argon"})
				require.NoError(t, result.Err)
				<-requests
				var response map[string]any
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				history := map[string]any{"model": "gemini-4-argon"}
				if protocol == llmkit.ProtocolOpenAIChat {
					message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
					messages := []any{message}
					if toolCall {
						call := message["tool_calls"].([]any)[0].(map[string]any)
						messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": "hi"})
					}
					history["messages"] = messages
				} else {
					input := response["output"].([]any)
					if toolCall {
						for _, value := range response["output"].([]any) {
							item := value.(map[string]any)
							if item["type"] == "function_call" {
								input = append(input, map[string]any{"type": "function_call_output", "call_id": item["call_id"], "output": "hi"})
							}
						}
					}
					history["input"] = input
				}
				bodyBytes, err := json.Marshal(history)
				require.NoError(t, err)
				relay, _ = newNativeTestCtx(t, bodyBytes, protocol, false)
				result = backend.Relay(relay, state.Attempt{Channel: channel, RealModel: "gemini-4-argon"})
				require.NoError(t, result.Err)
				received := <-requests
				messages := received["messages"].([]any)
				assistant := messages[0].(map[string]any)
				require.Equal(t, "text-signature", assistant["signature"])
				if toolCall {
					call := assistant["tool_calls"].([]any)[0].(map[string]any)
					require.Equal(t, "tool-signature", call["signature"])
					require.NotEmpty(t, call["id"])
					require.Equal(t, call["id"], messages[1].(map[string]any)["tool_call_id"])
				} else {
					require.Equal(t, "收到", assistant["content"])
				}
			})
		}
	}
}
