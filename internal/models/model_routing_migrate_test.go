package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyModelRouting struct {
	ID     uint   `gorm:"primaryKey"`
	Name   string `gorm:"size:128;uniqueIndex:uidx_routing_scope_user_name"`
	Scope  string `gorm:"size:8;uniqueIndex:uidx_routing_scope_user_name"`
	UserID uint   `gorm:"uniqueIndex:uidx_routing_scope_user_name"`
}

func (legacyModelRouting) TableName() string { return "model_routings" }

func TestAutoMigrate_ModelRoutingOwnerIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&legacyModelRouting{}))
	require.True(t, db.Migrator().HasIndex(&legacyModelRouting{}, "uidx_routing_scope_user_name"))

	require.NoError(t, AutoMigrate(db))
	require.True(t, db.Migrator().HasColumn(&ModelRouting{}, "token_id"))
	require.True(t, db.Migrator().HasIndex(&ModelRouting{}, "uidx_routing_owner_name"))
	require.False(t, db.Migrator().HasIndex(&ModelRouting{}, "uidx_routing_scope_user_name"))

	firstToken := Token{UserID: 1, Key: "sk-migrate-1", Name: "one"}
	secondToken := Token{UserID: 1, Key: "sk-migrate-2", Name: "two"}
	require.NoError(t, db.Create(&firstToken).Error)
	require.NoError(t, db.Create(&secondToken).Error)
	base := ModelRouting{Name: "smart", Scope: RoutingScopeToken, Members: `[{"ref":"gpt-4o","priority":0,"weight":1}]`}
	first := base
	first.TokenID = firstToken.ID
	second := base
	second.TokenID = secondToken.ID
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)
	duplicate := base
	duplicate.TokenID = firstToken.ID
	require.Error(t, db.Create(&duplicate).Error)

	require.NoError(t, AutoMigrate(db))
	require.True(t, db.Migrator().HasIndex(&ModelRouting{}, "uidx_routing_owner_name"))
}
