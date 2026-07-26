package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&AdminScript{}))
	return db
}

func TestAdminScript_ScopeRoundTrip(t *testing.T) {
	db := newTestDB(t)
	s := AdminScript{
		Name:    "trim-temperature",
		Code:    "function onRequest(ctx){}",
		Enabled: true,
		Scope: datatypes.NewJSONType(ScriptScope{
			ChannelIDs:        []uint{1, 2},
			PrivateChannelIDs: []uint{3, 4},
			ModelNames:        []string{"gpt-4o"},
			GroupIDs:          []uint{5, 6},
			UserIDs:           []uint{7, 8},
		}),
	}
	require.NoError(t, db.Create(&s).Error)

	var got AdminScript
	require.NoError(t, db.First(&got, s.ID).Error)
	assert.Equal(t, []uint{1, 2}, got.Scope.Data().ChannelIDs)
	assert.Equal(t, []uint{3, 4}, got.Scope.Data().PrivateChannelIDs)
	assert.Equal(t, []string{"gpt-4o"}, got.Scope.Data().ModelNames)
	assert.Equal(t, []uint{5, 6}, got.Scope.Data().GroupIDs)
	assert.Equal(t, []uint{7, 8}, got.Scope.Data().UserIDs)
	assert.True(t, got.Enabled)
}

func TestAdminScript_LegacyScopeScan(t *testing.T) {
	db := newTestDB(t)
	legacyScope := `{"channel_ids":[1],"model_names":["gpt-4o"]}`
	require.NoError(t, db.Exec(
		"INSERT INTO admin_scripts (name, code, enabled, priority, scope, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"legacy-scope", "function onRequest(ctx){}", true, 0, legacyScope, 1, 1,
	).Error)

	var got AdminScript
	require.NoError(t, db.Where("name = ?", "legacy-scope").First(&got).Error)
	assert.Equal(t, []uint{1}, got.Scope.Data().ChannelIDs)
	assert.Equal(t, []string{"gpt-4o"}, got.Scope.Data().ModelNames)
	assert.Empty(t, got.Scope.Data().PrivateChannelIDs)
	assert.Empty(t, got.Scope.Data().GroupIDs)
	assert.Empty(t, got.Scope.Data().UserIDs)
}

func TestAdminScript_NameUnique(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.Create(&AdminScript{Name: "dup", Code: "x"}).Error)
	err := db.Create(&AdminScript{Name: "dup", Code: "y"}).Error
	assert.Error(t, err)
}
