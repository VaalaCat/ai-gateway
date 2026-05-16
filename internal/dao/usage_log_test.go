package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestUsageLogDAO_Admin(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).UsageLog()
	m := NewAdminMutation(ctx).UsageLog()

	now := time.Now().Unix()

	log1 := &models.UsageLog{UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4", RequestID: "req-1", TotalCost: 100, Status: 1, CreatedAt: now}
	log2 := &models.UsageLog{UserID: 2, TokenID: 2, ChannelID: 2, ModelName: "claude-3", RequestID: "req-2", TotalCost: 200, Status: 1, CreatedAt: now}
	log3 := &models.UsageLog{UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4", RequestID: "req-3", TotalCost: 0, Status: 0, CreatedAt: now - 86400*30}
	for _, l := range []*models.UsageLog{log1, log2, log3} {
		if err := db.Select("*").Create(l).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("List all", func(t *testing.T) {
		logs, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UsageLogListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		_ = logs
	})

	t.Run("List with UserID filter", func(t *testing.T) {
		uid := uint(1)
		logs, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UsageLogListFilter{UserID: &uid})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		_ = logs
	})

	t.Run("List with ModelName filter", func(t *testing.T) {
		logs, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UsageLogListFilter{ModelName: "claude-3"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1, got %d", total)
		}
		_ = logs
	})

	t.Run("GetByRequestID", func(t *testing.T) {
		log, err := q.GetByRequestID("req-1")
		if err != nil {
			t.Fatalf("GetByRequestID: %v", err)
		}
		if log.TotalCost != 100 {
			t.Fatalf("expected 100, got %d", log.TotalCost)
		}
	})

	t.Run("GetByRequestID not found", func(t *testing.T) {
		_, err := q.GetByRequestID("nonexistent")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("ExistsByRequestID true", func(t *testing.T) {
		exists, err := q.ExistsByRequestID("req-1")
		if err != nil {
			t.Fatalf("ExistsByRequestID: %v", err)
		}
		if !exists {
			t.Fatal("expected true")
		}
	})

	t.Run("ExistsByRequestID false", func(t *testing.T) {
		exists, err := q.ExistsByRequestID("nonexistent")
		if err != nil {
			t.Fatalf("ExistsByRequestID: %v", err)
		}
		if exists {
			t.Fatal("expected false")
		}
	})

	t.Run("Create", func(t *testing.T) {
		log := &models.UsageLog{UserID: 1, RequestID: "req-new", TotalCost: 50, CreatedAt: now}
		if err := m.Create(log); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if log.ID == 0 {
			t.Fatal("expected ID set")
		}
	})

	t.Run("CreateTrace and GetTraceByRequestID", func(t *testing.T) {
		trace := &models.UsageLogTrace{RequestID: "req-1", InboundPath: "/v1/chat", OutboundPath: "/api/chat", UpstreamStatus: 200}
		if err := m.CreateTrace(trace); err != nil {
			t.Fatalf("CreateTrace: %v", err)
		}
		got, err := q.GetTraceByRequestID("req-1")
		if err != nil {
			t.Fatalf("GetTraceByRequestID: %v", err)
		}
		if got.InboundPath != "/v1/chat" {
			t.Fatalf("expected /v1/chat, got %s", got.InboundPath)
		}
	})

	t.Run("GetTraceByRequestID not found", func(t *testing.T) {
		_, err := q.GetTraceByRequestID("nonexistent")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("DeleteLogsBefore", func(t *testing.T) {
		cutoff := time.Now().Add(-24 * time.Hour)
		deleted, err := m.DeleteLogsBefore(cutoff)
		if err != nil {
			t.Fatalf("DeleteLogsBefore: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("expected 1 deleted, got %d", deleted)
		}
	})

	t.Run("DeleteTracesBefore", func(t *testing.T) {
		// Create an old trace
		oldTrace := &models.UsageLogTrace{RequestID: "req-old", CreatedAt: time.Now().Unix() - 86400*30}
		db.Select("*").Create(oldTrace)

		cutoff := time.Now().Add(-24 * time.Hour)
		deleted, err := m.DeleteTracesBefore(cutoff)
		if err != nil {
			t.Fatalf("DeleteTracesBefore: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("expected 1 deleted, got %d", deleted)
		}
	})
}

func TestUsageLogDAO_UserScoped(t *testing.T) {
	uctx, db := setupUserContext(t, 42)
	q := NewQuery(uctx).UsageLog()

	now := time.Now().Unix()
	// Logs for user 42
	l1 := &models.UsageLog{UserID: 42, RequestID: "ureq-1", ModelName: "gpt-4", TotalCost: 10, CreatedAt: now}
	l2 := &models.UsageLog{UserID: 42, RequestID: "ureq-2", ModelName: "claude-3", TotalCost: 20, CreatedAt: now}
	// Log for another user
	l3 := &models.UsageLog{UserID: 99, RequestID: "ureq-3", ModelName: "gpt-4", TotalCost: 30, CreatedAt: now}
	for _, l := range []*models.UsageLog{l1, l2, l3} {
		db.Select("*").Create(l)
	}

	t.Run("List only own logs", func(t *testing.T) {
		logs, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, UsageLogListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		_ = logs
	})

	t.Run("GetByRequestID own", func(t *testing.T) {
		log, err := q.GetByRequestID("ureq-1")
		if err != nil {
			t.Fatalf("GetByRequestID: %v", err)
		}
		if log.TotalCost != 10 {
			t.Fatalf("expected 10, got %d", log.TotalCost)
		}
	})

	t.Run("GetByRequestID other user", func(t *testing.T) {
		_, err := q.GetByRequestID("ureq-3")
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound for other user's log, got %v", err)
		}
	})
}
