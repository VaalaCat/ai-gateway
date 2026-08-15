// internal/agent/reporter/slim_test.go
package reporter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSlimAndDegradePreserveAutoDisableTrigger(t *testing.T) {
	trigger := autoDisableTrigger()
	base := protocol.UsageLogEntry{
		RequestID: "triggered", TraceData: `{"inbound_body":"large"}`,
		AttemptTraces:       []models.UsageLogTrace{{InboundBody: "large"}},
		FallbackChain:       []models.AttemptRecord{{Seq: 1}},
		AutoDisableTriggers: []attemptproxy.ChannelAutoDisableTrigger{trigger},
	}
	tests := []struct {
		name  string
		apply func(*protocol.UsageLogEntry)
	}{
		{name: "slim", apply: slimEntry},
		{name: "strip trace", apply: func(entry *protocol.UsageLogEntry) { applyDegrade(entry, DegradeStripTrace) }},
		{name: "billing only", apply: func(entry *protocol.UsageLogEntry) { applyDegrade(entry, DegradeBillingOnly) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := base
			test.apply(&got)
			require.Equal(t, []attemptproxy.ChannelAutoDisableTrigger{trigger}, got.AutoDisableTriggers)
			if test.name == "billing only" {
				require.Nil(t, got.FallbackChain, "L3 strips only the fallback chain after L2 trace removal")
			}
		})
	}
}

func TestSlimEntry_BlanksBodyFieldsKeepsRest(t *testing.T) {
	e := protocol.UsageLogEntry{
		RequestID:        "r1",
		PromptTokens:     10,
		CompletionTokens: 20,
		Status:           200,
		AttemptTraces: []models.UsageLogTrace{
			{
				RequestID:          "r1",
				AttemptIndex:       0,
				InboundPath:        "/v1/messages",
				OutboundPath:       "/v1/messages",
				InboundHeaders:     `{"content-type":"application/json"}`,
				OutboundHeaders:    `{"content-type":"application/json"}`,
				InboundBody:        "huge-base64-blob",
				OutboundBody:       "huge-base64-blob",
				ResponseHeaders:    `{"x":"y"}`,
				ResponseBody:       "huge-base64-blob",
				ClientResponseBody: "huge-base64-blob",
				UpstreamStatus:     200,
				ErrorStage:         "",
			},
		},
	}
	slimEntry(&e)

	// billing/identity fields must survive
	if e.RequestID != "r1" || e.PromptTokens != 10 || e.CompletionTokens != 20 || e.Status != 200 {
		t.Fatalf("billing/identity fields must survive slimming, got %+v", e)
	}
	if len(e.AttemptTraces) != 1 {
		t.Fatalf("AttemptTraces length must survive, got %d", len(e.AttemptTraces))
	}
	tr := e.AttemptTraces[0]
	if tr.InboundBody != slimMarker || tr.OutboundBody != slimMarker || tr.ResponseBody != slimMarker || tr.ClientResponseBody != slimMarker {
		t.Fatalf("body fields not blanked with marker: %+v", tr)
	}
	if tr.InboundPath != "/v1/messages" || tr.OutboundPath != "/v1/messages" {
		t.Fatalf("paths must survive slimming: %+v", tr)
	}
	if tr.InboundHeaders == "" || tr.OutboundHeaders == "" || tr.ResponseHeaders == "" {
		t.Fatalf("headers must survive slimming: %+v", tr)
	}
	if tr.UpstreamStatus != 200 || tr.RequestID != "r1" || tr.AttemptIndex != 0 {
		t.Fatalf("billing/identity trace fields must survive slimming: %+v", tr)
	}
}

// TestSlimEntry_DoesNotMutateSharedBackingArray 锁住"操作 peeked 拷贝、不碰 store 原始数据"
// 这条约束:PeekBatch 只做浅拷贝,AttemptTraces 的底层数组和 store 里的原件共享,slimEntry
// 如果直接原地改共享数组,就会把还没投递成功、仍要重试的 store 数据也一起改坏。
func TestSlimEntry_DoesNotMutateSharedBackingArray(t *testing.T) {
	original := []models.UsageLogTrace{{RequestID: "r1", InboundBody: "original"}}
	e := protocol.UsageLogEntry{RequestID: "r1", AttemptTraces: original}
	slimEntry(&e)
	if original[0].InboundBody != "original" {
		t.Fatalf("slimEntry must not mutate caller's backing array in place, got %q", original[0].InboundBody)
	}
}

func TestSlimEntry_TraceDataValidJSON_RemovesBodyKeysKeepsRest(t *testing.T) {
	blob := map[string]any{
		"inbound_body":  "xxx",
		"response_body": "yyy",
		"OutboundBody":  "zzz", // 大小写混合也要命中
		"status":        200,
		"error_stage":   "upstream",
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	e := protocol.UsageLogEntry{TraceData: string(raw)}
	slimEntry(&e)

	var got map[string]any
	if err := json.Unmarshal([]byte(e.TraceData), &got); err != nil {
		t.Fatalf("TraceData should still be valid JSON after slimming: %v", err)
	}
	for _, k := range []string{"inbound_body", "response_body", "OutboundBody"} {
		if _, ok := got[k]; ok {
			t.Fatalf("key %q containing 'body' should have been removed, got %+v", k, got)
		}
	}
	if got["status"] != float64(200) {
		t.Fatalf("non-body keys must survive, got %+v", got)
	}
	if got["error_stage"] != "upstream" {
		t.Fatalf("non-body keys must survive, got %+v", got)
	}
}

func TestSlimEntry_TraceDataInvalidJSON_Cleared(t *testing.T) {
	e := protocol.UsageLogEntry{TraceData: "{not-valid-json"}
	slimEntry(&e)
	if e.TraceData != "" {
		t.Fatalf("invalid TraceData should be cleared to empty string, got %q", e.TraceData)
	}
}

func TestSlimEntry_EmptyTraceData_StaysEmpty(t *testing.T) { // boundary
	e := protocol.UsageLogEntry{TraceData: ""}
	slimEntry(&e)
	if e.TraceData != "" {
		t.Fatalf("empty TraceData should stay empty, got %q", e.TraceData)
	}
}

func TestSlimOversizedEntries_SkipsBatchWithoutOversizedEntry(t *testing.T) {
	batch := []protocol.UsageLogEntry{
		{RequestID: "a", TraceData: "small"},
		{RequestID: "b", TraceData: "also-small"},
	}
	out := slimOversizedEntries(batch, zap.NewNop())
	if out[0].TraceData != "small" || out[1].TraceData != "also-small" {
		t.Fatalf("small entries must not be slimmed when nothing exceeds threshold, got %+v", out)
	}
}

func TestSlimOversizedEntries_SlimsOnlyEntryOverThreshold(t *testing.T) {
	big := strings.Repeat("x", slimThresholdBytes+1024)
	batch := []protocol.UsageLogEntry{
		{RequestID: "big", AttemptTraces: []models.UsageLogTrace{{InboundBody: big}}},
		{RequestID: "small", AttemptTraces: []models.UsageLogTrace{{InboundBody: "small-body"}}},
	}
	out := slimOversizedEntries(batch, zap.NewNop())
	if out[0].AttemptTraces[0].InboundBody != slimMarker {
		t.Fatalf("oversized entry must be slimmed, got %q", out[0].AttemptTraces[0].InboundBody)
	}
	if out[1].AttemptTraces[0].InboundBody != "small-body" {
		t.Fatalf("entry under threshold must be left untouched, got %q", out[1].AttemptTraces[0].InboundBody)
	}
	// 原 batch(peeked 拷贝的来源)不应被就地改写。
	if batch[0].AttemptTraces[0].InboundBody != big {
		t.Fatal("slimOversizedEntries must not mutate the input batch's backing data in place")
	}
}

func TestUploadTimeoutFor(t *testing.T) {
	cases := []struct {
		name string
		size int
		want time.Duration
	}{
		{"zero bytes", 0, 30 * time.Second},
		{"exactly 4MiB", 4 << 20, 150 * time.Second},
		{"huge payload capped", 50 << 20, 5 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uploadTimeoutFor(c.size); got != c.want {
				t.Fatalf("uploadTimeoutFor(%d) = %v, want %v", c.size, got, c.want)
			}
		})
	}
}

func TestSlimOversizedAPITracePreservesIdentityDispatchAndRateLimitFacts(t *testing.T) {
	entry := protocol.APIUsageEntry{
		RequestID: "api-large", APIServiceID: 7, APIRouteID: 9, APIUpstreamID: 11,
		ProviderDispatchKnown: true, ProviderDispatched: true, RequestBytes: 123, ResponseBytes: 456,
		RateLimitDecision: "rejected", RateLimitHitTotal: 1,
		RateLimitHits: []models.RateLimitHit{{LimiterID: 3, Name: "limit", Dimension: "rate/shared", Bucket: "api", Decision: "rejected"}},
		Trace: &protocol.APITraceEntry{ResponseBody: &apiattempt.APIBodyCapture{
			Captured: true, Data: strings.Repeat("x", slimThresholdBytes+1024),
			CapturedBytes: slimThresholdBytes + 1024, TotalBytes: slimThresholdBytes + 1024,
		}},
	}
	usage := protocol.ReportedUsage{API: &entry}

	got := slimOversizedReportedUsage([]protocol.ReportedUsage{usage}, zap.NewNop())

	require.Len(t, got, 1)
	require.NotNil(t, got[0].API)
	require.Equal(t, "api-large", got[0].API.RequestID)
	require.True(t, got[0].API.ProviderDispatchKnown)
	require.True(t, got[0].API.ProviderDispatched)
	require.Equal(t, int64(123), got[0].API.RequestBytes)
	require.Equal(t, int64(456), got[0].API.ResponseBytes)
	require.Equal(t, "rejected", got[0].API.RateLimitDecision)
	require.Equal(t, 1, got[0].API.RateLimitHitTotal)
	require.Less(t, entrySize(got[0]), entrySize(usage))
}

func degradeFixture() protocol.UsageLogEntry {
	return protocol.UsageLogEntry{
		RequestID: "req-x", UserID: 7, ModelName: "m", PromptTokens: 10,
		CompletionTokens: 5, Status: 200, Duration: 1234,
		TraceData:     `{"inbound_body":"xxx","note":"keep"}`,
		FallbackChain: []models.AttemptRecord{{Seq: 1, ChannelID: 3}},
		AttemptTraces: []models.UsageLogTrace{{InboundBody: "big", ResponseBody: "big"}},
		Other:         `{"foo":"bar"}`,
	}
}

func TestApplyDegradeStripTrace(t *testing.T) {
	e := degradeFixture()
	applyDegrade(&e, DegradeStripTrace)
	if e.AttemptTraces != nil || e.TraceData != "" {
		t.Fatal("L2 must clear AttemptTraces and TraceData entirely")
	}
	if len(e.FallbackChain) != 1 {
		t.Fatal("L2 must keep FallbackChain")
	}
	if e.PromptTokens != 10 || e.Status != 200 {
		t.Fatal("billing scalars must survive")
	}
	var other map[string]any
	if err := json.Unmarshal([]byte(e.Other), &other); err != nil || other["degrade"] != "trace_stripped" {
		t.Fatalf("Other must carry degrade marker, got %q", e.Other)
	}
	if other["foo"] != "bar" {
		t.Fatal("existing Other keys must survive marker merge")
	}
	if e.TraceRetentionStatus != models.TraceRetentionStripped {
		t.Fatalf("retention status = %q, want trace_stripped", e.TraceRetentionStatus)
	}
}

func TestApplyDegradeBillingOnly(t *testing.T) {
	e := degradeFixture()
	applyDegrade(&e, DegradeBillingOnly)
	if e.FallbackChain != nil || e.AttemptTraces != nil || e.TraceData != "" {
		t.Fatal("L3 must clear chain and traces")
	}
	if e.RequestID != "req-x" || e.CompletionTokens != 5 || e.Duration != 1234 {
		t.Fatal("billing scalars must survive")
	}
	var other map[string]any
	json.Unmarshal([]byte(e.Other), &other)
	if other["degrade"] != "billing_only" {
		t.Fatalf("marker = %v, want billing_only", other["degrade"])
	}
	if e.TraceRetentionStatus != models.TraceRetentionBillingOnly {
		t.Fatalf("retention status = %q, want billing_only", e.TraceRetentionStatus)
	}
}

func TestSlimEntryMarksBodyTrimmedWithoutDowngrading(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before models.TraceRetentionStatus
		want   models.TraceRetentionStatus
	}{
		{name: "full becomes trimmed", before: models.TraceRetentionFull, want: models.TraceRetentionBodyTrimmed},
		{name: "empty becomes trimmed", want: models.TraceRetentionBodyTrimmed},
		{name: "stripped stays stripped", before: models.TraceRetentionStripped, want: models.TraceRetentionStripped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := degradeFixture()
			e.TraceRetentionStatus = tc.before
			slimEntry(&e)
			if e.TraceRetentionStatus != tc.want {
				t.Fatalf("retention status = %q, want %q", e.TraceRetentionStatus, tc.want)
			}
		})
	}
}

func TestApplyDegradeMonotonicAndIdempotent(t *testing.T) {
	e := degradeFixture()
	applyDegrade(&e, DegradeBillingOnly)
	snap, _ := json.Marshal(e)
	// 再降到更低级别 = no-op(只升不降)
	applyDegrade(&e, DegradeStripTrace)
	again, _ := json.Marshal(e)
	if string(snap) != string(again) {
		t.Fatal("degrading to a lower level must be a no-op")
	}
	// 同级重复 = 幂等
	applyDegrade(&e, DegradeBillingOnly)
	third, _ := json.Marshal(e)
	if string(snap) != string(third) {
		t.Fatal("re-applying same level must be idempotent")
	}
}

func TestApplyDegradeBrokenOtherJSON(t *testing.T) {
	e := degradeFixture()
	e.Other = "not-json"
	applyDegrade(&e, DegradeStripTrace)
	var other map[string]any
	if err := json.Unmarshal([]byte(e.Other), &other); err != nil || other["degrade"] != "trace_stripped" {
		t.Fatalf("broken Other must be replaced by a fresh marker object, got %q", e.Other)
	}
}

func TestApplyDegradeNullOtherJSON(t *testing.T) {
	e := degradeFixture()
	e.Other = "null" // 合法 JSON 但非对象:Unmarshal 不报错却给 nil map,必须不 panic
	applyDegrade(&e, DegradeStripTrace)
	var other map[string]any
	if err := json.Unmarshal([]byte(e.Other), &other); err != nil || other["degrade"] != "trace_stripped" {
		t.Fatalf("null Other must be replaced by a fresh marker object, got %q", e.Other)
	}
}
