// internal/pkg/netaddr/master_target.go
package netaddr

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

var errInvalidAgentRelayTarget = errors.New("invalid agent relay target")

const (
	agentControlWebSocketPath = "/ws/agent"
	agentRelayWebSocketPath   = "/ws/agent-relay"
)

// MasterHTTPTarget 把 agent 配置的 master_url("http://host:port" | "ws://..." | "unix:/path")
// 解析成可用的 HTTP client + 完整目标 URL。enrollment 与 usage 上传共用。
func MasterHTTPTarget(masterURL, path string) (*http.Client, string, error) {
	if strings.HasPrefix(masterURL, "unix:") {
		return UnixHTTPClient(masterURL), "http://unix" + path, nil
	}
	raw := strings.Replace(masterURL, "ws://", "http://", 1)
	raw = strings.Replace(raw, "wss://", "https://", 1)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", err
	}
	u.Path = path
	return &http.Client{}, u.String(), nil
}

// AgentRelayTarget validates and canonicalizes the dedicated relay websocket URI.
// Path and query are intentionally preserved because deployments may route the
// tunnel through a non-default ingress path or signed query parameters.
func AgentRelayTarget(raw string) (string, error) {
	parsed, err := wire.ParseRelayURI(raw)
	if err != nil {
		return "", errInvalidAgentRelayTarget
	}
	return parsed.URI.String(), nil
}

// AgentRelayURIFromMasterURL derives the dedicated relay endpoint beside the
// control endpoint while preserving deployment-specific path prefixes and query.
func AgentRelayURIFromMasterURL(masterURL string) (string, bool) {
	if masterURL == "" || strings.TrimSpace(masterURL) != masterURL || strings.HasPrefix(masterURL, unixPrefix) {
		return "", false
	}
	u, err := url.Parse(masterURL)
	if err != nil || !u.IsAbs() || u.Hostname() == "" || u.User != nil || strings.Contains(masterURL, "#") {
		return "", false
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", false
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return "", false
	}

	escapedPath := strings.TrimRight(u.EscapedPath(), "/")
	if strings.HasSuffix(escapedPath, agentControlWebSocketPath) {
		escapedPath = strings.TrimSuffix(escapedPath, agentControlWebSocketPath)
	}
	escapedPath += agentRelayWebSocketPath
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", false
	}
	u.Path = path
	u.RawPath = escapedPath
	target, err := AgentRelayTarget(u.String())
	if err != nil {
		return "", false
	}
	return target, true
}
