// Package dataflow 把 channel 内部、llmkit 编码前的公共 IR 请求处理
// 表达成一串独立的 Step,由 ChannelDataFlow 统一运行与描述。
package dataflow

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// Pass 是一次请求在 channel 内部流动时携带的数据。
//
// 各 Step 只作用在 Working(IR)上。Original 是进入本 channel 处理那一刻的
// 冻结快照,只读——
// 需要"原始输入值"(如请求模型 Original.Model)的 Step 读它;默认语义是读 Working。
type Pass struct {
	Original *llmkit.Request // 冻结只读快照
	Working  *llmkit.Request // 被各 Step 加工的工作副本

	Rec *trace.Recorder // 透传给 Step 打 stage / WithFail;可能为 nil

	// Aborted 表示某 Step 已写回响应并要求终止(如脚本 reject)。
	// native 据此直接返回 AbortResult,不再 dispatch。
	Aborted     bool
	AbortResult state.AttemptResult
}

// CloneRequest 深拷贝一个 IR Request,用于冻结 Pass.Original。
// 用 JSON round-trip 实现深拷贝:IR 完全可 JSON 序列化,代价是一次 marshal,
// 对请求路径可忽略。Original 仅供读取稳定输入值(当前只读 Model),
// round-trip 对这些字段无损。
//
// 注意:JSON round-trip 会把 any-typed 字段(如 ResponseFormat、Tool.InputSchema /
// Tool.RawConfig)退化成 map[string]any / []any,丢失原始具体类型。今天没问题——
// Original 只读且只消费 .Model;但若将来有 Step 要读 Original.Tools 之类的字段,
// 应改用真正的深拷贝 / 重新求值,别依赖这里的 round-trip。
//
// 另注:marshal 失败时(不应发生)兜底走浅拷贝,而浅拷贝与入参共享 slice/map 引用,
// 因此这条非预期路径上 Original 不再与 Working 完全独立。
func CloneRequest(r *llmkit.Request) *llmkit.Request {
	if r == nil {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		// 不应发生(IR 可序列化);兜底返回浅拷贝,至少保住标量字段独立。
		cp := *r
		return &cp
	}
	var out llmkit.Request
	if err := json.Unmarshal(b, &out); err != nil {
		cp := *r
		return &cp
	}
	return &out
}
