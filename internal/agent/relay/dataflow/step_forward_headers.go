package dataflow

import (
	"context"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// StepForwardClientHeaders 把客户端入站请求头诚实地叠到已编码的上游请求上。
// 排在 StepEncode 之后(要有 HTTPReq)、StepHeaderOverride 之前(admin/script 最后赢)。
// 无条件装配(always-on):inbound 为 nil 的 describe/单测路径下 Apply 直接 no-op。
type StepForwardClientHeaders struct {
	inbound       http.Header
	crossProtocol bool
}

func (s *StepForwardClientHeaders) Key() string { return "forward_client_headers" }

func (s *StepForwardClientHeaders) Apply(_ context.Context, p *Pass) error {
	if p.HTTPReq == nil || s.inbound == nil {
		return nil
	}
	upstream.ForwardClientHeaders(p.HTTPReq.Header, s.inbound, s.crossProtocol)
	return nil
}

func (s *StepForwardClientHeaders) Describe() StepInfo {
	return baseStepInfos["forward_client_headers"]
}
