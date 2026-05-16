package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var _ app.Settler = (*Settler)(nil)

const quotaPerDollar = 100_000 // 1 dollar = 100,000 internal units

type Settler struct {
	App    dao.AppProvider
	Bus    app.EventBus
	Logger *zap.Logger
}

func NewSettler(application dao.AppProvider, bus app.EventBus, logger *zap.Logger) *Settler {
	return &Settler{App: application, Bus: bus, Logger: logger}
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
	channelName, channelType := parseChannelSnapshot(entry.Other)

	log := models.UsageLog{
		UserID:           entry.UserID,
		TokenID:          entry.TokenID,
		ChannelID:        entry.ChannelID,
		AgentID:          agentID,
		ModelName:        entry.ModelName,
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
	err = dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		m := dao.NewAdminMutation(txCtx)
		if err := m.UsageLog().Create(&log); err != nil {
			if isDuplicateRequestIDError(err) {
				return nil
			}
			return err
		}

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
			if err := m.Billing().UpsertTokenDaily(&log); err != nil {
				return err
			}
			if err := m.Billing().UpsertChannelDaily(&log); err != nil {
				return err
			}
			if entry.TokenName == "__system_test__" {
				s.Logger.Info("skipping quota deduction for ownerless system test usage", logFields...)
			} else {
				s.Logger.Warn("skipping quota deduction for ownerless usage with no owner", logFields...)
			}
			return nil
		}

		if err := m.Billing().UpsertTokenDaily(&log); err != nil {
			return err
		}
		if err := m.Billing().UpsertChannelDaily(&log); err != nil {
			return err
		}

		// Deduct user quota
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
