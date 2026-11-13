package dataflow

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// StepThinkingPassthrough 按映射前后的模型名匹配规则，开启时补空 thinking。
// openai_chat 和 claude_messages 出站时进链，规则未开启时保留原消息。
type StepThinkingPassthrough struct {
	rules upstream.ThinkingRules
}

func (s *StepThinkingPassthrough) Key() string { return "thinking_passthrough" }

func (s *StepThinkingPassthrough) Apply(_ context.Context, p *Pass) error {
	// 1. 按配置顺序匹配映射前后的模型名，开启时补空 thinking。
	originalModel := p.Working.Model
	if p.Original != nil {
		originalModel = p.Original.Model
	}
	if s.rules.SendBack(originalModel, p.Working.Model) {
		transform.ApplyThinkingPassthrough(p.Working.Messages)
	}
	return nil
}

func (s *StepThinkingPassthrough) Describe() StepInfo { return baseStepInfos["thinking_passthrough"] }

// StepThinkingStrip 按映射前后的模型名匹配规则，未开启回填时剥离 thinking。
// 仅 openai_chat 出站时进链。
type StepThinkingStrip struct {
	rules upstream.ThinkingRules
}

func (s *StepThinkingStrip) Key() string { return "thinking_strip" }

func (s *StepThinkingStrip) Apply(_ context.Context, p *Pass) error {
	// 1. 按配置顺序匹配映射前后的模型名，未开启回填时剥离 thinking。
	originalModel := p.Working.Model
	if p.Original != nil {
		originalModel = p.Original.Model
	}
	if !s.rules.SendBack(originalModel, p.Working.Model) {
		p.Working.Messages = transform.ApplyThinkingStrip(p.Working.Messages)
	}
	return nil
}

func (s *StepThinkingStrip) Describe() StepInfo { return baseStepInfos["thinking_strip"] }
