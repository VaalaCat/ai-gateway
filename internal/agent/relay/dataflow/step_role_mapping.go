package dataflow

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
)

// StepRoleMapping 按 channel 的 role_mapping 改写 Working 消息的 Role。
// 按 Original.Model(请求模型,= 今天 cfg.InboundModel)匹配规则,不按映射后的模型。
type StepRoleMapping struct {
	rules *transform.RoleMappingConfig
}

func (s *StepRoleMapping) Key() string { return "role_mapping" }

func (s *StepRoleMapping) Apply(_ context.Context, p *Pass) error {
	if s.rules == nil {
		return nil
	}
	mapping := s.rules.ResolveRoleMapping(p.Original.Model)
	if mapping == nil {
		return nil
	}
	transform.ApplyRoleMapping(p.Working.Messages, mapping)
	return nil
}

func (s *StepRoleMapping) Describe() StepInfo {
	info := baseStepInfos["role_mapping"]
	if s.rules != nil {
		info.Detail = strconv.Itoa(len(s.rules.Default) + len(s.rules.Models))
	}
	return info
}
