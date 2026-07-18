package rpc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/reporter"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

func newTestReporter(t *testing.T) (*reporter.Reporter, *reporter.MemPendingUsageStore) {
	t.Helper()
	store := reporter.NewMemPendingUsageStore(100, zap.NewNop())
	up, err := reporter.NewUsageUploader(reporter.UploaderConfig{
		Store:         store,
		MasterURL:     "http://127.0.0.1:1", // Invalid URL; not used in these tests
		AgentID:       "agent-t",
		Secret:        "sec-t",
		FlushInterval: 20 * time.Millisecond,
		BatchMax:      10,
		BackoffMaxSec: func() int { return 1 },
		Logger:        zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := eventbus.NewMemoryBus()
	return reporter.New(bus, zap.NewNop(), store, up, nil), store
}

func TestHandleUsageQueue_ReporterNil(t *testing.T) {
	_, err := HandleUsageQueue(nil)
	if err == nil {
		t.Fatal("want error when reporter is nil")
	}
	if err.Error() != "reporter not ready" {
		t.Fatalf("error = %q, want 'reporter not ready'", err.Error())
	}
}

func TestHandleUsageQueueOp_ReporterNil(t *testing.T) {
	_, err := HandleUsageQueueOp(nil, json.RawMessage("{}"))
	if err == nil {
		t.Fatal("want error when reporter is nil")
	}
	if err.Error() != "reporter not ready" {
		t.Fatalf("error = %q, want 'reporter not ready'", err.Error())
	}
}

func TestHandleUsageQueueOp_InvalidParams(t *testing.T) {
	r, _ := newTestReporter(t)
	_, err := HandleUsageQueueOp(r, json.RawMessage("not-json"))
	if err == nil {
		t.Fatal("want error for invalid JSON params")
	}
}

func TestHandleUsageQueue_HappyPath(t *testing.T) {
	r, store := newTestReporter(t)
	store.Append([]protocol.UsageLogEntry{{RequestID: "e1", Timestamp: 100}})

	snap, err := HandleUsageQueue(r)
	if err != nil {
		t.Fatalf("HandleUsageQueue error: %v", err)
	}
	qs, ok := snap.(reporter.QueueSnapshot)
	if !ok {
		t.Fatalf("want reporter.QueueSnapshot, got %T", snap)
	}
	if qs.StoreLen != 1 {
		t.Fatalf("StoreLen = %d, want 1", qs.StoreLen)
	}
}

func TestHandleUsageQueueOp_HappyPath(t *testing.T) {
	r, _ := newTestReporter(t)
	params := UsageQueueOpParams{Op: "retry_now", RequestIDs: nil, Level: 0}
	paramsJSON, _ := json.Marshal(params)

	res, err := HandleUsageQueueOp(r, paramsJSON)
	if err != nil {
		t.Fatalf("HandleUsageQueueOp error: %v", err)
	}
	opRes, ok := res.(UsageQueueOpResult)
	if !ok {
		t.Fatalf("want UsageQueueOpResult, got %T", res)
	}
	// Empty retry queue, retry_now affects 0 items
	if opRes.Affected != 0 {
		t.Fatalf("Affected = %d, want 0 (empty queue)", opRes.Affected)
	}
}
