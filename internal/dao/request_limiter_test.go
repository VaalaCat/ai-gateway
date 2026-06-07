package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestRequestLimiterCRUD(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)

	// success: create → get → update → list
	l := &models.RequestLimiter{Name: "u-rate", Metric: "rate", Capacity: 60, WindowMs: 60000, KeyBy: "per_user", Action: "wait", Enabled: true}
	require.NoError(t, m.RequestLimiter().Create(l))
	require.NotZero(t, l.ID)

	got, err := q.RequestLimiter().GetByID(l.ID)
	require.NoError(t, err)
	require.Equal(t, "u-rate", got.Name)

	require.NoError(t, m.RequestLimiter().Update(l.ID, map[string]any{"capacity": int64(100)}))
	got, _ = q.RequestLimiter().GetByID(l.ID)
	require.EqualValues(t, 100, got.Capacity)

	all, err := q.RequestLimiter().ListAll()
	require.NoError(t, err)
	require.Len(t, all, 1)

	// failure: GetByID 不存在的 ID → error
	_, err = q.RequestLimiter().GetByID(99999)
	require.Error(t, err)

	// binding success
	b := &models.LimiterBinding{LimiterID: l.ID, TargetType: "global", Enabled: true}
	require.NoError(t, m.LimiterBinding().Create(b))
	binds, err := q.LimiterBinding().ListByLimiter(l.ID)
	require.NoError(t, err)
	require.Len(t, binds, 1)

	// boundary: ListByLimiter 不存在的 limiter → 空切片 + nil error
	empty, err := q.LimiterBinding().ListByLimiter(99999)
	require.NoError(t, err)
	require.Len(t, empty, 0)

	// failure: 同一 (limiter_id,target_type,target_id) 重复绑定被 uk_limiter_binding 拒绝
	dup := &models.LimiterBinding{LimiterID: l.ID, TargetType: "global", Enabled: true}
	require.Error(t, m.LimiterBinding().Create(dup))

	require.NoError(t, m.LimiterBinding().Delete(b.ID))
	require.NoError(t, m.RequestLimiter().Delete(l.ID))
	all, _ = q.RequestLimiter().ListAll()
	require.Len(t, all, 0)
}

// TestLimiterBinding_DeleteByLimiter 守护级联清理原语：删 limiter 时一并清掉它的所有 binding，
// 避免孤儿绑定。master CRUD 删 limiter 时会调用它。
func TestLimiterBinding_DeleteByLimiter(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	m := NewAdminMutation(ctx)

	l := &models.RequestLimiter{Name: "x", Metric: "concurrency", Capacity: 10, KeyBy: "per_channel", Action: "reject", Enabled: true}
	require.NoError(t, m.RequestLimiter().Create(l))

	require.NoError(t, m.LimiterBinding().Create(&models.LimiterBinding{LimiterID: l.ID, TargetType: "channel", TargetID: 1, Enabled: true}))
	require.NoError(t, m.LimiterBinding().Create(&models.LimiterBinding{LimiterID: l.ID, TargetType: "channel", TargetID: 2, Enabled: true}))

	binds, err := q.LimiterBinding().ListByLimiter(l.ID)
	require.NoError(t, err)
	require.Len(t, binds, 2)

	require.NoError(t, m.LimiterBinding().DeleteByLimiter(l.ID))
	binds, err = q.LimiterBinding().ListByLimiter(l.ID)
	require.NoError(t, err)
	require.Len(t, binds, 0)
}
