package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var _ app.Settler = (*Settler)(nil)

const quotaPerDollar = 100_000 // 1 dollar = 100,000 internal units

// UsageAggregator is the narrow contract settler uses to hand off post-commit
// aggregation. Production implementation is *billing.Aggregator; tests inject
// mocks. Submit is called AFTER the settler transaction commits; never call
// it before commit (a rollback would leave the aggregator with phantom counts).
type UsageAggregator interface {
	Submit(log *models.UsageLog)
}

// noopAggregator is the zero-value backing for NewSettler (T2.8 legacy
// callers that haven't migrated to NewSettlerWithAggregator). Production
// always supplies a real aggregator via NewSettlerWithAggregator in
// master/server.go (T2.9).
type noopAggregator struct{}

func (noopAggregator) Submit(*models.UsageLog) {}

type Settler struct {
	App        dao.AppProvider
	Bus        app.EventBus
	Logger     *zap.Logger
	Aggregator UsageAggregator
}

func NewSettler(application dao.AppProvider, bus app.EventBus, logger *zap.Logger) *Settler {
	return NewSettlerWithAggregator(application, bus, logger, noopAggregator{})
}

// NewSettlerWithAggregator constructs a Settler that hands off post-commit
// aggregation to the supplied UsageAggregator. agg MUST be non-nil; callers
// that want to disable aggregation should use NewSettler (which wires
// noopAggregator).
func NewSettlerWithAggregator(application dao.AppProvider, bus app.EventBus, logger *zap.Logger, agg UsageAggregator) *Settler {
	if agg == nil {
		agg = noopAggregator{}
	}
	return &Settler{
		App:        application,
		Bus:        bus,
		Logger:     logger,
		Aggregator: agg,
	}
}

// Start subscribes to usage.reported events
func (s *Settler) Start() {
	events.SubscribeUsageReported(s.Bus, func(ctx context.Context, report protocol.UsageReport) error {
		s.Settle(ctx, report.AgentID, report.Logs)
		return nil
	})
}

// Settle is the void, fire-and-forget wrapper used by the legacy ws
// usage.reported subscriber: per-entry failures are logged inside
// SettleBatch and swallowed here, matching the async publish semantics
// (agent already got its ack before this runs).
func (s *Settler) Settle(ctx context.Context, agentID string, logs []protocol.UsageLogEntry) {
	_ = s.SettleBatch(ctx, agentID, logs)
}

// SettleBatch 逐条结算并返回聚合错误;成功条目照常落库/扣费,失败条目计入返回值。
// 供同步摄取路径(HTTP ingest)用:调用方以非 nil 返回决定 5xx,让 agent 重试整批;
// 已成功条目靠 request_id 去重天然幂等,重试不会双计。
func (s *Settler) SettleBatch(ctx context.Context, agentID string, logs []protocol.UsageLogEntry) error {
	var errs []error
	for _, entry := range logs {
		if err := s.settleOne(ctx, agentID, entry); err != nil {
			s.Logger.Error("settle failed",
				zap.String("request_id", entry.RequestID), zap.Error(err))
			errs = append(errs, fmt.Errorf("request %s: %w", entry.RequestID, err))
		}
	}
	s.publishQuotaSync(ctx, agentID, logs)
	return errors.Join(errs...)
}

// publishQuotaSync 结算完一批 usage 后，把受影响 user 的最新 Quota 定向回送给来源
// agent，让活跃 user 的本地缓存余额保持新鲜。冷门 user 走 token-load 旁路负载刷新，
// 不依赖此通道。
func (s *Settler) publishQuotaSync(ctx context.Context, agentID string, logs []protocol.UsageLogEntry) {
	seen := map[uint]bool{}
	var ids []uint
	for _, e := range logs {
		if e.UserID != 0 && !seen[e.UserID] {
			seen[e.UserID] = true
			ids = append(ids, e.UserID)
		}
	}
	if len(ids) == 0 {
		return
	}
	q := dao.NewAdminQuery(dao.NewContext(s.App))
	users := make([]protocol.SyncedUser, 0, len(ids))
	for _, id := range ids {
		u, err := q.User().GetByID(id)
		if err != nil || u == nil {
			continue
		}
		gid := u.GroupID
		if gid == 0 {
			gid = 1
		}
		users = append(users, protocol.SyncedUser{ID: u.ID, GroupID: gid, Quota: u.Quota})
	}
	if len(users) > 0 {
		if err := events.PublishUserQuotaSync(ctx, s.Bus, protocol.UserQuotaSync{AgentID: agentID, Users: users}); err != nil {
			s.Logger.Warn("publish user.quota_synced failed", zap.Error(err))
		}
	}
}

func (s *Settler) settleOne(ctx context.Context, agentID string, entry protocol.UsageLogEntry) error {
	daoCtx := dao.NewContext(s.App)
	q := dao.NewAdminQuery(daoCtx)

	// Deduplicate by request_id
	exists, err := q.UsageLog().ExistsByRequestID(entry.RequestID)
	if err != nil {
		return err
	}
	if exists {
		s.Logger.Debug("usage settle dedup hit (retransmit correctly ignored)",
			zap.String("request_id", entry.RequestID), zap.String("agent_id", agentID))
		return nil // already processed
	}

	// Look up model pricing
	var mc models.ModelConfig
	if strings.TrimSpace(entry.ModelName) != "" {
		if found, err := q.ModelConfig().GetByModelName(entry.ModelName); err == nil {
			mc = *found
		} else {
			// No pricing configured, log with zero cost
			s.Logger.Warn("no pricing for model", zap.String("model", entry.ModelName))
		}
	}

	// Calculate costs (prices are USD / 1M tokens)
	inputCost := int64(float64(entry.PromptTokens) * mc.InputPrice / 1_000_000 * float64(quotaPerDollar))
	outputCost := int64(float64(entry.CompletionTokens) * mc.OutputPrice / 1_000_000 * float64(quotaPerDollar))

	cacheReadCost := int64(0)
	if entry.CacheReadTokens > 0 && mc.CacheReadPrice > 0 {
		cacheReadCost = int64(float64(entry.CacheReadTokens) * mc.CacheReadPrice / 1_000_000 * float64(quotaPerDollar))
	}
	cacheWriteCost := int64(0)
	if entry.CacheWriteTokens > 0 && mc.CacheWritePrice > 0 {
		cacheWriteCost = int64(float64(entry.CacheWriteTokens) * mc.CacheWritePrice / 1_000_000 * float64(quotaPerDollar))
	}

	totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost

	// 原价快照(乘任何因子之前),供 UsageLog.Raw* 落库,让计费明细弹窗能展示
	// "原价 → ×因子 → 实付" 完整公式。指针入库;老行 NULL → 前端降级不出公式。
	rawIn, rawOut, rawCR, rawCW := inputCost, outputCost, cacheReadCost, cacheWriteCost

	// 计费因子三分支:免费渠道 → BYOK 模式 → 公共倍率。billingFactor 记实际生效倍率。
	// Daily rollups are always written regardless of mode—BYOK users still need
	// per-channel/per-token usage stats in their portal even when costs are zero.
	priceRatio := 1.0 // 写入 UsageLog.PriceRatio;private 行保持 1,公共行取快照
	var billingFactor float64
	free := entry.Free // 仅公共渠道可能为 true;private 行恒 false
	switch {
	case free:
		inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost = 0, 0, 0, 0, 0
		billingFactor = 0
	default:
		var byokMode string
		inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost, byokMode, billingFactor =
			s.applyByokBillingMode(q, entry, inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost)
		// 仅对 BYOK ("private") 行 +1。byokMode 为 "" 表示非 private 行，跳过 metric。
		if byokMode != "" {
			metrics.BYOKRequestTotal.WithLabelValues(entry.OwnerType, entry.ModelName).Inc()
		} else {
			// 非 private(公共 channel):应用请求时倍率快照。
			inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost, priceRatio =
				applyChannelPriceRatio(entry, inputCost, outputCost, cacheReadCost, cacheWriteCost)
			billingFactor = priceRatio
		}
	}

	channelName, channelType := parseChannelSnapshot(entry.Other)
	executionAgentID := agentID
	if entry.ExecutionAgentID != nil {
		executionAgentID = *entry.ExecutionAgentID
	}

	log := models.UsageLog{
		UserID:             entry.UserID,
		TokenID:            entry.TokenID,
		ChannelID:          entry.ChannelID,
		PrivateChannelID:   entry.PrivateChannelID,
		OwnerType:          entry.OwnerType,
		AgentID:            executionAgentID,
		RouteSourceAgentID: entry.RouteSourceAgentID,
		AgentRouteID:       entry.AgentRouteID,
		AgentRoutePath:     entry.AgentRoutePath,
		ModelName:          entry.ModelName,
		CreatedAt:          entry.Timestamp,
		PromptTokens:       entry.PromptTokens,
		CompletionTokens:   entry.CompletionTokens,
		InputCost:          inputCost,
		OutputCost:         outputCost,
		TotalCost:          totalCost,
		CacheReadCost:      cacheReadCost,
		CacheWriteCost:     cacheWriteCost,
		RawInputCost:       &rawIn,
		RawOutputCost:      &rawOut,
		RawCacheReadCost:   &rawCR,
		RawCacheWriteCost:  &rawCW,
		BillingFactor:      &billingFactor,
		Free:               free,
		PriceRatio:         priceRatio,
		IsStream:           entry.IsStream,
		Duration:           entry.Duration,
		RequestID:          entry.RequestID,
		ClientIP:           entry.ClientIP,
		TokenName:          entry.TokenName,
		ChannelName:        channelName,
		ChannelType:        channelType,
		UpstreamModel:      entry.UpstreamModel,
		FirstResponseMs:    entry.FirstResponseMs,
		CacheReadTokens:    entry.CacheReadTokens,
		CacheWriteTokens:   entry.CacheWriteTokens,
		InboundProtocol:    entry.InboundProtocol,
		OutboundProtocol:   entry.OutboundProtocol,
		UseLegacy:          entry.UseLegacy,
		Status:             entry.Status,
		ErrorMessage:       entry.ErrorMessage,
		Other:              entry.Other,
		TokenSource:        entry.TokenSource,
		RoutingName:        entry.RoutingName,
		AffinityStatus:     entry.AffinityStatus,
		AffinityRecorded:   entry.AffinityRecorded,
		ErrorStage:         entry.ErrorStage,
		InboundDecodeMs:    entry.InboundDecodeMs,
		OutboundEncodeMs:   entry.OutboundEncodeMs,
		UpstreamDispatchMs: entry.UpstreamDispatchMs,
		UpstreamDecodeMs:   entry.UpstreamDecodeMs,
		ClientEncodeMs:     entry.ClientEncodeMs,
		FallbackChain:      datatypes.NewJSONSlice(entry.FallbackChain),
		RateLimitDecision:  entry.RateLimitDecision,
		RateLimitWaitMs:    entry.RateLimitWaitMs,
		RateLimitReason:    entry.RateLimitReason,
		RateLimitHits:      datatypes.NewJSONSlice(entry.RateLimitHits),
	}

	var depleted bool
	var inserted bool
	err = dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		m := dao.NewAdminMutation(txCtx)
		if err := m.UsageLog().Create(&log); err != nil {
			if isDuplicateRequestIDError(err) {
				// inserted stays false; do NOT Submit a duplicate to aggregator.
				return nil
			}
			return err
		}
		inserted = true

		// Write trace data if present (any request with trace enabled or errors).
		// New path: AttemptTraces carries one row per candidate attempt.
		// Backward-compat path: if AttemptTraces is empty but legacy TraceData is
		// non-empty, write a single trace row at attempt_index=0.
		if len(entry.AttemptTraces) > 0 {
			wroteTrace := false
			for _, tr := range entry.AttemptTraces {
				tr := tr // avoid loop-variable aliasing
				tr.RequestID = entry.RequestID
				// AttemptIndex 由 agent 端按真实候选序号填好(可能不连续:空快照被跳过),
				// 这里不再按切片位置重排,以免与链路 seq 错位。
				if err := m.UsageLog().CreateTrace(&tr); err != nil {
					s.Logger.Warn("failed to write attempt trace",
						zap.String("request_id", entry.RequestID),
						zap.Int("attempt_index", tr.AttemptIndex),
						zap.Error(err),
					)
				} else {
					wroteTrace = true
				}
			}
			if wroteTrace {
				txCtx.GetDB().Model(&log).Update("has_trace", true)
			}
		} else if entry.TraceData != "" {
			// Legacy single-trace path (backward compat for old agents).
			var trace models.UsageLogTrace
			if err := json.Unmarshal([]byte(entry.TraceData), &trace); err == nil {
				trace.RequestID = entry.RequestID
				trace.AttemptIndex = 0
				if err := m.UsageLog().CreateTrace(&trace); err != nil {
					s.Logger.Warn("failed to write trace data",
						zap.String("request_id", entry.RequestID),
						zap.Error(err),
					)
				} else {
					// Mark the usage log as having trace data
					txCtx.GetDB().Model(&log).Update("has_trace", true)
				}
			}
		}

		if entry.UserID == 0 {
			logFields := []zap.Field{
				zap.String("request_id", entry.RequestID),
				zap.String("token_name", entry.TokenName),
				zap.Int64("total_cost", totalCost),
			}
			if entry.TokenName == "__system_test__" {
				s.Logger.Info("skipping quota deduction for ownerless system test usage", logFields...)
			} else {
				s.Logger.Warn("skipping quota deduction for ownerless usage with no owner", logFields...)
			}
			return nil
		}

		// Deduct user quota. BYOK free mode lands here with totalCost=0 and
		// naturally short-circuits.
		if totalCost > 0 {
			remaining, err := m.User().DeductQuota(entry.UserID, totalCost)
			if err != nil {
				return err
			}
			depleted = remaining < 0
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Aggregation moves OUT of the tx: settler's tx is now UsageLog + trace +
	// DeductQuota only. Hand the committed log to the in-memory aggregator
	// for batched 3-table upsert. Must be after commit so a rollback can't
	// leave the aggregator double-counting. Skip on duplicate-request-id
	// short-circuit (inserted=false) to avoid over-counting retries.
	if inserted {
		s.Aggregator.Submit(&log)
	}

	// Event OUTSIDE transaction
	if depleted {
		if err := events.PublishUserQuotaDepleted(ctx, s.Bus, models.User{ID: entry.UserID}); err != nil {
			s.Logger.Error("publish user.quota_depleted failed", zap.Error(err))
		}
	}
	return nil
}

type channelSnapshot struct {
	ChannelName string `json:"channel_name"`
	ChannelType int    `json:"channel_type"`
}

func parseChannelSnapshot(other string) (string, int) {
	if strings.TrimSpace(other) == "" {
		return "", 0
	}

	var snapshot channelSnapshot
	if err := json.Unmarshal([]byte(other), &snapshot); err != nil {
		return "", 0
	}
	return snapshot.ChannelName, snapshot.ChannelType
}

func isDuplicateRequestIDError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	// Fallback: string matching for drivers that don't map to ErrDuplicatedKey
	lower := strings.ToLower(err.Error())
	return (strings.Contains(lower, "unique") || strings.Contains(lower, "duplicate")) &&
		(strings.Contains(lower, "request_id") || strings.Contains(lower, "idx_usage_logs_request_id"))
}

// applyByokBillingMode adjusts per-bucket costs by the configured BYOK billing
// mode when entry.OwnerType == "private":
//   - "free" (default): all costs zeroed. Daily rollups are still written so the
//     BYOK user sees request/token counts in their portal; quota deduction is
//     naturally skipped because totalCost=0 fails the `if totalCost > 0` guard
//     in settleOne.
//   - "service_fee": each bucket multiplied by byok_service_fee_ratio.
//
// Non-private entries are passed through unchanged with mode="" so callers can
// distinguish "not a BYOK row" from a BYOK row in a specific mode.
func (s *Settler) applyByokBillingMode(q dao.AdminQuery, entry protocol.UsageLogEntry,
	inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost int64) (
	adjInput, adjOutput, adjCacheRead, adjCacheWrite, adjTotal int64, mode string, factor float64) {

	if entry.OwnerType != "private" {
		return inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost, "", 0
	}

	mode = q.Setting().LookupString(consts.SettingKeyBYOKBillingMode, consts.BYOKDefaultBillingMode)
	if mode == consts.BYOKBillingModeServiceFee {
		ratio := q.Setting().LookupFloat(consts.SettingKeyBYOKServiceFeeRatio, consts.BYOKDefaultServiceFeeRatioFloat)
		// Truncate each bucket independently, then recompute total as their
		// sum so that total_cost == input + output + cache_read + cache_write
		// holds exactly. Discounting the original total separately would drift
		// by one due to float64→int64 truncation.
		adjInput = int64(float64(inputCost) * ratio)
		adjOutput = int64(float64(outputCost) * ratio)
		adjCacheRead = int64(float64(cacheReadCost) * ratio)
		adjCacheWrite = int64(float64(cacheWriteCost) * ratio)
		adjTotal = adjInput + adjOutput + adjCacheRead + adjCacheWrite
		return adjInput, adjOutput, adjCacheRead, adjCacheWrite, adjTotal, mode, ratio
	}
	// free / unknown mode: zero all costs.
	return 0, 0, 0, 0, 0, mode, 0
}

// applyChannelPriceRatio 对非 private(公共 channel)行应用请求时的倍率快照。
// ratio 来自 entry.PriceRatio:
//   - <= 0 → 原价(旧 agent 未发 / channel 未配 / 显式 0),归一到 1.0(不改成本)
//   - 其他 → 逐桶乘,再把 total 重算为四桶之和(避免单独折 total 的截断漂移,
//     与 applyByokBillingMode 同款不变式 total == input+output+cacheR+cacheW)
//
// 返回归一后实际所用 ratio,供 settleOne 写入 UsageLog.PriceRatio。
func applyChannelPriceRatio(entry protocol.UsageLogEntry,
	inputCost, outputCost, cacheReadCost, cacheWriteCost int64) (
	adjInput, adjOutput, adjCacheRead, adjCacheWrite, adjTotal int64, ratio float64) {

	ratio = entry.PriceRatio
	if ratio <= 0 { // 0/负数都视为原价
		ratio = 1.0
	}
	adjInput = int64(float64(inputCost) * ratio)
	adjOutput = int64(float64(outputCost) * ratio)
	adjCacheRead = int64(float64(cacheReadCost) * ratio)
	adjCacheWrite = int64(float64(cacheWriteCost) * ratio)
	adjTotal = adjInput + adjOutput + adjCacheRead + adjCacheWrite
	return
}
