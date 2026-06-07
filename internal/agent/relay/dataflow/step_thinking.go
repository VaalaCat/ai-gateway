package dataflow

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// StepThinkingPassthrough 在 SendBack(Working.Model)=true 时补占位 thinking block。
// 仅 openai_chat 出站时进链(由装配器决定)。
type StepThinkingPassthrough struct {
	rules upstream.ThinkingRules
}

func (s *StepThinkingPassthrough) Key() string { return "thinking_passthrough" }

func (s *StepThinkingPassthrough) Apply(p *Pass) error {
	if s.rules.SendBack(p.Working.Model) {
		transform.ApplyThinkingPassthrough(p.Working.Messages)
	}
	return nil
}

func (s *StepThinkingPassthrough) Describe() StepInfo { return baseStepInfos["thinking_passthrough"] }

// StepThinkingStrip 在 SendBack(Working.Model)=false 时剥离 thinking block。
// 仅 openai_chat 出站时进链。
type StepThinkingStrip struct {
	rules upstream.ThinkingRules
}

func (s *StepThinkingStrip) Key() string { return "thinking_strip" }

func (s *StepThinkingStrip) Apply(p *Pass) error {
	if !s.rules.SendBack(p.Working.Model) {
		transform.ApplyThinkingStrip(p.Working.Messages)
	}
	return nil
}

func (s *StepThinkingStrip) Describe() StepInfo { return baseStepInfos["thinking_strip"] }
