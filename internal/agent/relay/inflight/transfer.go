package inflight

import (
	"io"
	"time"

	"github.com/gin-gonic/gin"
)

// TransferProgress 记录响应正文的已读、已写字节及距最后一次传输的毫秒数。
// nil 字节表示未上报；nil 空闲时间表示没有观测到正文传输。
type TransferProgress struct {
	UpstreamBodyBytes *int64 `json:"upstream_body_bytes"`
	ClientBodyBytes   *int64 `json:"client_body_bytes"`
	UpstreamIdleMs    *int64 `json:"upstream_idle_ms"`
	ClientIdleMs      *int64 `json:"client_idle_ms"`
}

type transferCounter struct {
	bytes    int64
	lastAt   time.Time
	observed bool
	unknown  bool
}

// ObserveUpstreamBytes 累积从 provider 响应正文实际读取的字节；零值开启观测。
func (e *Entry) ObserveUpstreamBytes(n int) {
	// 1. 同时更新请求和当前候选的计数。
	e.set(func() {
		now := time.Now()
		e.upstreamTransfer.add(n, now)
		if e.currentAttempt != nil {
			e.attemptUpstreamTransfer.add(n, now)
		}
	})
}

// MarkUpstreamUnreported 标记本次候选及请求累计的 provider 字节不可观测。
func (e *Entry) MarkUpstreamUnreported() {
	// 1. 保留未知标记直到请求结束或当前候选切换。
	e.set(func() {
		e.upstreamTransfer.unknown = true
		e.attemptUpstreamTransfer.unknown = true
	})
}

// WrapClientWriter 在请求入口记录底层 writer 实际接受的响应正文字节。
func (e *Entry) WrapClientWriter(writer gin.ResponseWriter) gin.ResponseWriter {
	// 1. 开启客户端计数并安装无正文缓存的 writer。
	if e == nil || writer == nil {
		return writer
	}
	e.observeClientBytes(0)
	return &transferResponseWriter{ResponseWriter: writer, entry: e}
}

// WrapUpstreamBody 记录实际读取的 provider 响应正文字节并保留关闭行为。
func (e *Entry) WrapUpstreamBody(body io.ReadCloser) io.ReadCloser {
	// 1. 开启上游计数并安装无正文缓存的 reader。
	if e == nil {
		return body
	}
	e.ObserveUpstreamBytes(0)
	if body == nil {
		return nil
	}
	return &transferResponseBody{ReadCloser: body, entry: e}
}

func (e *Entry) observeClientBytes(n int) {
	// 1. 同时更新请求和当前候选的计数。
	e.set(func() {
		now := time.Now()
		e.clientTransfer.add(n, now)
		if e.currentAttempt != nil {
			e.attemptClientTransfer.add(n, now)
		}
	})
}

func (counter *transferCounter) add(n int, now time.Time) {
	// 1. 记录观测状态，仅在正文传输时刷新时间。
	counter.observed = true
	if n > 0 {
		counter.bytes += int64(n)
		counter.lastAt = now
	}
}

func transferProgress(upstream, client transferCounter, now time.Time) TransferProgress {
	// 1. 从锁内副本计算两方向的字节数和空闲时间。
	upstreamBytes, upstreamIdle := upstream.values(now)
	clientBytes, clientIdle := client.values(now)
	return TransferProgress{
		UpstreamBodyBytes: upstreamBytes, ClientBodyBytes: clientBytes,
		UpstreamIdleMs: upstreamIdle, ClientIdleMs: clientIdle,
	}
}

func (counter transferCounter) values(now time.Time) (*int64, *int64) {
	// 1. 未观测或不完整的方向不输出数值。
	if !counter.observed || counter.unknown {
		return nil, nil
	}
	bytes := counter.bytes
	if counter.lastAt.IsZero() {
		return &bytes, nil
	}
	// 2. 计算距最后一次正文传输的时间。
	idle := max(int64(0), now.Sub(counter.lastAt).Milliseconds())
	return &bytes, &idle
}

type transferResponseWriter struct {
	gin.ResponseWriter
	entry *Entry
}

func (writer *transferResponseWriter) Write(body []byte) (int, error) {
	// 1. 按底层实际写出长度更新进度。
	n, err := writer.ResponseWriter.Write(body)
	writer.entry.observeClientBytes(n)
	return n, err
}

func (writer *transferResponseWriter) WriteString(body string) (int, error) {
	// 1. 按底层实际写出长度更新进度。
	n, err := writer.ResponseWriter.WriteString(body)
	writer.entry.observeClientBytes(n)
	return n, err
}

type transferResponseBody struct {
	io.ReadCloser
	entry *Entry
}

func (body *transferResponseBody) Read(buffer []byte) (int, error) {
	// 1. 同时返回数据和错误时仍累积已读取的字节。
	n, err := body.ReadCloser.Read(buffer)
	body.entry.ObserveUpstreamBytes(n)
	return n, err
}
