package dataflow

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"go.uber.org/zap"
)

// StepToolCompatibility 按映射后的模型名转换工具定义。
type StepToolCompatibility struct {
	rules  upstream.ToolCompatibilityRules
	logger *zap.Logger
}

func (s *StepToolCompatibility) Key() string { return "tool_compatibility" }

func (s *StepToolCompatibility) Apply(_ context.Context, p *Pass) error {
	// 1. 转换本次请求的工具定义副本。
	compatibility := s.rules.Match(p.Working.Model)
	tools, changes := llmkit.ConvertToolSchemas(p.Working.Tools, compatibility.Profile)
	p.Working.Tools = tools

	// 2. 记录被移除的字段路径。
	if len(changes) > 0 && s.logger != nil {
		s.logger.Info("tool schema compatibility applied",
			zap.String("model", p.Working.Model), zap.String("profile", string(compatibility.Profile)),
			zap.Any("changes", changes))
	}

	return nil
}

func (s *StepToolCompatibility) Describe() StepInfo {
	info := baseStepInfos[s.Key()]
	info.Detail = strconv.Itoa(s.rules.Len())
	return info
}
