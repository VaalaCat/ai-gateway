package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
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

func (s *Settler) Settle(ctx context.Context, agentID string, logs []protocol.UsageLogEntry) {
	for _, entry := range logs {
		if err := s.settleOne(ctx, agentID, entry); err != nil {
			s.Logger.Error("settle failed",
				zap.String("request_id", entry.RequestID),
				zap.Error(err),
			)
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

	// BYOK billing mode: adjust per-bucket costs (free → zero, service_fee → ratio).
	// Daily rollups are always written regardless of mode—BYOK users still need
	// per-channel/per-token usage stats in their portal even when costs are zero.
	inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost, byokMode :=
		s.applyByokBillingMode(q, entry, inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost)
	// 仅对 BYOK ("private") 行 +1。byokMode 为 "" 表示非 private 行，跳过 metric。
	if byokMode != "" {
		metrics.BYOKRequestTotal.WithLabelValues(entry.OwnerType, entry.ModelName).Inc()
	}

	channelName, channelType := parseChannelSnapshot(entry.Other)

	log := models.UsageLog{
		UserID:           entry.UserID,
		TokenID:          entry.TokenID,
		ChannelID:        entry.ChannelID,
		PrivateChannelID: entry.PrivateChannelID,
		OwnerType:        entry.OwnerType,
		AgentID:          agentID,
		ModelName:        entry.ModelName,
		CreatedAt:        entry.Timestamp,
		PromptTokens:     entry.PromptTokens,
		CompletionTokens: entry.CompletionTokens,
		InputCost:        inputCost,
		OutputCost:       outputCost,
		TotalCost:        totalCost,
		IsStream:         entry.IsStream,
		Duration:         entry.Duration,
		RequestID:        entry.RequestID,
		ClientIP:         entry.ClientIP,
		TokenName:        entry.TokenName,
		ChannelName:      channelName,
		ChannelType:      channelType,
		UpstreamModel:    entry.UpstreamModel,
		FirstResponseMs:  entry.FirstResponseMs,
		CacheReadTokens:  entry.CacheReadTokens,
		CacheWriteTokens: entry.CacheWriteTokens,
		InboundProtocol:  entry.InboundProtocol,
		OutboundProtocol: entry.OutboundProtocol,
		UseLegacy:        entry.UseLegacy,
		Status:           entry.Status,
		ErrorMessage:     entry.ErrorMessage,
		Other:            entry.Other,
		TokenSource:        entry.TokenSource,
		RoutingName:        entry.RoutingName,
		ErrorStage:         entry.ErrorStage,
		InboundDecodeMs:    entry.InboundDecodeMs,
		OutboundEncodeMs:   entry.OutboundEncodeMs,
		UpstreamDispatchMs: entry.UpstreamDispatchMs,
		UpstreamDecodeMs:   entry.UpstreamDecodeMs,
		ClientEncodeMs:     entry.ClientEncodeMs,
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

		// Write trace data if present (any request with trace enabled or errors)
		if entry.TraceData != "" {
			var trace models.UsageLogTrace
			if err := json.Unmarshal([]byte(entry.TraceData), &trace); err == nil {
				trace.RequestID = entry.RequestID
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
	adjInput, adjOutput, adjCacheRead, adjCacheWrite, adjTotal int64, mode string) {

	if entry.OwnerType != "private" {
		return inputCost, outputCost, cacheReadCost, cacheWriteCost, totalCost, ""
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
		return adjInput, adjOutput, adjCacheRead, adjCacheWrite, adjTotal, mode
	}
	// free / unknown mode: zero all costs.
	return 0, 0, 0, 0, 0, mode
}

