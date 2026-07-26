package billing

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"github.com/stretchr/testify/require"
)

func TestBuildLogBatchBuildsRequestTraceAndObservationDeltas(t *testing.T) {
	log := models.UsageLog{
		RequestID: "complete", UserID: 7, ChannelID: 3, ModelName: "m", AgentID: "agent",
		Status: 1, IsStream: true, CompletionTokens: 100, Duration: 2000, FirstResponseMs: 400,
		InboundDecodeMs: 2, CreatedAt: 1784786400,
	}
	entry := protocol.UsageLogEntry{RequestID: log.RequestID, AttemptTraces: []models.UsageLogTrace{{AttemptIndex: 4, UpstreamStatus: 200}}}

	batch, err := buildLogBatch(log, entry)
	require.NoError(t, err)
	require.Equal(t, "complete", batch.Request.RequestID)
	require.True(t, batch.Request.HasTrace)
	require.Len(t, batch.Traces, 1)
	require.Equal(t, 4, batch.Traces[0].AttemptIndex)
	require.Len(t, batch.Hourly, 1)
	require.Equal(t, int64(1), batch.Hourly[0].StreamRequestCount)
	require.Equal(t, int64(2), batch.Hourly[0].SumInboundDecodeMs)
	require.Len(t, batch.Duration, 1)
	require.Len(t, batch.TTFT, 1)
	require.Len(t, batch.TPS, 1)
	require.Len(t, batch.UserTTFT, 1)
	require.Len(t, batch.UserTPS, 1)
	require.Equal(t, int64(1), histogramSlot(t, batch.TTFT[0], ttfthist.SlotIndex(400)))
	wantTPS := int64(100) * 1000 / 1600
	require.Equal(t, int64(1), histogramSlot(t, batch.TPS[0], tpshist.SlotIndex(wantTPS)))
}

func TestBuildLogBatchFailedZeroTokenRequestOnlyBuildsBaseDeltas(t *testing.T) {
	batch, err := buildLogBatch(models.UsageLog{RequestID: "failed", Status: 0, CreatedAt: 1784786400}, protocol.UsageLogEntry{RequestID: "failed"})
	require.NoError(t, err)
	require.Len(t, batch.Hourly, 1)
	require.Equal(t, int64(1), batch.Hourly[0].FailedCount)
	require.Empty(t, batch.Duration)
	require.Empty(t, batch.TTFT)
	require.Empty(t, batch.TPS)
	require.Empty(t, batch.UserTTFT)
	require.Empty(t, batch.UserTPS)
}

func TestBuildLogBatchRejectsMalformedLegacyTrace(t *testing.T) {
	_, err := buildLogBatch(models.UsageLog{RequestID: "bad-trace"}, protocol.UsageLogEntry{RequestID: "bad-trace", TraceData: "{"})
	require.ErrorContains(t, err, "decode legacy trace")
}

func TestBuildLogBatchUsesIndependentTTFTAndTPSSamples(t *testing.T) {
	tests := []struct {
		name         string
		log          models.UsageLog
		wantTTFT     bool
		wantTPS      bool
		wantUserHist bool
	}{
		{name: "TTFT allows zero completion", log: models.UsageLog{UserID: 1, Status: 1, IsStream: true, FirstResponseMs: 400, Duration: 400}, wantTTFT: true, wantUserHist: true},
		{name: "TPS allows zero first response", log: models.UsageLog{UserID: 1, Status: 1, IsStream: true, FirstResponseMs: 0, Duration: 1000, CompletionTokens: 10}, wantTPS: true, wantUserHist: true},
		{name: "anonymous has global hist only", log: models.UsageLog{UserID: 0, Status: 1, IsStream: true, FirstResponseMs: 100, Duration: 1000, CompletionTokens: 10}, wantTTFT: true, wantTPS: true},
		{name: "failed excluded", log: models.UsageLog{UserID: 1, Status: 0, IsStream: true, FirstResponseMs: 100, Duration: 1000, CompletionTokens: 10}},
		{name: "nonstream excluded", log: models.UsageLog{UserID: 1, Status: 1, IsStream: false, FirstResponseMs: 100, Duration: 1000, CompletionTokens: 10}},
		{name: "zero values excluded", log: models.UsageLog{UserID: 1, Status: 1, IsStream: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.log.RequestID = test.name
			batch, err := buildLogBatch(test.log, protocol.UsageLogEntry{RequestID: test.name})
			require.NoError(t, err)
			require.Equal(t, test.wantTTFT, len(batch.TTFT) == 1)
			require.Equal(t, test.wantTPS, len(batch.TPS) == 1)
			wantUserTTFT := test.wantTTFT && test.wantUserHist
			wantUserTPS := test.wantTPS && test.wantUserHist
			require.Equal(t, wantUserTTFT, len(batch.UserTTFT) == 1)
			require.Equal(t, wantUserTPS, len(batch.UserTPS) == 1)
			if test.wantTTFT {
				require.Equal(t, int64(1), batch.Hourly[0].StreamRequestCount)
				require.Equal(t, int64(test.log.FirstResponseMs), batch.Hourly[0].SumFirstResponseMs)
			} else {
				require.Zero(t, batch.Hourly[0].StreamRequestCount)
				require.Zero(t, batch.Hourly[0].SumFirstResponseMs)
			}
			if test.wantTPS {
				require.Equal(t, int64(test.log.CompletionTokens), batch.Hourly[0].SumStreamCompletionTokens)
				require.Equal(t, int64(test.log.Duration-test.log.FirstResponseMs), batch.Hourly[0].SumGenerationMs)
			} else {
				require.Zero(t, batch.Hourly[0].SumStreamCompletionTokens)
				require.Zero(t, batch.Hourly[0].SumGenerationMs)
			}
		})
	}
}

func histogramSlot(t *testing.T, row any, slot int) int64 {
	t.Helper()
	value := reflect.ValueOf(row)
	field := value.FieldByName(fmt.Sprintf("H%d", slot))
	require.True(t, field.IsValid())
	return field.Int()
}
