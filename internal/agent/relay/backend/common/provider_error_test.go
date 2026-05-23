package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseProviderErrorType_Anthropic 验证 Anthropic body 形态:
// {"type":"error", "error":{"type":"invalid_request_error", ...}} 能拿到内嵌 type。
func TestParseProviderErrorType_Anthropic(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`)
	require.Equal(t, "invalid_request_error", ParseProviderErrorType(body))
}

// TestParseProviderErrorType_OpenAI 验证 OpenAI body 形态:
// {"error":{"type":"...", "code":"..."}} 能拿到 error.type。
func TestParseProviderErrorType_OpenAI(t *testing.T) {
	body := []byte(`{"error":{"type":"rate_limit_exceeded","code":"429"}}`)
	require.Equal(t, "rate_limit_exceeded", ParseProviderErrorType(body))
}

// TestParseProviderErrorType_EmptyBody nil / 空 byte slice 都应返回空串而不 panic。
func TestParseProviderErrorType_EmptyBody(t *testing.T) {
	require.Equal(t, "", ParseProviderErrorType(nil))
	require.Equal(t, "", ParseProviderErrorType([]byte{}))
}

// TestParseProviderErrorType_NotJSON 非 JSON body(纯文本/HTML 错误页)应安静返回空串。
func TestParseProviderErrorType_NotJSON(t *testing.T) {
	require.Equal(t, "", ParseProviderErrorType([]byte("plain text error")))
}

// TestUpstreamError_Error 验证 Error() 文案三类情形:
//   - 普通 HTTP 错误带 status + body
//   - Status=0 表示网络层错误,文案含 "network"
//   - body 超过 256 字符触发 "..." 截断
func TestUpstreamError_Error(t *testing.T) {
	upErr := &UpstreamError{Status: 429, Body: []byte("rate limit exceeded")}
	require.Contains(t, upErr.Error(), "429")
	require.Contains(t, upErr.Error(), "rate limit exceeded")

	// network 错误(Status=0)
	netErr := &UpstreamError{Status: 0, Body: []byte("connection refused")}
	require.Contains(t, netErr.Error(), "network")

	// body 超长触发 256 字符截断标记
	longBody := make([]byte, 1000)
	for i := range longBody {
		longBody[i] = 'x'
	}
	longErr := &UpstreamError{Status: 500, Body: longBody}
	require.Contains(t, longErr.Error(), "...")
}
