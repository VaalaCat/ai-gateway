package dataflow

import "fmt"

// ChannelDataFlow 是某条 channel 内部、按序排好的一串工序。
// relay 路径用 Run 跑请求;前端链路图用 Describe 报出每道工序说明。
type ChannelDataFlow struct {
	steps []Step
}

// Run 按序应用所有 Step。任一 Step 返回 error 立即中止并包裹返回;
// 任一 Step 置 p.Aborted(如脚本 reject,响应已写回)也立即停止后续 Step。
func (f *ChannelDataFlow) Run(p *Pass) error {
	for _, s := range f.steps {
		if err := s.Apply(p); err != nil {
			return fmt.Errorf("step %s: %w", s.Key(), err)
		}
		if p.Aborted {
			return nil
		}
	}
	return nil
}

// Describe 报出每道工序的展示信息(顺序与执行顺序一致)。
func (f *ChannelDataFlow) Describe() []StepInfo {
	out := make([]StepInfo, 0, len(f.steps))
	for _, s := range f.steps {
		out = append(out, s.Describe())
	}
	return out
}
