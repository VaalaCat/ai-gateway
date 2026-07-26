package historybackfill

import (
	"context"
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const defaultBatchSize = 5000

type BillingHistoryBatchWriter interface {
	WriteHistory(context.Context, string, uint, []models.BillingLog) error
}

type RequestHistoryBatchWriter interface {
	WriteRequests(context.Context, string, uint, []models.RequestLog) error
	WriteTraces(context.Context, string, uint, []models.RequestTrace) error
}

type Options struct {
	CoreDBFinder         func() *gorm.DB
	LogDBFinder          func() *gorm.DB
	Reader               *LegacyReader
	BillingHistoryWriter BillingHistoryBatchWriter
	RequestHistoryWriter RequestHistoryBatchWriter
	BatchSize            int
	Logger               *zap.Logger
}

type Backfiller struct {
	options Options
}

func New(options Options) *Backfiller {
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}
	return &Backfiller{options: options}
}

type PassResult struct {
	CopiedRows int64
	CaughtUp   bool
}

const (
	billingCursorKey = "billing"
	requestCursorKey = "requests"
	traceCursorKey   = "traces"
)

func (b *Backfiller) RunPass(ctx context.Context) (PassResult, error) {
	if ctx == nil {
		return PassResult{}, fmt.Errorf("history backfill context is nil")
	}
	if err := context.Cause(ctx); err != nil {
		return PassResult{}, err
	}
	if b == nil || b.options.Reader == nil {
		return PassResult{}, fmt.Errorf("legacy history reader is unavailable")
	}
	result := PassResult{CaughtUp: true}
	if err := b.copyBillingPass(ctx, &result); err != nil {
		return result, err
	}
	if !b.options.Reader.HasLogHistory() {
		return result, nil
	}
	if err := b.copyRequestPass(ctx, &result); err != nil {
		return result, err
	}
	if err := b.copyTracePass(ctx, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (b *Backfiller) copyBillingPass(ctx context.Context, result *PassResult) error {
	if b.options.BillingHistoryWriter == nil {
		return fmt.Errorf("billing history writer is unavailable")
	}
	after, err := loadHistoryCursor(ctx, b.options.CoreDBFinder, billingCursorKey)
	if err != nil {
		return err
	}
	batch, err := b.options.Reader.ReadBilling(ctx, after, b.options.BatchSize)
	if err != nil {
		return err
	}
	if len(batch.Rows) == 0 {
		return nil
	}
	if err := b.options.BillingHistoryWriter.WriteHistory(ctx, billingCursorKey, batch.LastSourceID, batch.Rows); err != nil {
		return err
	}
	result.CopiedRows += int64(len(batch.Rows))
	result.CaughtUp = false
	return nil
}

func (b *Backfiller) copyRequestPass(ctx context.Context, result *PassResult) error {
	if b.options.RequestHistoryWriter == nil {
		return fmt.Errorf("request history writer is unavailable")
	}
	cursor, err := loadHistoryCursorRecord(ctx, b.options.LogDBFinder, requestCursorKey)
	if err != nil {
		return err
	}
	if cursor.Skipped {
		return nil
	}
	batch, err := b.options.Reader.ReadRequests(ctx, cursor.LastSourceID, b.options.BatchSize)
	if err != nil {
		return err
	}
	if len(batch.Rows) == 0 {
		return nil
	}
	if err := b.options.RequestHistoryWriter.WriteRequests(ctx, requestCursorKey, batch.LastSourceID, batch.Rows); err != nil {
		return err
	}
	result.CopiedRows += int64(len(batch.Rows))
	result.CaughtUp = false
	return nil
}

func (b *Backfiller) copyTracePass(ctx context.Context, result *PassResult) error {
	cursor, err := loadHistoryCursorRecord(ctx, b.options.LogDBFinder, traceCursorKey)
	if err != nil {
		return err
	}
	if cursor.Skipped {
		return nil
	}
	batch, err := b.options.Reader.ReadTraces(ctx, cursor.LastSourceID, b.options.BatchSize)
	if err != nil {
		return err
	}
	if len(batch.Rows) == 0 {
		return nil
	}
	if err := b.options.RequestHistoryWriter.WriteTraces(ctx, traceCursorKey, batch.LastSourceID, batch.Rows); err != nil {
		return err
	}
	result.CopiedRows += int64(len(batch.Rows))
	result.CaughtUp = false
	return nil
}

func loadHistoryCursor(ctx context.Context, finder func() *gorm.DB, key string) (uint, error) {
	cursor, err := loadHistoryCursorRecord(ctx, finder, key)
	return cursor.LastSourceID, err
}

func loadHistoryCursorRecord(ctx context.Context, finder func() *gorm.DB, key string) (models.HistoryCursor, error) {
	if finder == nil {
		return models.HistoryCursor{}, fmt.Errorf("history cursor database is unavailable")
	}
	db := finder()
	if db == nil {
		return models.HistoryCursor{}, fmt.Errorf("history cursor database is unavailable")
	}
	var cursor models.HistoryCursor
	err := db.WithContext(ctx).Where("key = ?", key).First(&cursor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.HistoryCursor{Key: key}, nil
	}
	if err != nil {
		return models.HistoryCursor{}, fmt.Errorf("read history cursor %q: %w", key, err)
	}
	return cursor, nil
}
