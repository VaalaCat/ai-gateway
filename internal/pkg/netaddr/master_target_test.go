// internal/pkg/netaddr/master_target_test.go
package netaddr

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMasterHTTPTarget(t *testing.T) {
	cases := []struct {
		name, masterURL, path, wantURL string
		wantErr                        bool
	}{
		{"http passthrough", "http://m:8140", "/api/agents/usage", "http://m:8140/api/agents/usage", false},
		{"ws normalized", "ws://m:8140", "/api/agents/usage", "http://m:8140/api/agents/usage", false},
		{"wss normalized", "wss://m", "/api/agents/usage", "https://m/api/agents/usage", false},
		{"unix socket", "unix:/run/gw.sock", "/api/agents/usage", "http://unix/api/agents/usage", false},
		{"garbage", "://bad", "/x", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, url, err := MasterHTTPTarget(c.masterURL, c.path)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got url=%s", url)
				}
				return
			}
			if err != nil || client == nil || url != c.wantURL {
				t.Fatalf("got (%v, %s, %v), want url %s", client, url, err, c.wantURL)
			}
		})
	}
}

func TestAgentRelayTargetPreservesConfiguredPathAndQuery(t *testing.T) {
	t.Parallel()

	got, err := AgentRelayTarget("wss://relay.example/custom/path?region=us&token=secret")
	require.NoError(t, err)
	require.Equal(t, "wss://relay.example/custom/path?region=us&token=secret", got)
}

func TestAgentRelayTargetRejectsNonWebSocketAndEmptyURIs(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"",
		"https://relay.example/ws",
		"unix:/run/gateway.sock",
		"wss://relay.example:0/ws",
		"wss://relay.example:65536/ws",
		"wss://relay.example/" + strings.Repeat("a", 2048),
	} {
		_, err := AgentRelayTarget(raw)
		require.Error(t, err, raw)
	}
}

func TestAgentRelayURIFromMasterURL(t *testing.T) {
	t.Parallel()
	const relayPrefix = "wss://master.example.com/"
	const relaySuffix = "/ws/agent-relay"
	boundaryPath := strings.Repeat("a", 2048-len(relayPrefix)-len(relaySuffix))
	boundaryURI := relayPrefix + boundaryPath + relaySuffix
	require.Len(t, boundaryURI, 2048)

	tests := []struct {
		name      string
		masterURL string
		want      string
		wantOK    bool
	}{
		{
			name:      "https root",
			masterURL: "https://master.example.com",
			want:      "wss://master.example.com/ws/agent-relay",
			wantOK:    true,
		},
		{
			name:      "http path prefix and trailing slash",
			masterURL: "http://master.example.com/gateway/",
			want:      "ws://master.example.com/gateway/ws/agent-relay",
			wantOK:    true,
		},
		{
			name:      "existing control endpoint",
			masterURL: "ws://master.example.com/ws/agent",
			want:      "ws://master.example.com/ws/agent-relay",
			wantOK:    true,
		},
		{
			name:      "prefixed control endpoint preserves query",
			masterURL: "wss://cdn.example.com/gateway/ws/agent?region=jp&token=secret",
			want:      "wss://cdn.example.com/gateway/ws/agent-relay?region=jp&token=secret",
			wantOK:    true,
		},
		{
			name:      "root query",
			masterURL: "https://master.example.com?region=us",
			want:      "wss://master.example.com/ws/agent-relay?region=us",
			wantOK:    true,
		},
		{
			name:      "escaped path prefix",
			masterURL: "https://master.example.com/gateway%2Fedge",
			want:      "wss://master.example.com/gateway%2Fedge/ws/agent-relay",
			wantOK:    true,
		},
		{
			name:      "maximum port",
			masterURL: "https://master.example.com:65535",
			want:      "wss://master.example.com:65535/ws/agent-relay",
			wantOK:    true,
		},
		{
			name:      "canonical URI byte boundary",
			masterURL: "https://master.example.com/" + boundaryPath,
			want:      boundaryURI,
			wantOK:    true,
		},
		{name: "canonical URI byte overflow", masterURL: "https://master.example.com/" + boundaryPath + "a"},
		{name: "zero port", masterURL: "https://master.example.com:0"},
		{name: "overflow port", masterURL: "https://master.example.com:65536"},
		{name: "unix unsupported", masterURL: "unix:/run/gateway.sock"},
		{name: "empty", masterURL: ""},
		{name: "relative", masterURL: "master.example.com/gateway"},
		{name: "unsupported scheme", masterURL: "ftp://master.example.com/gateway"},
		{name: "userinfo rejected", masterURL: "https://user:secret@master.example.com"},
		{name: "fragment rejected", masterURL: "https://master.example.com/gateway#relay"},
		{name: "empty fragment delimiter rejected", masterURL: "https://master.example.com/gateway#"},
		{name: "malformed query rejected", masterURL: "https://master.example.com/gateway?token=%zz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := AgentRelayURIFromMasterURL(tt.masterURL)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
