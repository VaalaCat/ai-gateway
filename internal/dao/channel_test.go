package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestChannelLimitReconcileCASRejectsStaleSnapshots(t *testing.T) {
	tests := []struct {
		name          string
		initialStatus int
		desiredStatus int
		mutate        func(*gorm.DB, uint)
		wantStatus    int
		wantAutoBan   bool
	}{
		{
			name:          "newer auto-ban trip blocks limit recovery",
			initialStatus: 0,
			desiredStatus: 1,
			mutate: func(db *gorm.DB, id uint) {
				db.Model(&models.Channel{}).Where("id = ?", id).Updates(map[string]any{
					"auto_ban_state": datatypes.NewJSONType(models.ChannelDisableState{Tripped: true, Reason: "consecutive_errors"}),
				})
			},
			wantStatus:  0,
			wantAutoBan: true,
		},
		{
			name:          "newer explicit status revision mutation blocks limit recovery",
			initialStatus: 0,
			desiredStatus: 1,
			mutate: func(db *gorm.DB, id uint) {
				db.Model(&models.Channel{}).Where("id = ?", id).Updates(map[string]any{"status": 0, "auto_ban_revision": 1})
			},
			wantStatus: 0,
		},
		{
			name:          "newer manual configuration blocks limit disable",
			initialStatus: 1,
			desiredStatus: 0,
			mutate: func(db *gorm.DB, id uint) {
				db.Model(&models.Channel{}).Where("id = ?", id).Updates(map[string]any{"auto_ban": 1, "auto_ban_revision": 1})
			},
			wantStatus: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			channel := &models.Channel{
				ChannelCore: models.ChannelCore{Name: tt.name, Type: 1, Status: tt.initialStatus},
				LimitState:  datatypes.NewJSONType(models.ChannelDisableState{Tripped: true, AutoRecover: true}),
			}
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, db.Model(&models.Channel{}).Where("id = ?", channel.ID).Update("status", tt.initialStatus).Error)
			snapshot, err := NewAdminQuery(ctx).Channel().GetByID(channel.ID)
			require.NoError(t, err)
			tt.mutate(db, channel.ID)

			changed, err := NewAdminMutation(ctx).Channel().ReconcileLimit(*snapshot, tt.desiredStatus, models.ChannelDisableState{})
			require.NoError(t, err)
			require.False(t, changed)

			var got models.Channel
			require.NoError(t, db.First(&got, channel.ID).Error)
			require.Equal(t, tt.wantStatus, got.Status)
			require.Equal(t, tt.wantAutoBan, got.AutoBanState.Data().Tripped)
		})
	}
}

func TestChannelLimitReconcileCASBindsLimitSnapshot(t *testing.T) {
	tests := []struct {
		name          string
		initialStatus int
		desiredStatus int
		mutatedLimit  string
		wantStatus    int
	}{
		{
			name:          "removed limit blocks stale disable",
			initialStatus: 1,
			desiredStatus: 0,
			mutatedLimit:  `{}`,
			wantStatus:    1,
		},
		{
			name:          "raised legacy JSON limit blocks stale recovery",
			initialStatus: 0,
			desiredStatus: 1,
			mutatedLimit:  `{"disable_at":0,"rules":[{"metric":"calls","window":"daily","days":0,"threshold":10,"cost_basis":""}]}`,
			wantStatus:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			channel := &models.Channel{
				ChannelCore: models.ChannelCore{Name: tt.name, Type: 1, Status: 1},
				Limit: datatypes.NewJSONType(models.ChannelLimit{Rules: []models.LimitRule{{
					Metric: models.LimitMetricCalls, Window: models.LimitWindowDaily, Threshold: 1,
				}}}),
				LimitState: datatypes.NewJSONType(models.ChannelDisableState{Tripped: true, AutoRecover: true}),
			}
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, db.Model(&models.Channel{}).Where("id = ?", channel.ID).Update("status", tt.initialStatus).Error)
			snapshot, err := NewAdminQuery(ctx).Channel().GetByID(channel.ID)
			require.NoError(t, err)
			require.NoError(t, db.Model(&models.Channel{}).Where("id = ?", channel.ID).Update("limit", tt.mutatedLimit).Error)

			changed, err := NewAdminMutation(ctx).Channel().ReconcileLimit(*snapshot, tt.desiredStatus, models.ChannelDisableState{})
			require.NoError(t, err)
			require.False(t, changed)
			var got models.Channel
			require.NoError(t, db.First(&got, channel.ID).Error)
			require.Equal(t, tt.wantStatus, got.Status)
		})
	}
}

func TestChannelAutoDisableRequiresEnabledMatchingRevision(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		autoBan    int
		revision   uint64
		triggerRev uint64
		wantUpdate bool
	}{
		{name: "matching enabled channel", status: 1, autoBan: 1, revision: 7, triggerRev: 7, wantUpdate: true},
		{name: "stale revision", status: 1, autoBan: 1, revision: 8, triggerRev: 7},
		{name: "auto ban disabled", status: 1, autoBan: 0, revision: 7, triggerRev: 7},
		{name: "historical non-binary auto ban", status: 1, autoBan: 2, revision: 7, triggerRev: 7},
		{name: "already disabled", status: 0, autoBan: 1, revision: 7, triggerRev: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			channel := &models.Channel{ChannelCore: models.ChannelCore{
				Name: tt.name, Type: 1, Status: 1, AutoBan: tt.autoBan, AutoBanRevision: tt.revision,
			}}
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, db.Model(channel).UpdateColumns(map[string]any{
				"status": tt.status, "auto_ban": tt.autoBan, "updated_at": int64(1),
			}).Error)

			state := models.ChannelDisableState{Tripped: true, Reason: "consecutive_errors", TrippedAt: 123}
			result, err := NewAdminMutation(ctx).Channel().AutoDisable(channel.ID, tt.triggerRev, state)
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdate, result.Updated)
			require.Zero(t, result.OwnerID)

			got, err := NewAdminQuery(ctx).Channel().GetByID(channel.ID)
			require.NoError(t, err)
			if tt.wantUpdate {
				require.Zero(t, got.Status)
				require.Equal(t, state, got.AutoBanState.Data())
				require.Greater(t, got.UpdatedAt, int64(1))
			} else {
				require.Equal(t, tt.status, got.Status)
				require.False(t, got.AutoBanState.Data().Tripped)
				require.Equal(t, int64(1), got.UpdatedAt)
			}
			require.Equal(t, tt.revision, got.AutoBanRevision)
		})
	}
}

func TestChannelAutoDisableUnknownIDIsNoop(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	result, err := NewAdminMutation(ctx).Channel().AutoDisable(9999, 0, models.ChannelDisableState{Tripped: true})
	require.NoError(t, err)
	require.False(t, result.Updated)
}

func TestChannelDAO(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Channel()
	m := NewAdminMutation(ctx).Channel()

	// seed channels
	ch1 := &models.Channel{ChannelCore: models.ChannelCore{Name: "OpenAI", Type: 1, Status: 1}, Models: "gpt-4", Tag: "premium"}
	ch2 := &models.Channel{ChannelCore: models.ChannelCore{Name: "Claude", Type: 2, Status: 1}, Models: "claude-3", Tag: "premium"}
	ch3 := &models.Channel{ChannelCore: models.ChannelCore{Name: "Disabled", Type: 1, Status: 1}, Models: "gpt-3.5", Tag: "free"}
	for _, ch := range []*models.Channel{ch1, ch2, ch3} {
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Disable ch3 via raw update to bypass gorm defaults
	db.Model(&models.Channel{}).Where("id = ?", ch3.ID).Update("status", 0)

	t.Run("GetByID", func(t *testing.T) {
		ch, err := q.GetByID(ch1.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if ch.Name != "OpenAI" {
			t.Fatalf("expected OpenAI, got %s", ch.Name)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("List with pagination", func(t *testing.T) {
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 2}, ChannelListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected total 3, got %d", total)
		}
		if len(channels) != 2 {
			t.Fatalf("expected 2 channels, got %d", len(channels))
		}
	})

	t.Run("List with search filter", func(t *testing.T) {
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ChannelListFilter{Search: "claude"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected total 1, got %d", total)
		}
		if channels[0].Name != "Claude" {
			t.Fatalf("expected Claude, got %s", channels[0].Name)
		}
	})

	t.Run("List with type filter", func(t *testing.T) {
		tp := 1
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ChannelListFilter{Type: &tp})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected total 2, got %d", total)
		}
		_ = channels
	})

	t.Run("List with status filter", func(t *testing.T) {
		st := 0
		channels, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, ChannelListFilter{Status: &st})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected total 1, got %d", total)
		}
		if channels[0].Name != "Disabled" {
			t.Fatalf("expected Disabled, got %s", channels[0].Name)
		}
	})

	t.Run("ListAll", func(t *testing.T) {
		channels, err := q.ListAll()
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(channels) != 3 {
			t.Fatalf("expected 3, got %d", len(channels))
		}
	})

	t.Run("ListByTag", func(t *testing.T) {
		channels, err := q.ListByTag("premium")
		if err != nil {
			t.Fatalf("ListByTag: %v", err)
		}
		if len(channels) != 2 {
			t.Fatalf("expected 2, got %d", len(channels))
		}
	})

	t.Run("ListEnabled", func(t *testing.T) {
		channels, err := q.ListEnabled()
		if err != nil {
			t.Fatalf("ListEnabled: %v", err)
		}
		if len(channels) != 2 {
			t.Fatalf("expected 2, got %d", len(channels))
		}
	})

	t.Run("Create", func(t *testing.T) {
		ch := &models.Channel{ChannelCore: models.ChannelCore{Name: "NewCh", Type: 3}}
		if err := m.Create(ch); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if ch.ID == 0 {
			t.Fatal("expected ID to be set")
		}
	})

	t.Run("Update", func(t *testing.T) {
		if err := m.Update(ch1.ID, map[string]any{"name": "OpenAI-Updated"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		ch, _ := q.GetByID(ch1.ID)
		if ch.Name != "OpenAI-Updated" {
			t.Fatalf("expected OpenAI-Updated, got %s", ch.Name)
		}
	})

	t.Run("ListByIDs sorts IDs ascending", func(t *testing.T) {
		channels, err := q.ListByIDs([]uint{ch2.ID, ch1.ID})
		if err != nil {
			t.Fatalf("ListByIDs: %v", err)
		}
		if len(channels) != 2 || channels[0].ID != ch1.ID || channels[1].ID != ch2.ID {
			t.Fatalf("ListByIDs returned %#v, want IDs [%d %d]", channels, ch1.ID, ch2.ID)
		}
	})

	t.Run("UpdateByIDs returns affected rows", func(t *testing.T) {
		rows, err := m.UpdateByIDs([]uint{ch2.ID, ch1.ID}, map[string]any{"remark": "batch"})
		if err != nil {
			t.Fatalf("UpdateByIDs: %v", err)
		}
		if rows != 2 {
			t.Fatalf("UpdateByIDs rows = %d, want 2", rows)
		}
		channels, err := q.ListByIDs([]uint{ch1.ID, ch2.ID})
		if err != nil {
			t.Fatalf("ListByIDs after update: %v", err)
		}
		for _, channel := range channels {
			if channel.Remark != "batch" {
				t.Fatalf("channel %d remark = %q, want batch", channel.ID, channel.Remark)
			}
		}
	})

	t.Run("batch primitives honor transaction context", func(t *testing.T) {
		rollbackErr := errors.New("rollback batch update")
		err := RunInTx(ctx, func(txCtx Context) error {
			rows, err := NewAdminMutation(txCtx).Channel().UpdateByIDs(
				[]uint{ch1.ID, ch2.ID},
				map[string]any{"tag": "rolled-back"},
			)
			if err != nil {
				return err
			}
			if rows != 2 {
				t.Fatalf("transactional UpdateByIDs rows = %d, want 2", rows)
			}
			inside, err := NewAdminQuery(txCtx).Channel().ListByIDs([]uint{ch2.ID, ch1.ID})
			if err != nil {
				return err
			}
			if len(inside) != 2 || inside[0].Tag != "rolled-back" || inside[1].Tag != "rolled-back" {
				t.Fatalf("transactional ListByIDs did not observe update: %#v", inside)
			}
			return rollbackErr
		})
		if !errors.Is(err, rollbackErr) {
			t.Fatalf("RunInTx error = %v, want %v", err, rollbackErr)
		}
		outside, err := q.ListByIDs([]uint{ch1.ID, ch2.ID})
		if err != nil {
			t.Fatalf("ListByIDs after rollback: %v", err)
		}
		if len(outside) != 2 || outside[0].Tag != "premium" || outside[1].Tag != "premium" {
			t.Fatalf("batch update escaped rollback: %#v", outside)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := m.Delete(ch3.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := q.GetByID(ch3.ID)
		if err != gorm.ErrRecordNotFound {
			t.Fatalf("expected ErrRecordNotFound, got %v", err)
		}
	})
}

func TestChannelDAOUpdateByIDsCountsMatchedSQLiteRows(t *testing.T) {
	ctx, db := setupAdminContext(t)
	channels := []models.Channel{
		{ChannelCore: models.ChannelCore{Name: "same-value-1", Status: 1, Remark: "same", UpdatedAt: 42}},
		{ChannelCore: models.ChannelCore{Name: "same-value-2", Status: 1, Remark: "same", UpdatedAt: 42}},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatalf("seed same-value channels: %v", err)
	}
	ids := []uint{channels[0].ID, channels[1].ID}
	for _, channel := range channels {
		if channel.Remark != "same" || channel.UpdatedAt != 42 {
			t.Fatalf("seeded channel %d values = (%q, %d), want (same, 42)", channel.ID, channel.Remark, channel.UpdatedAt)
		}
	}

	rows, err := NewAdminMutation(ctx).Channel().UpdateByIDs(ids, map[string]any{
		"remark":     "same",
		"updated_at": int64(42),
	})
	if err != nil {
		t.Fatalf("UpdateByIDs same values: %v", err)
	}
	if rows != int64(len(ids)) {
		t.Fatalf("UpdateByIDs same-value rows = %d, want matched rows %d", rows, len(ids))
	}

	updated, err := NewAdminQuery(ctx).Channel().ListByIDs(ids)
	if err != nil {
		t.Fatalf("ListByIDs after same-value update: %v", err)
	}
	if len(updated) != len(ids) {
		t.Fatalf("ListByIDs returned %d rows, want %d", len(updated), len(ids))
	}
	for _, channel := range updated {
		if channel.Remark != "same" || channel.UpdatedAt != 42 {
			t.Fatalf("channel %d values = (%q, %d), want unchanged (same, 42)", channel.ID, channel.Remark, channel.UpdatedAt)
		}
	}
}
