package events

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestPublishSubscribeUsageReported(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer bus.Close()

	got := make(chan protocol.UsageReport, 1)
	_, err := SubscribeUsageReported(bus, func(ctx context.Context, report protocol.UsageReport) error {
		got <- report
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe usage.reported: %v", err)
	}

	want := protocol.UsageReport{
		AgentID: "agent-1",
		Logs: []protocol.UsageLogEntry{
			{RequestID: "req-1", UserID: 1, ModelName: "gpt-4o"},
		},
	}
	if err := PublishUsageReported(context.Background(), bus, want); err != nil {
		t.Fatalf("publish usage.reported: %v", err)
	}

	select {
	case gotReport := <-got:
		if gotReport.AgentID != want.AgentID {
			t.Fatalf("agent_id=%s want %s", gotReport.AgentID, want.AgentID)
		}
		if len(gotReport.Logs) != 1 || gotReport.Logs[0].RequestID != "req-1" {
			t.Fatalf("unexpected logs payload: %+v", gotReport.Logs)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting usage.reported")
	}
}

func TestPublishSubscribeUserQuotaSync(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer bus.Close()

	got := make(chan protocol.UserQuotaSync, 1)
	_, err := SubscribeUserQuotaSync(bus, func(ctx context.Context, m protocol.UserQuotaSync) error {
		got <- m
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe user.quota_synced: %v", err)
	}

	want := protocol.UserQuotaSync{
		AgentID: "a1",
		Users:   []protocol.SyncedUser{{ID: 3, Quota: 50}},
	}
	if err := PublishUserQuotaSync(context.Background(), bus, want); err != nil {
		t.Fatalf("publish user.quota_synced: %v", err)
	}

	select {
	case gotSync := <-got:
		if gotSync.AgentID != want.AgentID {
			t.Fatalf("agent_id=%s want %s", gotSync.AgentID, want.AgentID)
		}
		if len(gotSync.Users) != 1 || gotSync.Users[0].ID != 3 || gotSync.Users[0].Quota != 50 {
			t.Fatalf("unexpected users payload: %+v", gotSync.Users)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting user.quota_synced")
	}
}

func TestPublishWithGenericTopic(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer bus.Close()

	if err := Publish(context.Background(), bus, UsageReportedTopic, protocol.UsageReport{
		AgentID: "agent-1",
	}); err != nil {
		t.Fatalf("publish with generic topic failed: %v", err)
	}
}

func TestSyncHelpers(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer bus.Close()

	syncPushCh := make(chan protocol.SyncPushParams, 1)
	_, err := SubscribeSyncPushPattern(bus, SyncTokenAllPattern, func(ctx context.Context, push protocol.SyncPushParams) error {
		syncPushCh <- push
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe sync pattern: %v", err)
	}

	push := protocol.SyncPushParams{
		Entity:  EntityToken,
		Action:  "update",
		Data:    []byte(`{"id":1}`),
		Version: 7,
	}
	if err := PublishSyncEvent(context.Background(), bus, push.Entity, push.Action, push); err != nil {
		t.Fatalf("publish sync event: %v", err)
	}

	select {
	case got := <-syncPushCh:
		if got.Entity != push.Entity || got.Action != push.Action || got.Version != push.Version {
			t.Fatalf("sync payload mismatch: got=%+v want=%+v", got, push)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting sync push")
	}

	fullSyncCh := make(chan struct{}, 1)
	_, err = SubscribeSyncFullSyncRequested(bus, func(ctx context.Context) error {
		fullSyncCh <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe sync.full_sync_requested: %v", err)
	}

	if err := PublishSyncFullSyncRequested(context.Background(), bus); err != nil {
		t.Fatalf("publish sync.full_sync_requested: %v", err)
	}

	select {
	case <-fullSyncCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting full sync event")
	}
}
