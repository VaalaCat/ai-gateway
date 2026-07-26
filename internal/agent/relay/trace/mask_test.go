package trace

import (
	"strings"
	"testing"
	"unicode/utf8"

	backendcommon "github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/stretchr/testify/require"
)

func TestMaskingTailMatchesPatternOracleAcrossEveryChunkSplit(t *testing.T) {
	inputs := []string{
		"Key Bearer abc",
		"Key xxxBearer abc",
		"Bearer Key abc",
		"Key sk-abcdefghijklmnopqrst",
		"Bearer sk-abcdefghijklmnopqrst",
		"sk-abcdefghijklmnopqrstKey Bearer abc",
		"Bearer abc,Key def;sk-abcdefghijklmnopqrst\"tail",
		"Bearer\tabc Key\ndef Bearer\fghi Key\rjkl",
		"Bearer abc,Key def;Bearer ghi\"Key jkl",
		"Bear BearerX Keynote sk-short ordinary",
		"Bearer \t",
		"Key\n",
		"sk-abcdefghijklmnopqrs",
	}

	for _, input := range inputs {
		want := maskPatterns(input)
		t.Run(input, func(t *testing.T) {
			for split := 0; split <= len(input); split++ {
				capture := backendcommon.NewMaskingTail(4096)
				_, err := capture.Write([]byte(input[:split]))
				require.NoError(t, err)
				_, err = capture.Write([]byte(input[split:]))
				require.NoError(t, err)
				require.Equal(t, want, capture.String(), "split=%d", split)
			}

			capture := backendcommon.NewMaskingTail(4096)
			for index := range input {
				_, err := capture.Write([]byte{input[index]})
				require.NoError(t, err)
			}
			require.Equal(t, want, capture.String(), "single-byte writes")
		})
	}
}

func TestMaskingTailMatchesBoundedOracleAfterLongWhitespace(t *testing.T) {
	const limit = 32
	whitespace := strings.Repeat(" \t\n\f\r", limit*4)
	for _, input := range []string{
		"prefix Bearer" + whitespace + ";tail",
		"prefix Bearer" + whitespace,
		"prefix Bearer" + whitespace + "secret,tail",
		"Key Bearer" + whitespace + "secret,tail",
	} {
		want := maskPatterns(input)
		if len(want) > limit {
			want = want[len(want)-limit:]
		}
		capture := backendcommon.NewMaskingTail(limit)
		for index := range input {
			_, err := capture.Write([]byte{input[index]})
			require.NoError(t, err)
		}
		require.Equal(t, want, capture.String(), input)
	}
}

func TestMaskTextPreservingLength(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		secrets []string
		secret  string
	}{
		{name: "channel secret", input: "before-channel-secret-after", secrets: []string{"channel-secret"}, secret: "channel-secret"},
		{name: "host beside multibyte", input: "前api.example.com后", secrets: []string{"api.example.com"}, secret: "api.example.com"},
		{name: "sk token", input: "key=sk-abcdefghijklmnopqrstuvwxyz", secret: "sk-abcdefghijklmnopqrstuvwxyz"},
		{name: "bearer token", input: "前 Bearer eyJhbGciOiJIUzI1NiJ9.xxx 后", secret: "Bearer eyJhbGciOiJIUzI1NiJ9.xxx"},
		{name: "key token", input: "Key abcdefghijklmnop", secret: "Key abcdefghijklmnop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskTextPreservingLength(tt.input, tt.secrets)
			require.Len(t, []byte(got), len([]byte(tt.input)))
			require.NotContains(t, got, tt.secret)
			require.Equal(t, strings.Repeat("*", len(tt.secret)), got[strings.Index(tt.input, tt.secret):strings.Index(tt.input, tt.secret)+len(tt.secret)])
		})
	}
}

func TestMaskSensitiveValues(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		secrets  []string
		expected string
	}{
		{
			name:     "mask channel key in body",
			input:    `{"key":"sk-abc123def456","model":"gpt-4"}`,
			secrets:  []string{"sk-abc123def456"},
			expected: `{"key":"***","model":"gpt-4"}`,
		},
		{
			name:     "mask host in body",
			input:    `url is https://api.openai.com/v1/chat`,
			secrets:  []string{"api.openai.com"},
			expected: `url is https://***/v1/chat`,
		},
		{
			name:     "no secrets to mask",
			input:    `{"model":"gpt-4"}`,
			secrets:  []string{},
			expected: `{"model":"gpt-4"}`,
		},
		{
			name:     "empty secret ignored",
			input:    `{"model":"gpt-4"}`,
			secrets:  []string{""},
			expected: `{"model":"gpt-4"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskSensitiveValues(tt.input, tt.secrets)
			if got != tt.expected {
				t.Errorf("maskSensitiveValues() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMaskPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "mask sk- pattern",
			input:    `key is sk-abcdefghijklmnopqrstuvwxyz`,
			expected: `key is sk-***`,
		},
		{
			name:     "mask Bearer token",
			input:    `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.xxx`,
			expected: `Authorization: Bearer ***`,
		},
		{
			name:     "no pattern match",
			input:    `just normal text`,
			expected: `just normal text`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskPatterns(tt.input)
			if got != tt.expected {
				t.Errorf("maskPatterns() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMaskHeaders(t *testing.T) {
	input := map[string][]string{
		"Authorization": {"Bearer sk-secret123"},
		"Content-Type":  {"application/json"},
		"X-Api-Key":     {"my-secret-key"},
		"Cookie":        {"session=abc123"},
	}
	secrets := []string{"sk-secret123"}
	got := maskHeaders(input, secrets)

	if got["Authorization"][0] != "***" {
		t.Errorf("Authorization should be masked, got %q", got["Authorization"][0])
	}
	if got["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type should not be masked, got %q", got["Content-Type"][0])
	}
	if got["X-Api-Key"][0] != "***" {
		t.Errorf("X-Api-Key should be masked, got %q", got["X-Api-Key"][0])
	}
	if got["Cookie"][0] != "***" {
		t.Errorf("Cookie should be masked, got %q", got["Cookie"][0])
	}
}

func TestTruncateBodyKeepsTailWithinByteLimit(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		limit int
		want  string
	}{
		{name: "empty", body: "", limit: 4, want: ""},
		{name: "under limit", body: "abc", limit: 4, want: "abc"},
		{name: "exact limit", body: "abcd", limit: 4, want: "abcd"},
		{name: "limit plus one", body: "abcde", limit: 4, want: "...(truncated)bcde"},
		{name: "limit one", body: "abc", limit: 1, want: "...(truncated)c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBodyWithLimit(tt.body, tt.limit)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTruncateBodySkipsUTF8ContinuationAtTailStart(t *testing.T) {
	got := truncateBodyWithLimit("a界b", 3)
	require.Equal(t, "...(truncated)b", got)
	require.True(t, utf8.ValidString(got))

	// A fully continuation-byte tail has no valid rune start and keeps only the marker.
	got = truncateBodyWithLimit(string([]byte{'a', 0x80, 0x80}), 2)
	require.Equal(t, truncatedPrefix, got)
	require.True(t, utf8.ValidString(got))

	got = truncateBodyWithLimit(string([]byte{'a', 0xff, 'b'}), 2)
	require.Equal(t, "...(truncated)b", got)
	require.True(t, utf8.ValidString(got))
}

func TestTruncateBodyLimitFallback(t *testing.T) {
	body := strings.Repeat("a", defaultTraceMaxBodySize) + "z"
	for _, limit := range []int{0, -1} {
		got := truncateBodyWithLimit(body, limit)
		require.Equal(t, truncatedPrefix+body[1:], got)
		require.Len(t, strings.TrimPrefix(got, truncatedPrefix), defaultTraceMaxBodySize)
	}
}
