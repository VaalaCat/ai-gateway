package dataflow

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// StepInjectSystemPrompt 把 channel 配置的 SystemPrompt 注入 Working 消息头部。
// 复用 upstream.InjectSystemPrompt(空 prompt 自动 noop)。
type StepInjectSystemPrompt struct {
	prompt string
}

func (s *StepInjectSystemPrompt) Key() string { return "inject_system_prompt" }

func (s *StepInjectSystemPrompt) Apply(_ context.Context, p *Pass) error {
	upstream.InjectSystemPrompt(p.Working, s.prompt)
	return nil
}

func (s *StepInjectSystemPrompt) Describe() StepInfo { return baseStepInfos["inject_system_prompt"] }
