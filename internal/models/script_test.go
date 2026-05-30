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
		Scope:   datatypes.NewJSONType(ScriptScope{ChannelIDs: []uint{1, 2}, ModelNames: []string{"gpt-4o"}}),
	}
	require.NoError(t, db.Create(&s).Error)

	var got AdminScript
	require.NoError(t, db.First(&got, s.ID).Error)
	assert.Equal(t, []uint{1, 2}, got.Scope.Data().ChannelIDs)
	assert.Equal(t, []string{"gpt-4o"}, got.Scope.Data().ModelNames)
	assert.True(t, got.Enabled)
}

func TestAdminScript_NameUnique(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.Create(&AdminScript{Name: "dup", Code: "x"}).Error)
	err := db.Create(&AdminScript{Name: "dup", Code: "y"}).Error
	assert.Error(t, err)
}
