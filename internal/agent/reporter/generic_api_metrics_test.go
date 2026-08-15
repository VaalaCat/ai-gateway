package reporter

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type recordingGenericAPIUsageMetrics struct {
	dropped atomic.Uint64
	slimmed atomic.Uint64
}

func (m *recordingGenericAPIUsageMetrics) AddUsageDropped(count uint64) { m.dropped.Add(count) }
func (m *recordingGenericAPIUsageMetrics) AddTraceSlimmed(count uint64) { m.slimmed.Add(count) }

// behavior change: Generic API drop metrics count the actual evicted union
// member, not the shared queue's aggregate drop delta.
func TestGenericAPIPendingDropMetricsOnlyCountEvictedAPI(t *testing.T) {
	tests := []struct {
		name        string
		entries     []protocol.ReportedUsage
		wantAPIDrop uint64
	}{
		{name: "API only", entries: []protocol.ReportedUsage{apiUsage("api-1"), apiUsage("api-2")}, wantAPIDrop: 1},
		{name: "LLM only", entries: []protocol.ReportedUsage{llmUsage("llm-1"), llmUsage("llm-2")}},
		{name: "mixed evicts LLM", entries: []protocol.ReportedUsage{llmUsage("llm-first"), apiUsage("api-last")}},
		{name: "mixed evicts API", entries: []protocol.ReportedUsage{apiUsage("api-first"), llmUsage("llm-last")}, wantAPIDrop: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metrics := &recordingGenericAPIUsageMetrics{}
			store := NewMemPendingUsageStoreWithLimits(deliveryqueue.Limits{MaxEntries: 1}, zap.NewNop(), metrics)
			store.AppendReported(tc.entries)
			require.Equal(t, tc.wantAPIDrop, metrics.dropped.Load())
			require.Equal(t, uint64(1), store.Dropped())
		})
	}
}

func TestGenericAPIPendingDropMetricsCountMultipleActualEvictions(t *testing.T) {
	metrics := &recordingGenericAPIUsageMetrics{}
	first := llmUsage("small-1")
	second := llmUsage("small-2")
	limit := int64(entrySize(first) + entrySize(second))
	store := NewMemPendingUsageStoreWithLimits(deliveryqueue.Limits{MaxEntries: 10, MaxBytes: limit}, zap.NewNop(), metrics)
	store.AppendReported([]protocol.ReportedUsage{first, second})

	oversized := apiUsage("oversized-api")
	oversized.API.Trace = &apiattempt.APIExecutionTrace{
		RequestBody: &apiattempt.APIBodyCapture{Data: strings.Repeat("x", int(limit)+1024)},
	}
	store.AppendReported([]protocol.ReportedUsage{oversized})

	require.Equal(t, uint64(1), metrics.dropped.Load())
	require.Equal(t, uint64(3), store.Dropped())
	require.Zero(t, store.Len())
}

// behavior change: retry eviction uses the same typed accounting as pending.
func TestGenericAPIRetryDropMetricsOnlyCountEvictedAPI(t *testing.T) {
	tests := []struct {
		name        string
		entries     []protocol.ReportedUsage
		wantAPIDrop uint64
	}{
		{name: "API only", entries: []protocol.ReportedUsage{apiUsage("api-1"), apiUsage("api-2")}, wantAPIDrop: 1},
		{name: "LLM only", entries: []protocol.ReportedUsage{llmUsage("llm-1"), llmUsage("llm-2")}},
		{name: "mixed evicts LLM", entries: []protocol.ReportedUsage{llmUsage("llm-first"), apiUsage("api-last")}},
		{name: "mixed evicts API", entries: []protocol.ReportedUsage{apiUsage("api-first"), llmUsage("llm-last")}, wantAPIDrop: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metrics := &recordingGenericAPIUsageMetrics{}
			queue := newRetryQueue(1, zap.NewNop(), metrics)
			queue.pushReported(tc.entries, 1, testNow())
			require.Equal(t, tc.wantAPIDrop, metrics.dropped.Load())
			require.Equal(t, uint64(1), queue.Dropped())
		})
	}
}

// behavior change: only an actually oversized API entry contributes to the
// Generic API trace slim counter, even in a mixed retry batch.
func TestGenericAPITraceSlimMetricsOnlyCountOversizedAPI(t *testing.T) {
	tests := []struct {
		name        string
		items       []retryItem
		wantSlimmed uint64
	}{
		{name: "oversized API", items: []retryItem{{entry: oversizedAPIUsage("api-large"), attempts: 1}}, wantSlimmed: 1},
		{name: "oversized LLM", items: []retryItem{{entry: oversizedLLMUsage("llm-large"), attempts: 1}}},
		{name: "mixed oversized", items: []retryItem{{entry: oversizedLLMUsage("llm-mixed"), attempts: 1}, {entry: oversizedAPIUsage("api-mixed"), attempts: 1}}, wantSlimmed: 1},
		{name: "API below size boundary", items: []retryItem{{entry: apiUsage("api-small"), attempts: 1}}},
		{name: "API below retry boundary", items: []retryItem{{entry: oversizedAPIUsage("api-young"), attempts: 0}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metrics := &recordingGenericAPIUsageMetrics{}
			uploader := &UsageUploader{
				cfg: UploaderConfig{Logger: zap.NewNop(), Metrics: metrics}, slimBodyAfter: func() int { return 1 },
			}
			wire := uploader.slimRetryWireCopies(tc.items)
			require.Len(t, wire, len(tc.items))
			require.Equal(t, tc.wantSlimmed, metrics.slimmed.Load())
			require.Equal(t, tc.wantSlimmed, uploader.TraceSlimmed())
		})
	}
}

func TestGenericAPIPendingDropMetricsRemainExactUnderConcurrency(t *testing.T) {
	const entriesPerType = 32
	metrics := &recordingGenericAPIUsageMetrics{}
	store := NewMemPendingUsageStoreWithLimits(deliveryqueue.Limits{MaxEntries: 1}, zap.NewNop(), metrics)
	p := pool.New().WithMaxGoroutines(8)
	for index := range entriesPerType {
		p.Go(func() {
			store.AppendReported([]protocol.ReportedUsage{apiUsage("api-concurrent-" + strconv.Itoa(index))})
		})
		p.Go(func() {
			store.AppendReported([]protocol.ReportedUsage{llmUsage("llm-concurrent-" + strconv.Itoa(index))})
		})
	}
	p.Wait()

	remaining := store.PeekReportedBatch(1, true)
	require.Len(t, remaining, 1)
	wantAPIDrops := uint64(entriesPerType)
	if remaining[0].IsAPI() {
		wantAPIDrops--
	}
	require.Equal(t, wantAPIDrops, metrics.dropped.Load())
	require.Equal(t, uint64(entriesPerType*2-1), store.Dropped())
}

func apiUsage(id string) protocol.ReportedUsage {
	return protocol.ReportedUsage{API: &protocol.APIUsageEntry{RequestID: id}}
}

func llmUsage(id string) protocol.ReportedUsage {
	return protocol.ReportedUsage{LLM: &protocol.UsageLogEntry{RequestID: id}}
}

func oversizedAPIUsage(id string) protocol.ReportedUsage {
	usage := apiUsage(id)
	usage.API.Trace = &apiattempt.APIExecutionTrace{
		RequestBody: &apiattempt.APIBodyCapture{Data: strings.Repeat("x", slimThresholdBytes+1024)},
	}
	return usage
}

func oversizedLLMUsage(id string) protocol.ReportedUsage {
	usage := llmUsage(id)
	usage.LLM.TraceData = strings.Repeat("x", slimThresholdBytes+1024)
	return usage
}

func testNow() time.Time { return time.Unix(1, 0) }
