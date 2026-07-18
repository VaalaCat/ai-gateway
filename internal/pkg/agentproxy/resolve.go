package agentproxy

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type Address = app.AgentAddress

// ParseAddresses parses the JSON http_addresses field.
func ParseAddresses(raw string) []Address {
	if raw == "" {
		return nil
	}
	var addrs []Address
	json.Unmarshal([]byte(raw), &addrs)
	return addrs
}

// addressCache caches auto-probed results for 60s.
var (
	addrCache   = make(map[string]addrCacheEntry)
	addrCacheMu sync.RWMutex
)

type addrCacheEntry struct {
	url       string
	expiresAt time.Time
}

// ResolveAddress selects the best address for the target agent.
// Priority: addressTag -> preferredTag -> auto-probe (first reachable, cached 60s).
func ResolveAddress(addresses []Address, addressTag, preferredTag, cacheKey string) (string, error) {
	if len(addresses) == 0 {
		return "", fmt.Errorf("no addresses configured")
	}

	// 1. Match by explicit address tag
	if addressTag != "" {
		for _, a := range addresses {
			if a.Tag == addressTag {
				return a.URL, nil
			}
		}
	}

	// 2. Match by preferred tag
	if preferredTag != "" {
		for _, a := range addresses {
			if a.Tag == preferredTag {
				return a.URL, nil
			}
		}
	}

	// 3. Check cache
	if cacheKey != "" {
		addrCacheMu.RLock()
		entry, ok := addrCache[cacheKey]
		addrCacheMu.RUnlock()
		if ok && time.Now().Before(entry.expiresAt) {
			return entry.url, nil
		}
	}

	// 4. Auto-probe: try each address, return first reachable
	for _, a := range addresses {
		if probeURL(a.URL) {
			if cacheKey != "" {
				addrCacheMu.Lock()
				addrCache[cacheKey] = addrCacheEntry{url: a.URL, expiresAt: time.Now().Add(60 * time.Second)}
				addrCacheMu.Unlock()
			}
			return a.URL, nil
		}
	}

	return "", fmt.Errorf("all %d addresses unreachable", len(addresses))
}

func probeURL(urlStr string) bool {
	host := stripSchemeAndPath(urlStr)
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
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

// ResolveProxyURL returns the proxy URL to use for forwarding.
// Priority: per-agent > global default.
func ResolveProxyURL(agentProxyURL, globalProxyURL string) string {
	if agentProxyURL != "" {
		return agentProxyURL
	}
	return globalProxyURL
}
