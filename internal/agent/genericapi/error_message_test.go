package genericapi

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestSafeAPIErrorMessageBasicCases(t *testing.T) {
	require.Empty(t, safeAPIErrorMessage(nil, protocol.APIUpstreamCredential{}))
	require.Equal(t, "dial tcp 127.0.0.1:1: connection refused", safeAPIErrorMessage(
		errors.New("dial tcp 127.0.0.1:1: connection refused"), protocol.APIUpstreamCredential{},
	))

	message := safeAPIErrorMessage(errors.New("first\nsecond\tthird"), protocol.APIUpstreamCredential{})
	require.Equal(t, "first second third", message)
	require.NotContains(t, message, "\n")
	require.NotContains(t, message, "\t")
	require.LessOrEqual(t, len(message), apiattempt.MaxAPIErrorMessageBytes)
	require.True(t, utf8.ValidString(message))
}

func TestSafeAPIErrorMessageNormalizesEmptyInvalidAndBoundaryText(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "empty", err: errors.New(""), want: "redacted"},
		{name: "control only", err: errors.New("\x00\n\t\x7f\u0085")},
		{name: "invalid utf8", err: errors.New(string([]byte{'a', 0xff, 'b'})), want: "a�b"},
		{name: "exact byte limit", err: errors.New(strings.Repeat("a", apiattempt.MaxAPIErrorMessageBytes)), want: strings.Repeat("a", apiattempt.MaxAPIErrorMessageBytes)},
		{name: "multibyte rune across byte limit", err: errors.New(strings.Repeat("a", apiattempt.MaxAPIErrorMessageBytes-1) + "€"), want: strings.Repeat("a", apiattempt.MaxAPIErrorMessageBytes-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := safeAPIErrorMessage(tt.err, protocol.APIUpstreamCredential{})
			require.NotEmpty(t, message)
			require.True(t, utf8.ValidString(message))
			require.LessOrEqual(t, len(message), apiattempt.MaxAPIErrorMessageBytes)
			for _, r := range message {
				require.False(t, unicode.IsControl(r), "message contains control rune %q", r)
			}
			if tt.want != "" {
				require.Equal(t, tt.want, message)
			}
		})
	}
}

func TestSafeAPIErrorMessageDropsURLAtEveryLevel(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://outer.example/health?api_key=outer-secret",
		Err: &url.Error{
			Op:  "Post",
			URL: "https://inner.example/v1?token=inner-secret",
			Err: errors.New("dial tcp: connection refused"),
		},
	}

	message := safeAPIErrorMessage(err, protocol.APIUpstreamCredential{})
	require.Equal(t, "Get: Post: dial tcp: connection refused", message)
	for _, secret := range []string{"outer.example", "api_key", "outer-secret", "inner.example", "token", "inner-secret"} {
		require.NotContains(t, message, secret)
	}
}

func TestSafeAPIErrorMessageDropsURLAndRedactsCredentials(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://user:pass@upstream.example/health?api_key=query-secret",
		Err: errors.New("dial tcp: connection refused"),
	}
	credential := protocol.APIUpstreamCredential{
		BearerToken: "bearer-secret", HeaderValue: "header-secret",
		QueryValue: "query-secret", BasicUsername: "user", BasicPassword: "pass",
	}
	message := safeAPIErrorMessage(err, credential)
	require.Contains(t, message, "Get")
	require.Contains(t, message, "connection refused")
	for _, secret := range []string{"upstream.example", "api_key", "query-secret", "bearer-secret", "header-secret", "user", "pass"} {
		require.NotContains(t, message, secret)
	}
}

func TestSafeAPIErrorMessageRedactsCredentialRepresentations(t *testing.T) {
	credential := protocol.APIUpstreamCredential{
		BearerToken:   "bearer-secret",
		HeaderValue:   "header-secret",
		QueryValue:    "query-secret",
		BasicUsername: "basic-user",
		BasicPassword: "basic-password",
	}
	basicPair := credential.BasicUsername + ":" + credential.BasicPassword
	basicBase64 := base64.StdEncoding.EncodeToString([]byte(basicPair))
	redactedCandidates := []string{
		credential.BearerToken,
		credential.HeaderValue,
		credential.QueryValue,
		credential.BasicPassword,
		basicPair,
		basicBase64,
		"Bearer " + credential.BearerToken,
	}

	message := safeAPIErrorMessage(errors.New("username="+credential.BasicUsername+" | "+strings.Join(redactedCandidates, " | ")), credential)
	require.Contains(t, message, "username="+credential.BasicUsername)
	for _, candidate := range redactedCandidates {
		require.NotContains(t, message, candidate)
	}
	require.Contains(t, message, "[REDACTED]")
}

func TestSafeAPIErrorMessageDoesNotRedactForEmptyCredential(t *testing.T) {
	require.Equal(t, "Bearer ordinary failure", safeAPIErrorMessage(errors.New("Bearer ordinary failure"), protocol.APIUpstreamCredential{}))
}

func TestSafeAPIErrorMessageRecoversAsRedacted(t *testing.T) {
	require.Equal(t, "redacted", safeAPIErrorMessage(panickingAPIError{}, protocol.APIUpstreamCredential{}))
}

type panickingAPIError struct{}

func (panickingAPIError) Error() string {
	panic("unexpected error rendering failure")
}
