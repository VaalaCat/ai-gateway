package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutoMigrate_CreatesUsageHourlyBucketTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, AutoMigrate(db))

	var name string
	err = db.Raw(`SELECT name FROM sqlite_master WHERE type='table' AND name='usage_hourly_buckets'`).Scan(&name).Error
	require.NoError(t, err)
	require.Equal(t, "usage_hourly_buckets", name)
}

func TestUsageHourlyBucket_UniqueKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, AutoMigrate(db))

	row := UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, PrivateChannelID: 0,
		ModelName: "gpt-4o", AgentID: "cn-1",
		OwnerType: "admin", ChannelName: "openai-shared",
		RequestCount: 1,
	}
	require.NoError(t, db.Create(&row).Error)

	dup := row
	dup.ID = 0
	err = db.Create(&dup).Error
	require.Error(t, err, "second insert with same key must violate unique index")
}

func TestUsageHourlyBucket_AdminAndBYOKCanCoexist(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, AutoMigrate(db))

	admin := UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, PrivateChannelID: 0,
		ModelName: "gpt-4o", AgentID: "cn-1", OwnerType: "admin", RequestCount: 1,
	}
	byok := UsageHourlyBucket{
		Date: "2026-05-20", Hour: 13, ChannelID: 0, PrivateChannelID: 7,
		ModelName: "gpt-4o", AgentID: "cn-1", OwnerType: "private", RequestCount: 1,
	}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&byok).Error, "admin/BYOK 同维度其他列时不冲突")
}
