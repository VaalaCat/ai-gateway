package dataflow

import (
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// StepParamOverride 把 channel 的 ParamOverride 浅合并进上游 body 字节。
// 复用 upstream.ApplyOverrides 的 param 分支;失败 graceful degrade(warn + 保留原 body),
// 对齐现状 applyNativeOverrides。
type StepParamOverride struct {
	params map[string]any
	logger *zap.Logger
}

func (s *StepParamOverride) Key() string { return "param_override" }

func (s *StepParamOverride) Apply(p *Pass) error {
	newBody, err := upstream.ApplyOverrides(p.HTTPReq, p.Body, s.params, nil)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("apply param override failed", zap.Error(err))
		}
	}
	p.Body = newBody
	return nil
}

func (s *StepParamOverride) Describe() StepInfo {
	info := baseStepInfos["param_override"]
	info.Detail = joinSortedKeys(s.params)
	return info
}

// StepHeaderOverride 把 channel 的 HeaderOverride 应用到上游 HTTP header。
// 复用 upstream.ApplyOverrides 的 header 分支(不动 body,不返回错误)。
type StepHeaderOverride struct {
	headers map[string]any
}

func (s *StepHeaderOverride) Key() string { return "header_override" }

func (s *StepHeaderOverride) Apply(p *Pass) error {
	upstream.ApplyOverrides(p.HTTPReq, p.Body, nil, s.headers) //nolint:errcheck // header 分支不返回错误
	return nil
}

func (s *StepHeaderOverride) Describe() StepInfo {
	info := baseStepInfos["header_override"]
	info.Detail = joinSortedKeys(s.headers)
	return info
}

// joinSortedKeys 把 map 的 key 排序后逗号连接(确定性输出,供前端展示)。
func joinSortedKeys(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
