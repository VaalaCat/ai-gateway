// Package publish 是 relay pipeline 的第 4 阶段（最末端）：把累加好的 RelayContext 状态
// 一次性收成 protocol.UsageLogEntry 并经 EventBus 发布 usage.completed。
//
// 设计目标：UsageLog 写入的单一出口。所有路径（成功 / ctxBuild fail / plan fail /
// execute fail / forwarded）汇聚到 Publisher.Publish() 一处，按 rctx.State.FailPhase
// 选填字段集，避免老 handler.go 每个分支重复拼装 UsageLogEntry。
//
// rctx → UsageLogEntry 的纯投影逻辑抽到 usage_view.go 的 ProjectUsageEntry，
// 供 inflight registry 复用；publish 专属的副作用（affinity 记录）留在本文件。
package publish

import (
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Publisher 是 UsageLog 写入的单一出口。按 rctx.State.FailPhase 选填字段集，
// 通过 EventBus 发布 protocol.UsageLogEntry。所有路径（成功 / ctxBuild fail /
// plan fail / execute fail / forwarded）汇聚到 Publish() 一处。
type Publisher struct {
	bus      app.EventBus
	logger   *zap.Logger
	affinity *affinity.Engine
}

// NewPublisher 构造 Publisher。logger 为 nil 时 Publish 默默吞 publish 错误。
func NewPublisher(bus app.EventBus, logger *zap.Logger, aff *affinity.Engine) *Publisher {
	return &Publisher{bus: bus, logger: logger, affinity: aff}
}

// Publish 把 rctx 累加的状态收成 UsageLogEntry 并发 usage.completed。
// usage_log 的内容完全由 ProjectUsageEntry 投影决定；publish 在投影之后追加唯一的
// 专属副作用——affinity 记录（recordAffinity），它读 e.CacheRead/WriteTokens
// 所以必须在投影之后、发布之前执行，顺序与重构前一致。
func (p *Publisher) Publish(rctx *state.RelayContext) {
	if rctx == nil || rctx.State == nil || rctx.Context == nil || rctx.Request == nil {
		return
	}
	e := ProjectUsageEntry(rctx)
	p.recordAffinity(rctx, &e)
	if err := events.PublishUsageCompleted(rctx.Request.Context(), p.bus, e); err != nil {
		if p.logger != nil {
			p.logger.Error("publish usage.completed failed", zap.Error(err))
		}
	}
}

// recordAffinity 推导三态 AffinityStatus 并在成功 + 有缓存活动时记录/续期。
// engine 为 nil 或开关关闭时全部留空，不影响原行为。
//
// 只在执行阶段挑出了 channel（Used.Channel != nil）时才有意义——对齐重构前
// fillExecution 仅在 u.Channel != nil 分支末尾调用 fillAffinity 的行为。
func (p *Publisher) recordAffinity(rctx *state.RelayContext, e *protocol.UsageLogEntry) {
	u := rctx.State.Execution.Used
	if u.Channel == nil {
		return
	}
	success := rctx.State.Execution.Err == nil
	if p.affinity == nil || rctx.Input.UserInfo == nil || rctx.Input.UserInfo.UserID == 0 {
		return
	}
	uid := rctx.Input.UserInfo.UserID
	ovr := u.Channel.Affinity.Data()
	dec := p.affinity.Decide(affinity.Subject{
		UserID: uid, RealModel: u.RealModel,
		ChannelEnabled: ovr.Enabled, ChannelTTLSec: ovr.TTLSec,
	})
	if !dec.Apply && !dec.Record {
		return
	}
	switch {
	case u.ByAffinity:
		e.AffinityStatus = affinity.StatusHit
	case rctx.State.Plan.HadAffinityEntry:
		e.AffinityStatus = affinity.StatusFallback
	default:
		e.AffinityStatus = affinity.StatusNone
	}
	if success && dec.Record && (e.CacheReadTokens > 0 || e.CacheWriteTokens > 0) {
		p.affinity.Remember(affinity.Key{UserID: uid, RealModel: u.RealModel}, u.Source, u.SourceID, ovr.TTLSec)
		e.AffinityRecorded = true
	}
}
