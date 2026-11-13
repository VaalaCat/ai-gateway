package gemini

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, body string, stream bool) []ir.Event {
	t.Helper()
	events, err := NewHandler().DecodeResponse(context.Background(), protocol.DecodeResponseInput{Body: strings.NewReader(body), Stream: stream}, conversionState{model: "m"})
	require.NoError(t, err)
	var result []ir.Event
	for event := range events {
		result = append(result, event)
	}
	return result
}

func TestDecodeImageTextThoughtAndUsage(t *testing.T) {
	body := `{"modelVersion":"gemini-image","candidates":[{"content":{"role":"model","parts":[{"text":"think","thought":true},{"text":"你"},{"inlineData":{"mimeType":"image/webp","data":"AA=="},"thoughtSignature":"image-sig"},{"text":"好"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":80,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125}}`
	events := decode(t, body, false)
	var image *ir.ContentBlock
	var usage *ir.Usage
	var text string
	var thought bool
	for _, e := range events {
		require.NotEqual(t, ir.EventError, e.Type)
		switch e.Type {
		case ir.EventContentDelta:
			text += e.Delta.Text
		case ir.EventReasoningSummaryDelta:
			thought = true
		case ir.EventContentBlock:
			image = e.ContentBlock
		case ir.EventUsage:
			usage = e.Usage
		}
	}
	require.Equal(t, "你好", text)
	require.True(t, thought)
	require.Equal(t, "image/webp", image.MimeType)
	require.Equal(t, []string{"m"}, image.ProviderData.Models)
	require.JSONEq(t, `{"part_index":2,"text_offset":3}`, string(image.ProviderData.Fields["content_position"]))
	require.JSONEq(t, `"image-sig"`, string(image.ProviderData.Fields["thoughtSignature"]))
	require.Equal(t, &ir.Usage{PromptTokens: 20, CacheReadTokens: 80, CompletionTokens: 25, ReasoningTokens: 5, TotalTokens: 125}, usage)
	require.Equal(t, ir.EventDone, events[len(events)-1].Type)
}

func TestDecodeSSELateSignatureAndPostFinishUsage(t *testing.T) {
	body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"working"},{"functionCall":{"id":"call-a","name":"read","args":{"path":"a"}}}]}}]}` + "\n\n" +
		"data: " + `{"candidates":[{"content":{"parts":[{"thoughtSignature":"late"}]},"finishReason":"STOP"}]}` + "\n\n" +
		"data: " + `{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":15}}` + "\n\n"
	events := decode(t, body, true)
	var types []ir.EventType
	var toolCount int
	for _, e := range events {
		types = append(types, e.Type)
		if e.Type == ir.EventToolCallStart || e.Type == ir.EventToolCallEnd {
			toolCount++
			require.JSONEq(t, `"late"`, string(e.ToolCall.ProviderData.Fields["thoughtSignature"]))
		}
	}
	require.Equal(t, 2, toolCount)
	require.Equal(t, ir.EventContentDelta, events[1].Type)
	require.Contains(t, types, ir.EventUsage)
	require.Equal(t, "tool_calls", events[len(events)-1].FinishReason)
}

func TestDecodeLargeImageSSE(t *testing.T) {
	data := strings.Repeat("A", 2*1024*1024)
	frame := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": data}}}}, "finishReason": "STOP"}}}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	events := decode(t, "data: "+string(raw)+"\n\n", true)
	require.Len(t, events, 3)
	require.Equal(t, data, events[1].ContentBlock.MediaB64)
}

func TestDecodeErrorsAndEmptyUsageFrames(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		stream     bool
		wantErr    bool
	}{
		{"blocked", `{"promptFeedback":{"blockReason":"SAFETY"}}`, false, true},
		{"upstream error", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"limited"}}`, false, true},
		{"bad JSON", `{`, false, true},
		{"truncated stream", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n", true, true},
		{"multiple candidates", `{"candidates":[{"index":0},{"index":1}]}`, false, true},
		{"empty frames", "data: {}\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":{\"totalTokenCount\":1}}\n\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := decode(t, tc.body, tc.stream)
			require.Equal(t, tc.wantErr, events[len(events)-1].Type == ir.EventError)
		})
	}
}

func TestDecodeCancellationClosesReader(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	events, err := NewHandler().DecodeResponse(ctx, protocol.DecodeResponseInput{Body: reader, Stream: true}, nil)
	require.NoError(t, err)
	cancel()
	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("decode did not stop on cancellation")
	}
}

func TestDecodeLateImageSignatureDoesNotOverwriteTool(t *testing.T) {
	body := "data: " + `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{}},"thoughtSignature":"tool"},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}}]}` + "\n\n" +
		"data: " + `{"candidates":[{"content":{"parts":[{"thoughtSignature":"image"}]},"finishReason":"STOP"}]}` + "\n\n"
	events := decode(t, body, true)
	var image, tool string
	for _, event := range events {
		if event.Type == ir.EventContentBlock {
			image = string(event.ContentBlock.ProviderData.Fields["thoughtSignature"])
		}
		if event.Type == ir.EventToolCallStart {
			tool = string(event.ToolCall.ProviderData.Fields["thoughtSignature"])
		}
	}
	require.JSONEq(t, `"image"`, image)
	require.JSONEq(t, `"tool"`, tool)
}

func TestSignedToolPublishesBeforeEOF(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer writer.Close()
	events, err := NewHandler().DecodeResponse(ctx, protocol.DecodeResponseInput{Body: reader, Stream: true}, nil)
	require.NoError(t, err)
	go func() {
		_, _ = io.WriteString(writer, "data: "+`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{}},"thoughtSignature":"signed"}]}}]}`+"\n\n")
	}()
	for {
		select {
		case event := <-events:
			if event.Type == ir.EventToolCallEnd {
				require.Equal(t, `"signed"`, string(event.ToolCall.ProviderData.Fields["thoughtSignature"]))
				return
			}
		case <-time.After(time.Second):
			t.Fatal("signed tool waited for EOF")
		}
	}
}

func TestUnsignedToolDoesNotMoveAfterFollowingText(t *testing.T) {
	body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"before"},{"functionCall":{"name":"read","args":{}}},{"text":"after"}]},"finishReason":"STOP"}]}` + "\n\n"
	events := decode(t, body, true)
	var sequence []string
	for _, event := range events {
		if event.Type == ir.EventContentDelta {
			sequence = append(sequence, event.Delta.Text)
		} else if event.Type == ir.EventToolCallStart {
			sequence = append(sequence, "call")
		}
	}
	require.Equal(t, []string{"before", "call", "after"}, sequence)
}
