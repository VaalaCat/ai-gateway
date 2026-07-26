package logqueue

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCoreSettingsFinderLoadsAndClampsValues(t *testing.T) {
	db := newCoreSettingsDB(t)
	require.NoError(t, db.Create(&[]models.Setting{
		{Key: QueueMaxEntriesKey, Value: "999999999"},
		{Key: QueueMaxBytesKey, Value: "1"},
		{Key: DeliveryBatchSizeKey, Value: "17"},
		{Key: BackoffMaxSecondsKey, Value: "not-a-number"},
	}).Error)
	finder := NewCoreSettingsFinder(func() *gorm.DB { return db }, nil)

	settings := finder.Find(context.Background())

	require.Equal(t, MaxQueueEntries, settings.QueueMaxEntries)
	require.Equal(t, int64(MinQueueBytes), settings.QueueMaxBytes)
	require.Equal(t, 17, settings.DeliveryBatchSize)
	require.Equal(t, DefaultBackoffMaxSeconds, settings.BackoffMaxSeconds)
}

func TestCoreSettingsFinderKeepsLastValidValuesOnQueryFailure(t *testing.T) {
	db := newCoreSettingsDB(t)
	require.NoError(t, db.Create(&models.Setting{Key: DeliveryBatchSizeKey, Value: "23"}).Error)
	current := db
	var reported error
	finder := NewCoreSettingsFinder(func() *gorm.DB { return current }, func(err error) { reported = err })
	require.Equal(t, 23, finder.Find(context.Background()).DeliveryBatchSize)
	current = nil

	settings := finder.Find(context.Background())

	require.Equal(t, 23, settings.DeliveryBatchSize)
	require.Error(t, reported)
}

func TestCoreSettingsFinderCanceledContextKeepsLastValues(t *testing.T) {
	finder := NewCoreSettingsFinder(func() *gorm.DB { return nil }, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("stop"))
	require.Equal(t, DefaultDeliverySettings(), finder.Find(ctx))
}

func newCoreSettingsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	require.NoError(t, models.MigrateCoreDB(db))
	return db
}
