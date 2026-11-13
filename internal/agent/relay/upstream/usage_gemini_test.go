package upstream

import (
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"testing"
)

func TestGeminiUsageObserver(t *testing.T) {
	usage := `"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":30,"candidatesTokenCount":7,"thoughtsTokenCount":3,"totalTokenCount":110}`
	image := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 2*1024*1024) + `"}}]},"finishReason":"STOP"}],` + usage + `}`
	for _, tc := range []struct {
		name, body string
		stream     bool
	}{
		{"json", image, false}, {"array", "[" + image + "," + image + "]", true}, {"sse", "data: " + image + "\n\ndata: {" + usage + "}\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := &GeminiUsageObserver{Stream: tc.stream}
			for i := 0; i < len(tc.body); i += 37 {
				_, err := observer.Write([]byte(tc.body[i:min(i+37, len(tc.body))]))
				require.NoError(t, err)
			}
			require.NoError(t, observer.Finish())
			require.Equal(t, 70, observer.Usage.PromptTokens)
			require.Equal(t, 10, observer.Usage.CompletionTokens)
			require.Equal(t, 30, observer.Usage.CacheReadTokens)
			require.LessOrEqual(t, cap(observer.text), 256)
			require.Less(t, cap(observer.capture), 4096)
		})
	}
}

func TestGeminiUsageObserverFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"cut JSON", `data: {"candidates":[{"content":{"parts":[{"text":"cut`, true},
		{"missing finish", `data: {"candidates":[{"content":{"parts":[{"text":"cut"}]}}]}` + "\n\n", true},
		{"upstream error", `data: {"error":{"code":429,"message":"rate limited"}}` + "\n\n", true},
		{"blocked", `{"promptFeedback":{"blockReason":"SAFETY"}}`, true},
		{"empty candidates", `data: {"candidates":[],"usageMetadata":{"promptTokenCount":1}}` + "\n\n", false},
		{"tool result error is content", `{"candidates":[{"content":{"parts":[{"functionResponse":{"response":{"error":{"code":1,"message":"tool failed"},"usageMetadata":{"promptTokenCount":900}}}}]},"finishReason":"STOP"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &GeminiUsageObserver{Stream: true}
			_, _ = io.WriteString(o, tc.body)
			if tc.wantErr {
				require.Error(t, o.Finish())
			} else {
				require.NoError(t, o.Finish())
			}
		})
	}
}

func TestGeminiUsageObserverIgnoresSSEMetadataAndToolFields(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		body := ": {\"usageMetadata\":{\"promptTokenCount\":90000}}" + newline + "event: {\"error\":{\"code\":400,\"message\":\"fake error\"}}" + newline +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"tool\",\"args\":{\"usageMetadata\":{\"promptTokenCount\":90000},\"error\":{\"code\":400,\"message\":\"fake\"},\"promptFeedback\":{\"blockReason\":\"fake\"}}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10}}" + newline + newline +
			": {\"error\":{\"code\":429,\"message\":\"fake trailing error\"}}" + newline + ": {\"usageMetadata\":{\"promptTokenCount\":90000}}" + newline
		observer := &GeminiUsageObserver{Stream: true}
		for _, b := range []byte(body) {
			_, _ = observer.Write([]byte{b})
		}
		require.NoError(t, observer.Finish())
		require.Equal(t, 10, observer.Usage.PromptTokens)
	}
	o := &GeminiUsageObserver{Stream: true}
	_, _ = io.WriteString(o, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":{"finishReason":"STOP"}}}]}}]}`+"\n\n")
	require.ErrorIs(t, o.Finish(), io.ErrUnexpectedEOF)
}
