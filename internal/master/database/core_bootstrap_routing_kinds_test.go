package database

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestBootstrapCoreMigratesCopiedRoutingKinds(t *testing.T) {
	source, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	target, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(source))
	require.NoError(t, models.MigrateCoreDB(target))
	require.NoError(t, source.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "official", Status: 1}, Models: "same"}).Error)
	require.NoError(t, source.Create(&models.ModelRouting{Name: "same", Scope: "global", Enabled: true, Members: `[{"ref":"same","weight":1}]`}).Error)
	_, err = BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith}, BootstrapOptions{})
	require.NoError(t, err)
	var r models.ModelRouting
	require.NoError(t, target.First(&r).Error)
	require.JSONEq(t, `[{"kind":"model","ref":"same","priority":0,"weight":1}]`, r.Members)
}
