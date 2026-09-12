package llmkit_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	codec "github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekReasoningGoldenChatToResponses(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		golden         string
		wantToolCalls  int
		decodeAsStream bool
	}{
		{
			name:           "reasoning answer",
			input:          "golden/deepseek_reasoning/chat_reasoning_answer.input.sse",
			golden:         "deepseek_reasoning/chat_reasoning_answer.responses.sse",
			decodeAsStream: true,
		},
		{
			name:   "reasoning answer nonstream",
			input:  "golden/deepseek_reasoning/chat_reasoning_answer_nonstream.input.json",
			golden: "deepseek_reasoning/chat_reasoning_answer_nonstream.responses.sse",
		},
		{
			name:           "reasoning tool call",
			input:          "golden/deepseek_reasoning/chat_reasoning_tool_call.input.sse",
			golden:         "deepseek_reasoning/chat_reasoning_tool_call.responses.sse",
			wantToolCalls:  1,
			decodeAsStream: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events, output := roundTripStream(
				t,
				test.input,
				getOutbound(codec.ProtocolOpenAIChat),
				getInbound(codec.ProtocolOpenAIResponses),
				test.decodeAsStream,
				true,
			)

			var summaryDeltas, contentDeltas, reasoningDone, streamStarts, completions int
			var finishReason string
			for _, event := range events {
				switch event.Type {
				case codec.EventStreamStart:
					streamStarts++
				case codec.EventDone:
					completions++
					finishReason = event.FinishReason
				case codec.EventReasoningSummaryDelta:
					summaryDeltas++
				case codec.EventReasoningContentDelta:
					contentDeltas++
				case codec.EventReasoningDone:
					reasoningDone++
				}
			}
			require.Positive(t, summaryDeltas)
			require.Equal(t, summaryDeltas, contentDeltas)
			require.Equal(t, 1, reasoningDone)

			records := parseCrossSSE([]byte(output))
			require.GreaterOrEqual(t, len(records), 3)
			require.Equal(t, "response.created", records[0].Event)
			require.Equal(t, "response.in_progress", records[1].Event)
			require.Equal(t, "response.completed", records[len(records)-1].Event)
			lifecycleCounts := make(map[string]int)
			var reasoningItem map[string]any
			var functionCalls, wireSummaryDeltas, wireContentDeltas int
			reasoningDoneIndex, firstFunctionIndex := -1, -1
			for index, record := range records {
				lifecycleCounts[record.Event]++
				switch record.Event {
				case "response.reasoning_summary_text.delta":
					wireSummaryDeltas++
				case "response.reasoning_text.delta":
					wireContentDeltas++
				case "response.output_item.added":
					item, _ := record.Data["item"].(map[string]any)
					if item["type"] == "function_call" {
						functionCalls++
						if firstFunctionIndex < 0 {
							firstFunctionIndex = index
						}
					}
				case "response.output_item.done":
					item, _ := record.Data["item"].(map[string]any)
					if item["type"] == "reasoning" {
						reasoningItem = item
						reasoningDoneIndex = index
					}
				}
			}

			require.Equal(t, 1, lifecycleCounts["response.created"])
			require.Equal(t, 1, lifecycleCounts["response.in_progress"])
			require.Equal(t, 1, lifecycleCounts["response.completed"])
			require.Equal(t, 1, streamStarts)
			require.Equal(t, 1, completions)
			require.Equal(t, codec.EventStreamStart, events[0].Type)
			require.Equal(t, codec.EventDone, events[len(events)-1].Type)
			wantFinish := "stop"
			if test.wantToolCalls > 0 {
				wantFinish = "tool_calls"
			}
			require.Equal(t, wantFinish, finishReason)
			require.NotNil(t, reasoningItem)
			require.NotEmpty(t, reasoningItem["summary"])
			require.NotEmpty(t, reasoningItem["content"])
			encrypted, _ := reasoningItem["encrypted_content"].(string)
			require.True(t, strings.HasPrefix(encrypted, "llmkit:v1:"))
			require.Equal(t, summaryDeltas, wireSummaryDeltas)
			require.Equal(t, contentDeltas, wireContentDeltas)
			require.Equal(t, test.wantToolCalls, functionCalls)
			if test.wantToolCalls > 0 {
				require.Less(t, reasoningDoneIndex, firstFunctionIndex)
			}

			assertSSEFormat(t, output, codec.ProtocolOpenAIResponses)
			assertGoldenSSE(t, output, test.golden)
		})
	}
}

func TestDeepSeekReasoningGoldenResponsesHistoryToChat(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		golden        string
		stripThinking bool
		assert        func(*testing.T, []any)
	}{
		{
			name:   "multiturn tool history",
			input:  "responses_multiturn_tool_history.input.json",
			golden: "deepseek_reasoning/responses_multiturn_tool_history.chat.json",
			assert: func(t *testing.T, messages []any) {
				require.Len(t, messages, 4)
				first := messages[1].(map[string]any)
				require.Equal(t, "Need inspect files.", first["reasoning_content"])
				require.Equal(t, "I will inspect the project.", first["content"])
				require.Len(t, first["tool_calls"], 1)
				toolResult := messages[2].(map[string]any)
				require.Equal(t, "tool", toolResult["role"])
				require.Equal(t, "call_a", toolResult["tool_call_id"])
				second := messages[3].(map[string]any)
				require.Equal(t, "Tool returned files.", second["reasoning_content"])
				require.Equal(t, "The project contains README.md and src.", second["content"])
			},
		},
		{
			name:   "parallel tool calls",
			input:  "responses_parallel_tool_calls.input.json",
			golden: "deepseek_reasoning/responses_parallel_tool_calls.chat.json",
			assert: func(t *testing.T, messages []any) {
				require.Len(t, messages, 4)
				assistant := messages[1].(map[string]any)
				require.Equal(t, "Need inspect files.", assistant["reasoning_content"])
				require.Equal(t, "", assistant["content"])
				require.Len(t, assistant["tool_calls"], 2)
				firstResult := messages[2].(map[string]any)
				require.Equal(t, "tool", firstResult["role"])
				require.Equal(t, "call_a", firstResult["tool_call_id"])
				secondResult := messages[3].(map[string]any)
				require.Equal(t, "tool", secondResult["role"])
				require.Equal(t, "call_b", secondResult["tool_call_id"])
			},
		},
		{
			name:   "readable fallback",
			input:  "reasoning_without_envelope.input.json",
			golden: "deepseek_reasoning/reasoning_without_envelope.chat.json",
			assert: func(t *testing.T, messages []any) {
				require.Equal(t, "content one", messages[0].(map[string]any)["reasoning_content"])
				require.Equal(t, "summary two", messages[2].(map[string]any)["reasoning_content"])
			},
		},
		{
			name:   "malformed envelope",
			input:  "malformed_envelope.input.json",
			golden: "deepseek_reasoning/malformed_envelope.chat.json",
			assert: func(t *testing.T, messages []any) {
				assistant := messages[0].(map[string]any)
				require.Equal(t, "readable fallback", assistant["reasoning_content"])
				require.NotContains(t, stringMustJSON(t, assistant), "llmkit:v1")
			},
		},
		{
			name:          "passthrough disabled",
			input:         "passthrough_disabled.input.json",
			golden:        "deepseek_reasoning/passthrough_disabled.chat.json",
			stripThinking: true,
			assert: func(t *testing.T, messages []any) {
				require.Len(t, messages, 5)
				for _, value := range messages {
					require.NotContains(t, value.(map[string]any), "reasoning_content")
				}
				require.Equal(t, "visible answer", messages[1].(map[string]any)["content"])
				toolAssistant := messages[3].(map[string]any)
				require.Equal(t, "", toolAssistant["content"])
				require.Len(t, toolAssistant["tool_calls"], 1)
				toolResult := messages[4].(map[string]any)
				require.Equal(t, "tool", toolResult["role"])
				require.Equal(t, "call_a", toolResult["tool_call_id"])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := encodeDeepSeekHistoryFixture(t, test.input, test.stripThinking)
			messages := mustGetArray(t, output, "messages")
			test.assert(t, messages)
			assertGoldenJSON(t, output, test.golden)
		})
	}
}

func encodeDeepSeekHistoryFixture(t *testing.T, fixture string, stripThinking bool) map[string]any {
	t.Helper()
	body, err := os.ReadFile("testdata/golden/deepseek_reasoning/" + fixture)
	require.NoError(t, err)

	inbound := getInbound(codec.ProtocolOpenAIResponses)
	request := &http.Request{Body: io.NopCloser(bytes.NewReader(body))}
	decoded, err := inbound.DecodeRequest(request)
	require.NoError(t, err)
	if stripThinking {
		decoded.Messages = transform.ApplyThinkingStrip(decoded.Messages)
	}

	outbound := getOutbound(codec.ProtocolOpenAIChat)
	encoded, err := outbound.EncodeRequest(decoded, &testChannelConfig{
		BaseURL: "https://test.example.com",
		APIKey:  "test",
		Model:   "deepseek-reasoner",
	})
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.NewDecoder(encoded.Body).Decode(&result))
	return result
}

func stringMustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
