package pricing

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
)

const cacheTTL = 1 * time.Hour

type cacheEntry struct {
	data      []byte
	fetchedAt time.Time
}

var (
	cache   = make(map[string]*cacheEntry)
	cacheMu sync.RWMutex
)

// Fetch retrieves data from the given URL with 1-hour in-memory caching.
// If proxyURL is non-empty, it is used as the HTTP proxy; otherwise
// the standard HTTP_PROXY/HTTPS_PROXY environment variables apply.
func Fetch(targetURL string, proxyURL string) ([]byte, error) {
	cacheMu.RLock()
	if e, ok := cache[targetURL]; ok && time.Since(e.fetchedAt) < cacheTTL {
		cacheMu.RUnlock()
		return e.data, nil
	}
	cacheMu.RUnlock()

	client := httputil.NewClient(proxyURL, 15*time.Second)
	resp, err := client.Get(targetURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", targetURL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", targetURL, err)
	}

	cacheMu.Lock()
	cache[targetURL] = &cacheEntry{data: data, fetchedAt: time.Now()}
	cacheMu.Unlock()

	return data, nil
}
