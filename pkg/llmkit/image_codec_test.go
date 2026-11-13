package llmkit

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/stretchr/testify/require"
)

func imageCodecEvents() []Event {
	return []Event{
		{Type: EventStreamStart, Model: "image-model"},
		{Type: EventContentDelta, Delta: &DeltaPayload{Text: "前"}},
		{Type: EventContentBlock, ContentBlock: &ContentBlock{Type: ContentTypeImage, MimeType: "image/webp", MediaB64: "aW1hZ2Ux", ProviderData: &ProviderData{Protocol: ProtocolGemini, Models: []string{"image-model"}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"sig-1"`), "content_position": json.RawMessage(`{"part_index":1,"text_offset":3}`)}}}},
		{Type: EventContentDelta, Delta: &DeltaPayload{Text: "后"}},
		{Type: EventContentBlock, ContentBlock: &ContentBlock{Type: ContentTypeImage, MimeType: "image/jpeg", MediaB64: "aW1hZ2Uy", ProviderData: &ProviderData{Protocol: ProtocolGemini, Models: []string{"image-model"}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"sig-2"`), "content_position": json.RawMessage(`{"part_index":3,"text_offset":6}`)}}}},
		{Type: EventUsage, Usage: &Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}},
		{Type: EventDone, FinishReason: "stop"},
	}
}

func encodeImageTestEvents(t *testing.T, protocol Protocol, stream bool, events []Event) []byte {
	t.Helper()
	in := make(chan Event, len(events))
	for _, event := range events {
		in <- event
	}
	close(in)
	out, err := NewCodec().EncodeResponse(context.Background(), EncodeResponseInput{Protocol: protocol, Stream: stream, Events: in})
	require.NoError(t, err)
	var body bytes.Buffer
	for chunk := range out {
		require.NoError(t, chunk.Err)
		body.Write(chunk.Data)
	}
	return body.Bytes()
}

func TestChatImagesResponseAndHistory(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			body := encodeImageTestEvents(t, ProtocolOpenAIChat, stream, imageCodecEvents())
			var images []json.RawMessage
			var text strings.Builder
			if stream {
				for _, line := range strings.Split(string(body), "\n") {
					if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
						continue
					}
					var frame struct {
						Choices []struct {
							Delta struct {
								Content string            `json:"content"`
								Images  []json.RawMessage `json:"images"`
							} `json:"delta"`
						} `json:"choices"`
					}
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame))
					for _, choice := range frame.Choices {
						text.WriteString(choice.Delta.Content)
						images = append(images, choice.Delta.Images...)
					}
				}
			} else {
				var response struct {
					Choices []struct {
						Message struct {
							Content string            `json:"content"`
							Images  []json.RawMessage `json:"images"`
						} `json:"message"`
					} `json:"choices"`
				}
				require.NoError(t, json.Unmarshal(body, &response))
				require.Len(t, response.Choices, 1)
				text.WriteString(response.Choices[0].Message.Content)
				images = response.Choices[0].Message.Images
			}
			require.Equal(t, "前后", text.String())
			require.Len(t, images, 2)
			var first map[string]any
			require.NoError(t, json.Unmarshal(images[0], &first))
			require.Equal(t, float64(0), first["index"])
			require.Equal(t, "data:image/webp;base64,aW1hZ2Ux", first["image_url"].(map[string]any)["url"])
			history, err := json.Marshal(map[string]any{"model": "image-model", "messages": []any{map[string]any{"role": "assistant", "content": text.String(), "images": images}}})
			require.NoError(t, err)
			decoded, err := NewCodec().DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: history})
			require.NoError(t, err)
			require.Len(t, decoded.Request.Messages[0].Content, 3)
			for i := 0; i < 2; i++ {
				block := decoded.Request.Messages[0].Content[i+1]
				require.Equal(t, ContentTypeImage, block.Type)
				require.Equal(t, imageCodecEvents()[2+i*2].ContentBlock.ProviderData, block.ProviderData)
			}
			// 再编码 Chat 请求时仍保留图片原生扩展。
			encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{Request: decoded.Request, Target: Target{Protocol: ProtocolOpenAIChat, BaseURL: "https://example.invalid", Model: "image-model"}})
			require.NoError(t, err)
			require.Contains(t, string(encoded.Body), `"thoughtSignature":"sig-1"`)
		})
	}
}

func TestChatExplicitImageOrderAndUnknownGenerationParameters(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,eA=="},"provider_data":{"protocol":"gemini","fields":{"thoughtSignature":"signature","content_position":{"part_index":50,"text_offset":999}}}},{"type":"text","text":"after"}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"],"futureFlag":true},"modalities":["text","image"],"future_option":{"enabled":true}}`)
	decoded, err := NewCodec().DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/chat/completions", Body: body})
	require.NoError(t, err)
	require.Len(t, decoded.Request.Messages[0].Content, 2)
	for _, block := range decoded.Request.Messages[0].Content {
		require.Equal(t, true, block.Metadata["explicit_content_order"])
	}
	require.Equal(t, "after", decoded.Request.Messages[0].Content[1].Text)
	require.NotNil(t, decoded.Request.Messages[0].Content[0].ProviderData)
	require.Equal(t, true, decoded.Request.Extras["generationConfig"].(map[string]any)["futureFlag"])
	require.Equal(t, map[string]any{"enabled": true}, decoded.Request.Extras["future_option"])
}

func TestNonStreamCodecsKeepNewToolEvents(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAIChat, ProtocolClaude, ProtocolOpenAIResponses} {
		t.Run(string(protocol), func(t *testing.T) {
			events := []Event{{Type: EventStreamStart}, {Type: EventToolCallStart, ToolCall: &StreamingToolCall{CallID: "call_1", Name: "draw", ProviderData: &ProviderData{Protocol: ProtocolGemini, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"signature"`)}}}}, {Type: EventToolCallArgumentsDelta, ToolCall: &StreamingToolCall{CallID: "call_1", Arguments: `{"prompt":`}}, {Type: EventToolCallArgumentsDelta, ToolCall: &StreamingToolCall{CallID: "call_1", Arguments: `"cat"}`}}, {Type: EventToolCallEnd, ToolCall: &StreamingToolCall{CallID: "call_1", Name: "draw", Arguments: `{"prompt":"cat"}`}}, {Type: EventDone, FinishReason: "tool_calls"}}
			body := encodeImageTestEvents(t, protocol, false, events)
			require.Contains(t, string(body), `"draw"`)
			require.Contains(t, string(body), "cat")
			require.Contains(t, string(body), "call_llmkit_v1_")
		})
	}
}

func TestGeminiToolProviderDataSurvivesEndpointChange(t *testing.T) {
	for _, model := range []string{"same", "other"} {
		t.Run(model, func(t *testing.T) {
			data := &ProviderData{Protocol: ProtocolGemini, Models: []string{"same"}, Fields: map[string]json.RawMessage{"thoughtSignature": json.RawMessage(`"signature"`)}}
			id := convert.PackToolCallID("call_1", data)
			request := Request{Messages: []Message{{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Name: "f"}}}, {Role: RoleTool, ToolCallID: id}}}
			convert.PrepareToolCallsForUpstream(&request, ProtocolGemini, model, convert.ToolSignatureFallbackNone)
			require.Equal(t, "call_1", request.Messages[0].ToolCalls[0].ID)
			require.Equal(t, "call_1", request.Messages[1].ToolCallID)
			require.NotNil(t, request.Messages[0].ToolCalls[0].ProviderData)
			require.Equal(t, data.Fields, request.Messages[0].ToolCalls[0].ProviderData.Fields)
		})
	}
}

func TestChatLargeImageResponseDecode(t *testing.T) {
	// 1. 通过公开编码器生成大于旧 1MiB 行上限的图片 SSE。
	image := &ContentBlock{Type: ContentTypeImage, MimeType: "image/png", MediaB64: strings.Repeat("YQ==", 512*1024)}
	body := encodeImageTestEvents(t, ProtocolOpenAIChat, true, []Event{{Type: EventStreamStart}, {Type: EventContentBlock, ContentBlock: image}, {Type: EventUsage, Usage: &Usage{PromptTokens: 1, CompletionTokens: 9, TotalTokens: 10}}, {Type: EventDone}})
	events, err := NewCodec().DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolOpenAIChat, StatusCode: 200, Stream: true, Body: bytes.NewReader(body)})
	require.NoError(t, err)
	// 2. 大图完整恢复，图片后用量仍然可读。
	var images int
	var usage *Usage
	for event := range events {
		require.NotEqual(t, EventError, event.Type)
		if event.Type == EventContentBlock {
			images++
			require.Equal(t, image.MediaB64, event.ContentBlock.MediaB64)
		}
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	require.Equal(t, 1, images)
	require.NotNil(t, usage)
	require.Equal(t, 9, usage.CompletionTokens)
}

func TestImageHistoryDoesNotLeakResponsesRawJSONToClaude(t *testing.T) {
	// 1. 从 Responses 图片生成历史恢复结构化图片。
	request, err := NewCodec().DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/v1/responses", Body: []byte(`{"model":"m","input":[{"type":"image_generation_call","id":"ig_1","status":"completed","result":"eA==","output_format":"jpeg","provider_data":{"protocol":"gemini","fields":{"thoughtSignature":"signature"}}}]}`)})
	require.NoError(t, err)
	// 2. Claude 接收图片输入格式，而非错误的原生 Responses 输出项。
	encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{Request: request.Request, Target: Target{Protocol: ProtocolClaude, BaseURL: "https://example.invalid", Model: "m"}})
	require.NoError(t, err)
	require.Contains(t, string(encoded.Body), `"media_type":"image/jpeg"`)
	require.Contains(t, string(encoded.Body), `"type":"image"`)
	require.NotContains(t, string(encoded.Body), "image_generation_call")
}
