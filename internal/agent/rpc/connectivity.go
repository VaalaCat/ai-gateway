package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
)

type ConnTarget struct {
	AgentID       string          `json:"agent_id"`
	Name          string          `json:"name"`
	HTTPAddresses json.RawMessage `json:"http_addresses"`
}

type AddrEntry struct {
	URL string `json:"url"`
	Tag string `json:"tag"`
}

type ProbeResult struct {
	URL       string `json:"url"`
	Tag       string `json:"tag"`
	Reachable bool   `json:"reachable"`
	LatencyMs int    `json:"latency_ms"`
	Error     string `json:"error"`
}

type ConnResult struct {
	TargetAgentID string        `json:"target_agent_id"`
	TargetName    string        `json:"target_name"`
	Results       []ProbeResult `json:"results"`
}

func HandleCheckConnectivity(ctx context.Context, params json.RawMessage, logger *zap.Logger) (any, error) {
	var targets []ConnTarget
	if err := json.Unmarshal(params, &targets); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	results := make([]ConnResult, 0, len(targets))
	for _, t := range targets {
		var addrs []AddrEntry
		json.Unmarshal(t.HTTPAddresses, &addrs)

		cr := ConnResult{
			TargetAgentID: t.AgentID,
			TargetName:    t.Name,
			Results:       make([]ProbeResult, 0, len(addrs)),
		}

		for _, a := range addrs {
			pr := probeAddress(a.URL, a.Tag)
			cr.Results = append(cr.Results, pr)
		}
		results = append(results, cr)
	}
	return results, nil
}

func probeAddress(urlStr, tag string) ProbeResult {
	host := stripSchemeAndPath(urlStr)

	start := time.Now()
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		return ProbeResult{
			URL:       urlStr,
			Tag:       tag,
			Reachable: false,
			LatencyMs: -1,
			Error:     err.Error(),
		}
	}
	conn.Close()

	return ProbeResult{
		URL:       urlStr,
		Tag:       tag,
		Reachable: true,
		LatencyMs: int(elapsed.Milliseconds()),
	}
}

func stripSchemeAndPath(u string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(u, prefix) {
			u = u[len(prefix):]
			break
		}
	}
	if idx := strings.Index(u, "/"); idx >= 0 {
		return u[:idx]
	}
	return u
}
