package upstream

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// TestTransportPoolImplementsInterface 断言 *transportPool 满足 app.TransportPool。
// 编译期检查；签名漂移会让构建失败。
func TestTransportPoolImplementsInterface(t *testing.T) {
	var _ app.TransportPool = (*transportPool)(nil)
}

// TestNewTransportPoolReturnsInterface 验证导出构造函数返回非 nil 接口。
func TestNewTransportPoolReturnsInterface(t *testing.T) {
	pool := NewTransportPool(8, 4, 30*time.Second)
	if pool == nil {
		t.Fatal("NewTransportPool returned nil")
	}
	// happy path：构造完能用接口方法
	tr := pool.Get(&models.Channel{ChannelCore: models.ChannelCore{ID: 42, BaseURL: "https://x"}})
	if tr == nil {
		t.Fatal("pool.Get returned nil transport")
	}
}

// TestTransportPoolGetEmptyChannelFields 覆盖边界：channel 字段为零值，
// Get 不应 panic，并能正常返回 transport。
func TestTransportPoolGetEmptyChannelFields(t *testing.T) {
	pool := NewTransportPool(8, 4, 30*time.Second)
	tr := pool.Get(&models.Channel{ChannelCore: models.ChannelCore{ID: 999}, ProxyURL: ""})
	if tr == nil {
		t.Fatal("Get should return a usable transport for zero-value channel")
	}
}

func TestTransportPool_GetReturnsCachedInstance(t *testing.T) {
	p := newTransportPool(100, 10, 30*time.Second)
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://api.example.com"}}

	t1 := p.Get(ch)
	t2 := p.Get(ch)
	if t1 != t2 {
		t.Errorf("same channel should return same *http.Transport, got different pointers")
	}
}

func TestTransportPool_DifferentChannelsGetDifferentTransports(t *testing.T) {
	p := newTransportPool(100, 10, 30*time.Second)
	ch1 := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://a"}}
	ch2 := &models.Channel{ChannelCore: models.ChannelCore{ID: 2, BaseURL: "https://b"}}

	if p.Get(ch1) == p.Get(ch2) {
		t.Errorf("different channels should get different transports")
	}
}

func TestTransportPool_ReadsConfigFields(t *testing.T) {
	p := newTransportPool(50, 5, 60*time.Second)
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x"}}
	tr := p.Get(ch)
	if tr.MaxIdleConns != 50 {
		t.Errorf("MaxIdleConns = %d, want 50", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 5 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 5", tr.MaxIdleConnsPerHost)
	}
	if tr.ResponseHeaderTimeout != 60*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 60s", tr.ResponseHeaderTimeout)
	}
	if tr.MaxConnsPerHost != 0 {
		t.Errorf("MaxConnsPerHost = %d, want 0 (unlimited)", tr.MaxConnsPerHost)
	}
}

func TestTransportPool_ProxyURLAppliedToTransport(t *testing.T) {
	p := newTransportPool(100, 10, 30*time.Second)
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x"}, ProxyURL: "http://proxy.local:3128"}
	tr := p.Get(ch)
	if tr.Proxy == nil {
		t.Fatal("Proxy should be set when ProxyURL configured")
	}
	// 用一个假请求触发 Proxy func 计算
	req, _ := http.NewRequest("GET", "https://x", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy func: %v", err)
	}
	if u == nil || u.Host != "proxy.local:3128" {
		t.Errorf("proxy url = %v, want proxy.local:3128", u)
	}
}

func TestTransportPool_InvalidateRebuildsAfter(t *testing.T) {
	p := newTransportPool(100, 10, 30*time.Second)
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x"}, ProxyURL: "http://proxy:3128"}

	t1 := p.Get(ch)
	p.Invalidate(ch.ID, ch.ProxyURL)
	t2 := p.Get(ch)
	if t1 == t2 {
		t.Errorf("Invalidate should force rebuild, got same transport")
	}
}

func TestTransportPool_ConcurrentGetCreatesOnce(t *testing.T) {
	p := newTransportPool(100, 10, 30*time.Second)
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x"}}

	var seen sync.Map
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr := p.Get(ch)
			seen.Store(tr, struct{}{})
		}()
	}
	wg.Wait()

	count := 0
	seen.Range(func(_, _ any) bool { count++; return true })
	if count != 1 {
		t.Errorf("expected exactly 1 transport instance, got %d", count)
	}
}

func TestTransportPool_InvalidateOnlyOnProxyChange(t *testing.T) {
	p := newTransportPool(100, 10, 30*time.Second)
	ch1 := &models.Channel{ChannelCore: models.ChannelCore{ID: 1}, ProxyURL: "http://proxy1:3128"}
	t1 := p.Get(ch1)

	// 模拟"ProxyURL 变了"
	p.Invalidate(ch1.ID, ch1.ProxyURL)
	ch1.ProxyURL = "http://proxy2:3128"
	t2 := p.Get(ch1)
	if t1 == t2 {
		t.Errorf("expected new transport after ProxyURL change")
	}
}
