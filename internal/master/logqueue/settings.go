package logqueue

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

const (
	QueueMaxEntriesKey   = "log.delivery_queue_max_entries"
	QueueMaxBytesKey     = "log.delivery_queue_max_bytes"
	DeliveryBatchSizeKey = "log.delivery_batch_size"
	BackoffMaxSecondsKey = "log.delivery_backoff_max_seconds"

	DefaultQueueMaxEntries   = 10000
	DefaultQueueMaxBytes     = int64(128 << 20)
	DefaultDeliveryBatchSize = 100
	DefaultBackoffMaxSeconds = 60

	MinQueueEntries = 100
	MaxQueueEntries = 1_000_000
	MinQueueBytes   = int64(1 << 20)
	MaxQueueBytes   = int64(8 << 30)
	MinBatchSize    = 1
	MaxBatchSize    = 1000
	MinBackoffSecs  = 1
	MaxBackoffSecs  = 3600
)

type DeliverySettings struct {
	QueueMaxEntries   int
	QueueMaxBytes     int64
	DeliveryBatchSize int
	BackoffMaxSeconds int
}

type DeliverySettingDefinition struct {
	Default string
	Min     int64
	Max     int64
}

func DeliverySettingDefinitions() map[string]DeliverySettingDefinition {
	return map[string]DeliverySettingDefinition{
		QueueMaxEntriesKey:   {Default: strconv.Itoa(DefaultQueueMaxEntries), Min: MinQueueEntries, Max: MaxQueueEntries},
		QueueMaxBytesKey:     {Default: strconv.FormatInt(DefaultQueueMaxBytes, 10), Min: MinQueueBytes, Max: MaxQueueBytes},
		DeliveryBatchSizeKey: {Default: strconv.Itoa(DefaultDeliveryBatchSize), Min: MinBatchSize, Max: MaxBatchSize},
		BackoffMaxSecondsKey: {Default: strconv.Itoa(DefaultBackoffMaxSeconds), Min: MinBackoffSecs, Max: MaxBackoffSecs},
	}
}

func DefaultDeliverySettings() DeliverySettings {
	return DeliverySettings{
		QueueMaxEntries: DefaultQueueMaxEntries, QueueMaxBytes: DefaultQueueMaxBytes,
		DeliveryBatchSize: DefaultDeliveryBatchSize, BackoffMaxSeconds: DefaultBackoffMaxSeconds,
	}
}

type SettingsFinder interface {
	Find(context.Context) DeliverySettings
}

type CoreSettingsFinder struct {
	dbFinder func() *gorm.DB
	onError  func(error)
	last     atomic.Pointer[DeliverySettings]
}

func NewCoreSettingsFinder(dbFinder func() *gorm.DB, onError func(error)) *CoreSettingsFinder {
	finder := &CoreSettingsFinder{dbFinder: dbFinder, onError: onError}
	defaults := DefaultDeliverySettings()
	finder.last.Store(&defaults)
	return finder
}

func (f *CoreSettingsFinder) Find(ctx context.Context) DeliverySettings {
	previous := *f.last.Load()
	if err := context.Cause(ctx); err != nil {
		return previous
	}
	if f.dbFinder == nil {
		f.report(fmt.Errorf("load log delivery settings: core database unavailable"))
		return previous
	}
	db := f.dbFinder()
	if db == nil {
		f.report(fmt.Errorf("load log delivery settings: core database unavailable"))
		return previous
	}
	var rows []models.Setting
	keys := []string{QueueMaxEntriesKey, QueueMaxBytesKey, DeliveryBatchSizeKey, BackoffMaxSecondsKey}
	if err := db.WithContext(ctx).Where("key IN ?", keys).Find(&rows).Error; err != nil {
		f.report(fmt.Errorf("load log delivery settings: %w", err))
		return previous
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	defaults := DefaultDeliverySettings()
	next := DeliverySettings{
		QueueMaxEntries:   clampInt(parseInt(values[QueueMaxEntriesKey], defaults.QueueMaxEntries), MinQueueEntries, MaxQueueEntries),
		QueueMaxBytes:     clampInt64(parseInt64(values[QueueMaxBytesKey], defaults.QueueMaxBytes), MinQueueBytes, MaxQueueBytes),
		DeliveryBatchSize: clampInt(parseInt(values[DeliveryBatchSizeKey], defaults.DeliveryBatchSize), MinBatchSize, MaxBatchSize),
		BackoffMaxSeconds: clampInt(parseInt(values[BackoffMaxSecondsKey], defaults.BackoffMaxSeconds), MinBackoffSecs, MaxBackoffSecs),
	}
	f.last.Store(&next)
	return next
}

func (f *CoreSettingsFinder) report(err error) {
	if f.onError != nil {
		f.onError(err)
	}
}

func parseInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func parseInt64(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func clampInt64(value, min, max int64) int64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
