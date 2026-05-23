package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
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

func TestUsageLog_AdminList_FilterByPrivateChannelID(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).UsageLog()
	m := NewAdminMutation(ctx)

	// seed: 2 BYOK rows on pchan=10, 1 BYOK row on pchan=20, 1 admin row (pchan=0)
	if err := m.UsageLog().Create(&models.UsageLog{UserID: 1, OwnerType: "private", PrivateChannelID: 10, ModelName: "claude", Status: 1, RequestID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := m.UsageLog().Create(&models.UsageLog{UserID: 1, OwnerType: "private", PrivateChannelID: 10, ModelName: "claude", Status: 1, RequestID: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := m.UsageLog().Create(&models.UsageLog{UserID: 1, OwnerType: "private", PrivateChannelID: 20, ModelName: "claude", Status: 1, RequestID: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := m.UsageLog().Create(&models.UsageLog{UserID: 1, OwnerType: "admin", ChannelID: 1, ModelName: "gpt-4o", Status: 1, RequestID: "d"}); err != nil {
		t.Fatal(err)
	}

	pcid := uint(10)
	logs, total, err := q.List(ListOptions{Page: 1, PageSize: 100}, UsageLogListFilter{PrivateChannelID: &pcid})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (only pchan=10)", total)
	}
	if len(logs) != 2 {
		t.Fatalf("rows = %d, want 2", len(logs))
	}
	for _, l := range logs {
		if l.PrivateChannelID != 10 {
			t.Fatalf("got PrivateChannelID=%d, want 10", l.PrivateChannelID)
		}
	}
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

// ---- Task 2.8: PercentileTTFT ----

// seedStreamLogTTFT inserts a stream/success/non-zero-completion usage_log
// for the given user_id with the given first_response_ms.
func seedStreamLogTTFT(t *testing.T, db *gorm.DB, userID uint, reqID string, ttftMs int) {
	t.Helper()
	if err := db.Select("*").Create(&models.UsageLog{
		UserID: userID, ChannelID: 5, ModelName: "gpt-4o",
		Status: 1, IsStream: true,
		CompletionTokens: 100,
		Duration:         ttftMs + 1000,
		FirstResponseMs:  ttftMs,
		RequestID:        reqID,
		CreatedAt:        time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestPercentileTTFT_Known100Rows_P95(t *testing.T) {
	uctx, db := setupUserContext(t, 42)
	q := NewQuery(uctx).UsageLog()
	for i := 1; i <= 100; i++ {
		seedStreamLogTTFT(t, db, 42, fmt.Sprintf("ttft-%d", i), i)
	}
	got, err := q.PercentileTTFT(UsageLogListFilter{}, 0.95)
	if err != nil {
		t.Fatalf("PercentileTTFT: %v", err)
	}
	// offset = floor(100 * 0.95) = 95 → row[95] (0-indexed) = value 96
	if got != 96 {
		t.Fatalf("PercentileTTFT(p=0.95) = %d, want 96 (offset 95 of 1..100)", got)
	}
}

func TestPercentileTTFT_NoData_ReturnsZero(t *testing.T) {
	uctx, _ := setupUserContext(t, 42)
	q := NewQuery(uctx).UsageLog()
	got, err := q.PercentileTTFT(UsageLogListFilter{}, 0.95)
	if err != nil {
		t.Fatalf("PercentileTTFT: %v", err)
	}
	if got != 0 {
		t.Fatalf("PercentileTTFT empty = %d, want 0", got)
	}
}

func TestPercentileTTFT_OnlyFailedRows_ReturnsZero(t *testing.T) {
	uctx, db := setupUserContext(t, 42)
	q := NewQuery(uctx).UsageLog()
	// 10 status=0 stream rows: should be filtered out → cnt=0 → 0
	for i := 0; i < 10; i++ {
		if err := db.Select("*").Create(&models.UsageLog{
			UserID: 42, ChannelID: 5, ModelName: "gpt-4o",
			Status: 0, IsStream: true,
			CompletionTokens: 100,
			Duration:         500,
			FirstResponseMs:  100 + i,
			RequestID:        fmt.Sprintf("ttft-fail-%d", i),
			CreatedAt:        time.Now().Unix(),
		}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := q.PercentileTTFT(UsageLogListFilter{}, 0.95)
	if err != nil {
		t.Fatalf("PercentileTTFT: %v", err)
	}
	if got != 0 {
		t.Fatalf("PercentileTTFT only-failed = %d, want 0", got)
	}
}

func TestPercentileTTFT_NonStreamOrZeroCompletion_Excluded(t *testing.T) {
	uctx, db := setupUserContext(t, 42)
	q := NewQuery(uctx).UsageLog()
	// non-stream, status=1, completion>0 → excluded
	if err := db.Select("*").Create(&models.UsageLog{
		UserID: 42, ChannelID: 5, ModelName: "gpt-4o",
		Status: 1, IsStream: false, CompletionTokens: 100,
		FirstResponseMs: 999,
		RequestID:       "nonstream", CreatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	// stream, status=1, completion=0 → excluded
	if err := db.Select("*").Create(&models.UsageLog{
		UserID: 42, ChannelID: 5, ModelName: "gpt-4o",
		Status: 1, IsStream: true, CompletionTokens: 0,
		FirstResponseMs: 888,
		RequestID:       "zerocomp", CreatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := q.PercentileTTFT(UsageLogListFilter{}, 0.95)
	if err != nil {
		t.Fatalf("PercentileTTFT: %v", err)
	}
	if got != 0 {
		t.Fatalf("PercentileTTFT with only excluded rows = %d, want 0", got)
	}
}

func TestUsageLogList_TimeWindowFiltersByCreatedAt(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).UsageLog()

	for i, ts := range []int64{1000, 2000, 3000} {
		if err := db.Select("*").Create(&models.UsageLog{
			UserID:    1,
			RequestID: fmt.Sprintf("req-%d", i),
			CreatedAt: ts,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	logs, total, err := q.List(
		ListOptions{Page: 1, PageSize: 100},
		UsageLogListFilter{
			TimeWindow: listfilter.TimeWindow{Start: 1500, End: 3000},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (only ts=2000)", total)
	}
	if len(logs) != 1 || logs[0].CreatedAt != 2000 {
		t.Errorf("logs = %+v, want single row with ts=2000", logs)
	}
}

func TestUsageLogList_TimeWindowZeroNoOp(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).UsageLog()

	for i, ts := range []int64{1000, 2000, 3000} {
		if err := db.Select("*").Create(&models.UsageLog{
			UserID:    1,
			RequestID: fmt.Sprintf("req-%d", i),
			CreatedAt: ts,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	_, total, err := q.List(
		ListOptions{Page: 1, PageSize: 100},
		UsageLogListFilter{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (no time filter)", total)
	}
}

func TestUsageLogList_BoundaryStartInclusiveEndExclusive(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).UsageLog()

	for i, ts := range []int64{1000, 2000} {
		if err := db.Select("*").Create(&models.UsageLog{
			UserID:    1,
			RequestID: fmt.Sprintf("req-%d", i),
			CreatedAt: ts,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	logs, total, err := q.List(
		ListOptions{Page: 1, PageSize: 100},
		UsageLogListFilter{
			TimeWindow: listfilter.TimeWindow{Start: 1000, End: 2000},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (Start inclusive, End exclusive)", total)
	}
	if len(logs) != 1 || logs[0].CreatedAt != 1000 {
		t.Errorf("expected only the row ts=1000, got %+v", logs)
	}
}
