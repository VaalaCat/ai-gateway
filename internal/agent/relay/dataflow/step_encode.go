package dataflow

import (
	"bytes"
	"fmt"
	"io"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// EncodeConfig 是翻译成上游格式时真正需要的配置(原 43 字段 ChannelConfig 瘦身后剩余)。
// 不含 Model——上游 model 在 Apply 时取 Pass.Working.Model。
type EncodeConfig struct {
	BaseURL             string
	APIKey              string
	Organization        string
	APIVersion          string
	EndpointPath        string
	SystemPromptInInput bool
	BuiltinToolFallback string
}

func (e EncodeConfig) toChannelConfig(model string) *codec.ChannelConfig {
	return &codec.ChannelConfig{
		BaseURL:             e.BaseURL,
		APIKey:              e.APIKey,
		Model:               model,
		Organization:        e.Organization,
		APIVersion:          e.APIVersion,
		EndpointPath:        e.EndpointPath,
		SystemPromptInInput: e.SystemPromptInInput,
		BuiltinToolFallback: e.BuiltinToolFallback,
	}
}

// StepEncode 把 Working(IR)翻译成上游 HTTP 请求,写入 Pass.HTTPReq / Pass.Body。
// 是翻译前 / 翻译后两段的分界点。
type StepEncode struct {
	enc       EncodeConfig
	oc        codec.OutboundCodec
	proto     codec.Protocol
	channelID uint
	logger    *zap.Logger
}

func (s *StepEncode) Key() string { return "encode" }

func (s *StepEncode) Apply(p *Pass) error {
	if p.Rec != nil {
		p.Rec.WithStage(trace.StageOutboundEncode)
	}
	cfg := s.enc.toChannelConfig(p.Working.Model)
	httpReq, err := s.oc.EncodeRequest(p.Working, cfg)
	if err != nil {
		if p.Rec != nil {
			p.Rec.WithFail(trace.StageOutboundEncode, err)
		}
		return fmt.Errorf("encode outbound request: %w", err)
	}
	upstream.EmitDroppedToolsLog(s.logger, p.Working, s.channelID, p.Working.InboundProtocol, s.proto, cfg.BuiltinToolFallback)

	var body []byte
	if httpReq.Body != nil {
		body, _ = io.ReadAll(httpReq.Body)
		httpReq.Body = io.NopCloser(bytes.NewReader(body))
	}
	p.HTTPReq = httpReq
	p.Body = body
	return nil
}

func (s *StepEncode) Describe() StepInfo {
	info := baseStepInfos["encode"]
	info.Detail = string(s.proto)
	return info
}
