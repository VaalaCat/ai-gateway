package agentproxy

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveAddress_ByTag(t *testing.T) {
	addrs := []Address{
		{URL: "http://10.0.1.1:8139", Tag: "internal"},
		{URL: "http://1.2.3.4:8139", Tag: "public"},
	}
	url, err := ResolveAddress(addrs, "public", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://1.2.3.4:8139" {
		t.Errorf("expected public addr, got %s", url)
	}
}

func TestResolveAddress_ByPreferredTag(t *testing.T) {
	addrs := []Address{
		{URL: "http://10.0.1.1:8139", Tag: "internal"},
		{URL: "http://1.2.3.4:8139", Tag: "public"},
	}
	url, err := ResolveAddress(addrs, "", "internal", "")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://10.0.1.1:8139" {
		t.Errorf("expected internal addr, got %s", url)
	}
}

func TestResolveAddress_Empty(t *testing.T) {
	_, err := ResolveAddress(nil, "", "", "")
	if err == nil {
		t.Error("expected error for empty addresses")
	}
}

func TestResolveAddressCacheFollowsCurrentAddressSet(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	second, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	cacheKey := fmt.Sprintf("resolve-hot-update-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		addrCacheMu.Lock()
		defer addrCacheMu.Unlock()
		for key := range addrCache {
			if key == cacheKey || strings.HasPrefix(key, cacheKey+"\x00") {
				delete(addrCache, key)
			}
		}
	})
	firstURL := "http://" + first.Addr().String()
	secondURL := "http://" + second.Addr().String()

	resolved, err := ResolveAddress([]Address{{URL: firstURL}}, "", "", cacheKey)
	require.NoError(t, err)
	require.Equal(t, firstURL, resolved)
	require.NoError(t, first.Close())

	resolved, err = ResolveAddress([]Address{{URL: secondURL}}, "", "", cacheKey)
	require.NoError(t, err)
	require.Equal(t, secondURL, resolved)
	addrCacheMu.RLock()
	matchingEntries := 0
	for key := range addrCache {
		if key == cacheKey || strings.HasPrefix(key, cacheKey+"\x00") {
			matchingEntries++
		}
	}
	addrCacheMu.RUnlock()
	require.Equal(t, 1, matchingEntries, "one agent cache key must be overwritten instead of growing per address fingerprint")
}

func TestParseAddresses(t *testing.T) {
	raw := `[{"url":"http://10.0.1.1:8139","tag":"internal"}]`
	addrs := ParseAddresses(raw)
	if len(addrs) != 1 || addrs[0].Tag != "internal" {
		t.Errorf("unexpected parse result: %+v", addrs)
	}
}

func TestResolveProxyURL(t *testing.T) {
	if p := ResolveProxyURL("http://agent-proxy", "http://global"); p != "http://agent-proxy" {
		t.Errorf("expected agent proxy, got %s", p)
	}
	if p := ResolveProxyURL("", "http://global"); p != "http://global" {
		t.Errorf("expected global proxy, got %s", p)
	}
}
