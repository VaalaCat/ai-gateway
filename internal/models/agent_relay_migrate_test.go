package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutoMigrateAgentRelayDefaultsAndIdempotence(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, db.Exec(`CREATE TABLE agents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_id TEXT UNIQUE,
		secret TEXT,
		name TEXT,
		status INTEGER DEFAULT 1,
		last_seen INTEGER,
		created_at INTEGER,
		http_addresses TEXT,
		tags TEXT,
		proxy_url TEXT
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO agents (agent_id, name) VALUES (?, ?)`, "legacy-agent", "legacy").Error)

	require.NoError(t, AutoMigrate(db))

	var legacy Agent
	require.NoError(t, db.Where("agent_id = ?", "legacy-agent").First(&legacy).Error)
	require.Equal(t, "inherit", legacy.RelayMode)
	require.Empty(t, legacy.RelayURI)

	created := Agent{AgentID: "new-agent", Name: "new"}
	require.NoError(t, db.Create(&created).Error)
	var fresh Agent
	require.NoError(t, db.Where("agent_id = ?", "new-agent").First(&fresh).Error)
	require.Equal(t, "inherit", fresh.RelayMode)
	require.Empty(t, fresh.RelayURI)

	require.NoError(t, AutoMigrate(db))
	var migratedAgain []Agent
	require.NoError(t, db.Order("agent_id").Find(&migratedAgain).Error)
	require.Len(t, migratedAgain, 2)
	for _, agent := range migratedAgain {
		require.Equal(t, "inherit", agent.RelayMode)
		require.Empty(t, agent.RelayURI)
	}
}

func TestAutoMigrateRemovesLegacyRelayFallbackSettingIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, AutoMigrate(db))
	requireLegacyRelayFallbackSettingCount(t, db, 0)
	require.NoError(t, db.Create(&Setting{Key: legacyRelayFallbackSettingKey(), Value: "1"}).Error)
	requireLegacyRelayFallbackSettingCount(t, db, 1)

	require.NoError(t, AutoMigrate(db))
	requireLegacyRelayFallbackSettingCount(t, db, 0)
	require.NoError(t, AutoMigrate(db))
	requireLegacyRelayFallbackSettingCount(t, db, 0)
}

func requireLegacyRelayFallbackSettingCount(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&Setting{}).Where("key = ?", legacyRelayFallbackSettingKey()).Count(&count).Error)
	require.Equal(t, want, count)
}
