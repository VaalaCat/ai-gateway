package dataflow

import (
	"context"
	"strconv"
)

// StepModelMapping 把 Working.Model 经 channel 的 model_mapping 映射成上游 model。
// 对应原 state.ApplyModelMapping。mapping 在装配时解析好;空表 / 未命中 → 不变。
type StepModelMapping struct {
	mapping map[string]string
}

func (s *StepModelMapping) Key() string { return "model_mapping" }

func (s *StepModelMapping) Apply(_ context.Context, p *Pass) error {
	if mapped, ok := s.mapping[p.Working.Model]; ok {
		p.Working.Model = mapped
	}
	return nil
}

func (s *StepModelMapping) Describe() StepInfo {
	info := baseStepInfos["model_mapping"]
	info.Detail = strconv.Itoa(len(s.mapping))
	return info
}
