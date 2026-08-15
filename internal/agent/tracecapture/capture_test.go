package tracecapture

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestBodyWindowKeepsUTF8SafeBoundedTail(t *testing.T) {
	window := NewBodyWindow(7)
	for _, chunk := range [][]byte{[]byte("prefix-"), []byte("你"), []byte("好z")} {
		count, err := window.Write(chunk)
		require.NoError(t, err)
		require.Equal(t, len(chunk), count)
	}

	body := window.Capture(BodyCaptureDecision{Capture: true})
	require.True(t, body.Captured)
	require.Equal(t, "你好z", body.Data)
	require.Equal(t, int64(len("prefix-你好z")), body.TotalBytes)
	require.Equal(t, int64(len("你好z")), body.CapturedBytes)
	require.True(t, body.Truncated)
	require.True(t, utf8.ValidString(body.Data))
}

func TestDecideBodyReturnsPreciseSkipReasons(t *testing.T) {
	tests := []struct {
		name, protocol, contentType, contentEncoding string
		body                                         []byte
		readFailed                                   bool
		want                                         string
	}{
		{name: "encoded", contentType: "application/json", contentEncoding: "gzip", body: []byte("{}"), want: ReasonContentEncoded},
		{name: "multipart", contentType: "multipart/form-data; boundary=x", body: []byte("part"), want: ReasonMultipart},
		{name: "binary content type", contentType: "application/octet-stream", body: []byte("plain"), want: ReasonBinaryContentType},
		{name: "invalid utf8", contentType: "application/json", body: []byte{0xff}, want: ReasonBinaryDetected},
		{name: "obvious binary", contentType: "text/plain", body: []byte("a\x00b"), want: ReasonBinaryDetected},
		{name: "websocket", protocol: "websocket", contentType: "text/plain", body: []byte("message"), want: ReasonWebSocket},
		{name: "read failure", contentType: "text/plain", body: []byte("partial"), readFailed: true, want: ReasonCaptureReadFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window := NewBodyWindow(64)
			_, _ = window.Write(test.body)
			if test.readFailed {
				window.MarkReadFailed()
			}
			decision := DecideBody(test.protocol, test.contentType, test.contentEncoding, window.Text())
			body := window.Capture(decision)
			require.False(t, body.Captured)
			require.Equal(t, test.want, body.SkipReason)
			require.Empty(t, body.Data)
		})
	}
}

func TestRedactHeadersAndTrailersCaseInsensitivelyAndBoundsValues(t *testing.T) {
	headers := http.Header{
		"aUtHoRiZaTiOn":          {"Bearer client-secret"},
		"Proxy-Authorization":    {"proxy-secret"},
		"COOKIE":                 {"session=secret"},
		"set-cookie":             {"session=secret"},
		"X-API-KEY":              {"api-secret"},
		"Api-Key":                {"legacy-api-secret"},
		"X-GoOg-ApI-kEy":         {"google-api-secret"},
		"X-Custom-Auth":          {"dynamic-secret"},
		"X-Vaala-Agent-Secret":   {"agent-secret"},
		"Forwarded":              {"for=203.0.113.10;host=internal.example"},
		"x-FoRwArDeD-fOr":        {"203.0.113.10"},
		"X-Forwarded-Host":       {"internal.example"},
		"X-Forwarded-Custom-Hop": {"private-hop"},
		"X-Forwarding-Business":  {"visible"},
		"X-Safe":                 {strings.Repeat("v", MaxHeaderValueBytes+1)},
	}

	redacted, truncated := RedactHeaders(headers, "x-custom-auth")
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "Api-Key",
		"X-Goog-Api-Key", "X-Custom-Auth", "X-Vaala-Agent-Secret", "Forwarded",
		"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Custom-Hop",
	} {
		require.Equal(t, RedactedValue, redacted.Get(name), name)
	}
	require.Equal(t, "visible", redacted.Get("X-Forwarding-Business"))
	require.True(t, truncated)
	require.Len(t, redacted.Get("X-Safe"), MaxHeaderValueBytes)
}
