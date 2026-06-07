package limiter

import (
	"fmt"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

// streamGuard 负责 stream 排队期间开流 + 周期保活帧。
// 拿到名额/失败前必须 stopKeepalive 干净停（join goroutine），避免与 backend 并发写同一 writer。
type streamGuard struct {
	rctx        *state.RelayContext
	keepaliveMs int
	opened      bool
	running     bool
	stop        chan struct{}
	done        chan struct{}
}

func newStreamGuard(rctx *state.RelayContext, keepaliveMs int) *streamGuard {
	if keepaliveMs <= 0 {
		keepaliveMs = 15000
	}
	return &streamGuard{rctx: rctx, keepaliveMs: keepaliveMs}
}

// ensureOpen 写一次 SSE 头 + 首帧保活并置 StreamOpened；幂等。
func (s *streamGuard) ensureOpen() {
	if s.opened {
		return
	}
	s.opened = true
	s.rctx.State.StreamOpened = true
	h := s.rctx.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	writeKeepalive(s.rctx.Writer) // 首帧触发 WriteHeader(200)，状态码自此钉死
}

func (s *streamGuard) startKeepalive() {
	if s.running {
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.loop()
}

// stopKeepalive 停止并 join goroutine；调用方在交给 backend 写之前必须先调。
func (s *streamGuard) stopKeepalive() {
	if !s.running {
		return
	}
	close(s.stop)
	<-s.done
	s.running = false
}

func (s *streamGuard) loop() {
	defer close(s.done)
	t := time.NewTicker(time.Duration(s.keepaliveMs) * time.Millisecond)
	defer t.Stop()
	var ctxDone <-chan struct{}
	if s.rctx.Context != nil && s.rctx.Request != nil {
		ctxDone = s.rctx.Request.Context().Done()
	}
	for {
		select {
		case <-s.stop:
			return
		case <-ctxDone:
			return
		case <-t.C:
			writeKeepalive(s.rctx.Writer)
		}
	}
}

func writeKeepalive(w http.ResponseWriter) {
	fmt.Fprint(w, ": keepalive\n\n")
	flush(w)
}

// WriteSSEError 在已开流上写一帧错误事件（按入站协议格式），不回 JSON。inbound 用 string(codec.Protocol)。
// codec.Protocol 实际取值为 "claude"/"openai_chat"/"openai_responses"/"gemini"；
// 测试用 "anthropic"/"openai" 别名同样落到对应分支。
func WriteSSEError(w http.ResponseWriter, inbound, msg string) {
	switch inbound {
	case "anthropic", "claude":
		fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":%q}}\n\n", msg)
	default: // openai 家族（openai_chat / openai_responses / 别名 openai）
		fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"rate_limit_error\"}}\n\n", msg)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
	flush(w)
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
