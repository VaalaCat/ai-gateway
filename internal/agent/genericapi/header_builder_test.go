package genericapi

import (
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestHeaderBuilderStripsHopByHopAndGatewayCredentials(t *testing.T) {
	client := http.Header{
		"Authorization":       {"Bearer gateway-token"},
		"Host":                {"attacker.example"},
		"Connection":          {"keep-alive, X-Hop", "X-Second-Hop"},
		"X-Hop":               {"secret"},
		"X-Second-Hop":        {"secret-2"},
		"Keep-Alive":          {"timeout=5"},
		"Proxy-Connection":    {"keep-alive"},
		"Proxy-Authorization": {"Basic secret"},
		"Te":                  {"trailers"},
		"Trailer":             {"X-Checksum"},
		"Transfer-Encoding":   {"chunked"},
		"Upgrade":             {"websocket"},
		"Forwarded":           {"for=attacker"},
		"X-Forwarded-For":     {"127.0.0.1"},
		"X-Vaala-Agent-ID":    {"forged"},
		"Cookie":              {"session=kept"},
		"X-End-To-End":        {"client"},
	}
	upstream := protocol.SyncedAPIUpstream{AuthType: "header", HeaderOverride: map[string]string{
		"X-End-To-End":  "admin",
		"X-Admin":       "configured",
		"Authorization": "Bearer admin-override",
		"X-Admin-Key":   "override-loses-to-credential",
	}}
	credential := protocol.APIUpstreamCredential{HeaderName: "X-Admin-Key", HeaderValue: "upstream-secret"}

	got := (HeaderBuilder{}).Build(client, upstream, credential)

	for _, stripped := range []string{
		"Host", "Connection", "X-Hop", "X-Second-Hop", "Keep-Alive",
		"Proxy-Connection", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding",
		"Upgrade", "Forwarded", "X-Forwarded-For", "X-Vaala-Agent-ID",
	} {
		require.Emptyf(t, got.Values(stripped), "%s must be stripped", stripped)
	}
	require.Equal(t, "session=kept", got.Get("Cookie"))
	require.Equal(t, "admin", got.Get("X-End-To-End"))
	require.Equal(t, "configured", got.Get("X-Admin"))
	require.Equal(t, "Bearer admin-override", got.Get("Authorization"))
	require.Equal(t, "upstream-secret", got.Get("X-Admin-Key"))
	require.Equal(t, "Bearer gateway-token", client.Get("Authorization"), "client headers must remain unchanged")
}

func TestHeaderBuilderDropsCRLFAndInvalidNames(t *testing.T) {
	client := http.Header{
		"X-Good":      {"client"},
		"Bad Header":  {"bad-name"},
		"X-Bad-Value": {"ok\r\nX-Injected: yes"},
	}
	upstream := protocol.SyncedAPIUpstream{AuthType: "header", HeaderOverride: map[string]string{
		"X-Good":       "admin",
		"Invalid Name": "bad",
		"X-Injected":   "bad\nvalue",
	}}
	credential := protocol.APIUpstreamCredential{HeaderName: "X-Key\r\nInjected", HeaderValue: "secret"}

	got := (HeaderBuilder{}).Build(client, upstream, credential)

	require.Equal(t, "admin", got.Get("X-Good"))
	require.Empty(t, got.Get("Bad Header"))
	require.Empty(t, got.Get("X-Bad-Value"))
	require.Empty(t, got.Get("Invalid Name"))
	require.Empty(t, got.Get("X-Injected"))
}
