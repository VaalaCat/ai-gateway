package dataflow

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Step 是 channel 内部的一道处理工序。
type Step interface {
	// Key 是稳定标识(如 "model_mapping"),供默认顺序列表引用、供前端图使用。
	Key() string
	// Apply 在 Pass 上加工。返回 error 表示该工序失败(对齐现状:多数工序遇非法配置
	// noop 并打 warn 返回 nil;只有 encode / script 这类会真正失败)。
	Apply(p *Pass) error
	// Describe 自我描述,纯供前端画图使用,不参与运行逻辑。
	Describe() StepInfo
}

// StepInfo 是一道工序的展示信息。不含任何模型名专属字段——模型名只是 Pass 里
// 一个普通字段,"读 Original 还是 Working"是所有字段共用的通用机制。
type StepInfo struct {
	Key       string // 同 Key()
	Title     string // 一句人话标题(中文权威标签;前端按 key 走 i18n,Title 作兜底)
	ConfigRef string // 该工序配置所在页/锚点,前端点击跳转用
	Detail    string // 本工序配置摘要,语言中立数据(计数/字段名/协议 key);裁剪掉的工序为空
}

// StepDeps 是装配 Step 时需要的运行期依赖(脚本引擎、日志等)。
// 只有 StepUpstreamScript 要求 Agent / GinCtx / RCtx 非 nil;其余 Step 不读它们。
type StepDeps struct {
	Agent  app.AgentApplication // StepUpstreamScript 用
	GinCtx *gin.Context         // StepUpstreamScript 用(写回 reject 响应)
	RCtx   *state.RelayContext  // StepUpstreamScript 用(取 UserInfo / Header)
	Logger *zap.Logger          // StepEncode / StepParamOverride 用;可能为 nil
	// 注:StepHeaderOverride 故意不取 logger——它只走 ApplyOverrides 的 header 分支
	// (paramOverride=nil),该分支不可能返回错误(对齐原先单次合并 override 调用,
	// 那次唯一的 warn 也只发生在 param 分支)。
}
