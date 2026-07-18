package upstream

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// KeepaliveConfig 控制上游 TCP 连接的保活探测(用于及时发现半开死连接)。
type KeepaliveConfig struct {
	Idle     time.Duration
	Interval time.Duration
	Count    int
}

// buildDialer 构造带 TCP 保活的拨号器。保活探测对"慢但活"的连接零误杀,
// 只在对端不应答时(半开死连接)于 Idle+Interval*Count 内判死。
func buildDialer(kc KeepaliveConfig) *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: kc.Interval,
		KeepAliveConfig: net.KeepAliveConfig{
			Enable:   true,
			Idle:     kc.Idle,
			Interval: kc.Interval,
			Count:    kc.Count,
		},
	}
}

// NewTransportPool 是 newTransportPool 的导出封装，返回 app.TransportPool 接口。
// 给 agent application 装配用——避免暴露包内私有类型。
func NewTransportPool(maxIdle, maxIdlePerHost int, responseTimeout time.Duration, kc KeepaliveConfig) app.TransportPool {
	return newTransportPool(maxIdle, maxIdlePerHost, responseTimeout, kc)
}

// TransportPool 按 channel.ID|proxy_url 缓存 *http.Transport，让连接池在多次
// upstream 请求间复用。Invalidate 用于 channel 配置变更时让旧 transport 失效。
type transportPool struct {
	mu                  sync.RWMutex
	transports          map[string]*http.Transport
	maxIdleConns        int
	maxIdleConnsPerHost int
	responseTimeout     time.Duration
	keepalive           KeepaliveConfig
}

func newTransportPool(maxIdle, maxIdlePerHost int, responseTimeout time.Duration, kc KeepaliveConfig) *transportPool {
	return &transportPool{
		transports:          map[string]*http.Transport{},
		maxIdleConns:        maxIdle,
		maxIdleConnsPerHost: maxIdlePerHost,
		responseTimeout:     responseTimeout,
		keepalive:           kc,
	}
}

func transportKey(channelID uint, proxyURL string) string {
	return fmt.Sprintf("%d|%s", channelID, proxyURL)
}

// Get 返回 channel 对应的共享 *http.Transport。
// 双重检查锁保证并发场景下同一 channel 只构造一次。
func (p *transportPool) Get(ch *models.Channel) *http.Transport {
	k := transportKey(ch.ID, ch.ProxyURL)
	p.mu.RLock()
	if t, ok := p.transports[k]; ok {
		p.mu.RUnlock()
		return t
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.transports[k]; ok {
		return t
	}
	t := p.build(ch)
	p.transports[k] = t
	return t
}

func (p *transportPool) build(ch *models.Channel) *http.Transport {
	t := &http.Transport{
		DialContext:           buildDialer(p.keepalive).DialContext,
		MaxIdleConns:          p.maxIdleConns,
		MaxIdleConnsPerHost:   p.maxIdleConnsPerHost,
		MaxConnsPerHost:       0, // unlimited，长流式并发不被钳制
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: p.responseTimeout,
	}
	if ch.ProxyURL != "" {
		if u, err := url.Parse(ch.ProxyURL); err == nil {
			t.Proxy = http.ProxyURL(u)
		}
	}
	if ch.DisableKeepalive {
		t.DisableKeepAlives = true
	}
	return t
}

// Invalidate 删除 channelID + oldProxyURL 对应的 transport。
// 调用方应该在 channel.ProxyURL 字段变化时（且只在这种变化时）调用。
// 旧 transport 从 map 删除后，in-flight 请求仍持有指针、继续跑到结束；
// 之后随 GC 回收，所有 keep-alive 连接经 IdleConnTimeout 自然释放。
//
// channel.Key / channel.BaseURL 变化不需要 Invalidate：
// - API key 是每请求在 header 上 set 的，不在 transport 上
// - URL 是每请求重构的
func (p *transportPool) Invalidate(channelID uint, oldProxyURL string) {
	k := transportKey(channelID, oldProxyURL)
	p.mu.Lock()
	transport := p.transports[k]
	delete(p.transports, k)
	p.mu.Unlock()
	if transport != nil {
		transport.CloseIdleConnections()
	}
}

func (p *transportPool) CloseIdleConnections() {
	p.mu.Lock()
	transports := make([]*http.Transport, 0, len(p.transports))
	for key, transport := range p.transports {
		transports = append(transports, transport)
		delete(p.transports, key)
	}
	p.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

func (p *transportPool) ResourceCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.transports)
}
