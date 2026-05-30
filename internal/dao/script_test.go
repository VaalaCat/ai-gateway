package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAdminScriptDAO(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).AdminScript()
	m := NewAdminMutation(ctx).AdminScript()

	s := &models.AdminScript{
		Name: "a", Code: "function onRequest(c){}", Enabled: true, Priority: 2,
		Scope: datatypes.NewJSONType(models.ScriptScope{ModelNames: []string{"m"}}),
	}
	require.NoError(t, m.Create(s))

	t.Run("GetByID", func(t *testing.T) {
		got, err := q.GetByID(s.ID)
		require.NoError(t, err)
		assert.Equal(t, "a", got.Name)
		assert.Equal(t, []string{"m"}, got.Scope.Data().ModelNames)
	})

	t.Run("GetByID not found", func(t *testing.T) {
		_, err := q.GetByID(9999)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})

	t.Run("Update", func(t *testing.T) {
		require.NoError(t, m.Update(s.ID, map[string]any{"enabled": false}))
		got, _ := q.GetByID(s.ID)
		assert.False(t, got.Enabled)
	})

	t.Run("ListEnabled excludes disabled", func(t *testing.T) {
		items, err := q.ListEnabled()
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("List pagination", func(t *testing.T) {
		items, total, err := q.List(ListOptions{Page: 1, PageSize: 10}, "")
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Len(t, items, 1)
	})

	t.Run("Delete", func(t *testing.T) {
		require.NoError(t, m.Delete(s.ID))
		_, err := q.GetByID(s.ID)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})
}

// TestAdminScriptCreateDisabled 钉住一个 GORM 坑：Enabled 带 `default:true`，
// 用结构体 Create 时 false 是零值会被 GORM 跳过、套用 DB 默认值 true。
// Create 必须显式写入 Enabled，才能创建“停用”脚本。
func TestAdminScriptCreateDisabled(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	q := NewAdminQuery(ctx).AdminScript()
	m := NewAdminMutation(ctx).AdminScript()

	s := &models.AdminScript{Name: "off", Code: "function onRequest(c){}", Enabled: false}
	require.NoError(t, m.Create(s))

	got, err := q.GetByID(s.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled, "created with Enabled:false must persist as false")
}
