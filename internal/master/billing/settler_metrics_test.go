package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"
)

// TestSettleOne_EmitsBYOKRequestTotal_OnlyForPrivateOwner 验证 BYOKRequestTotal
// 只对 OwnerType="private" 行递增，admin 行不染指（避免污染 BYOK 流量观测）。
func TestSettleOne_EmitsBYOKRequestTotal_OnlyForPrivateOwner(t *testing.T) {
	db, appProv := setupTestDB(t)
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	db.Create(&models.User{Username: "alice", Password: "x", Role: 1, Status: 1, Quota: 1_000_000})
	db.Create(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 5.0, OutputPrice: 15.0, Status: 1})
	db.Create(&models.Setting{Key: "byok_billing_mode", Value: "free"})

	metrics.BYOKRequestTotal.Reset()

	settler := NewSettler(appProv, bus, logger)

	// 一条 BYOK 行
	settler.Settle(context.Background(), "agent-1", []protocol.UsageLogEntry{{
		RequestID:        "metric-byok-1",
		UserID:           1,
		OwnerType:        "private",
		PrivateChannelID: 7,
		ModelName:        "gpt-4o",
		PromptTokens:     10,
		CompletionTokens: 5,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})
	// 一条 admin 行
	settler.Settle(context.Background(), "agent-1", []protocol.UsageLogEntry{{
		RequestID:        "metric-admin-1",
		UserID:           1,
		OwnerType:        "admin",
		ChannelID:        9,
		ModelName:        "gpt-4o",
		PromptTokens:     10,
		CompletionTokens: 5,
		Status:           1,
		Timestamp:        time.Now().Unix(),
	}})

	if got := readMetricCounterVec(t, metrics.BYOKRequestTotal, "private", "gpt-4o"); got != 1 {
		t.Fatalf("private/gpt-4o counter = %v, want 1", got)
	}
	if got := readMetricCounterVec(t, metrics.BYOKRequestTotal, "admin", "gpt-4o"); got != 0 {
		t.Fatalf("admin/gpt-4o counter = %v, want 0 (admin must not emit byok_request_total)", got)
	}
}

func readMetricCounterVec(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := cv.WithLabelValues(labels...).(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetCounter().GetValue()
}
