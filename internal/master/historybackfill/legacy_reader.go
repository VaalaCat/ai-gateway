package historybackfill

import (
	"context"
	"fmt"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type HistoryBatch[T any] struct {
	Rows         []T
	LastSourceID uint
}

type LegacyReader struct {
	db   *gorm.DB
	kind masterdatabase.LegacyLayout
}

func NewLegacyReader(db *gorm.DB, kind masterdatabase.LegacyLayout) *LegacyReader {
	return &LegacyReader{db: db, kind: kind}
}

func (r *LegacyReader) ReadBilling(ctx context.Context, after uint, limit int) (HistoryBatch[models.BillingLog], error) {
	if err := r.validate(ctx, limit); err != nil {
		return HistoryBatch[models.BillingLog]{LastSourceID: after}, err
	}
	switch r.kind {
	case masterdatabase.LegacyLayoutMonolith:
		rows, last, err := readSourceRows[models.UsageLog](ctx, r.db, after, limit)
		if err != nil {
			return HistoryBatch[models.BillingLog]{LastSourceID: after}, fmt.Errorf("read legacy billing: %w", err)
		}
		out := make([]models.BillingLog, len(rows))
		for i := range rows {
			out[i] = billingLogFromUsage(rows[i])
			out[i].ID = 0
		}
		return HistoryBatch[models.BillingLog]{Rows: out, LastSourceID: last}, nil
	case masterdatabase.LegacyLayoutV5Core:
		rows, last, err := readSourceRows[models.BillingLog](ctx, r.db, after, limit)
		if err != nil {
			return HistoryBatch[models.BillingLog]{LastSourceID: after}, fmt.Errorf("read legacy billing: %w", err)
		}
		for i := range rows {
			rows[i].ID = 0
		}
		return HistoryBatch[models.BillingLog]{Rows: rows, LastSourceID: last}, nil
	default:
		return HistoryBatch[models.BillingLog]{LastSourceID: after}, fmt.Errorf("read legacy billing: unsupported layout %q", r.kind)
	}
}

func (r *LegacyReader) ReadRequests(ctx context.Context, after uint, limit int) (HistoryBatch[models.RequestLog], error) {
	if err := r.validate(ctx, limit); err != nil {
		return HistoryBatch[models.RequestLog]{LastSourceID: after}, err
	}
	if r.kind == masterdatabase.LegacyLayoutV5Core {
		return HistoryBatch[models.RequestLog]{LastSourceID: after}, nil
	}
	if r.kind != masterdatabase.LegacyLayoutMonolith {
		return HistoryBatch[models.RequestLog]{LastSourceID: after}, fmt.Errorf("read legacy requests: unsupported layout %q", r.kind)
	}
	rows, last, err := readSourceRows[models.UsageLog](ctx, r.db, after, limit)
	if err != nil {
		return HistoryBatch[models.RequestLog]{LastSourceID: after}, fmt.Errorf("read legacy requests: %w", err)
	}
	out := make([]models.RequestLog, len(rows))
	for i := range rows {
		rows[i].ID = 0
		out[i] = models.RequestLog(rows[i])
	}
	return HistoryBatch[models.RequestLog]{Rows: out, LastSourceID: last}, nil
}

func (r *LegacyReader) ReadTraces(ctx context.Context, after uint, limit int) (HistoryBatch[models.RequestTrace], error) {
	if err := r.validate(ctx, limit); err != nil {
		return HistoryBatch[models.RequestTrace]{LastSourceID: after}, err
	}
	if r.kind == masterdatabase.LegacyLayoutV5Core {
		return HistoryBatch[models.RequestTrace]{LastSourceID: after}, nil
	}
	if r.kind != masterdatabase.LegacyLayoutMonolith {
		return HistoryBatch[models.RequestTrace]{LastSourceID: after}, fmt.Errorf("read legacy traces: unsupported layout %q", r.kind)
	}
	rows, last, err := readSourceRows[models.UsageLogTrace](ctx, r.db, after, limit)
	if err != nil {
		return HistoryBatch[models.RequestTrace]{LastSourceID: after}, fmt.Errorf("read legacy traces: %w", err)
	}
	out := make([]models.RequestTrace, len(rows))
	for i := range rows {
		rows[i].ID = 0
		out[i] = models.RequestTrace(rows[i])
	}
	return HistoryBatch[models.RequestTrace]{Rows: out, LastSourceID: last}, nil
}

func (r *LegacyReader) HasLogHistory() bool {
	return r != nil && r.kind == masterdatabase.LegacyLayoutMonolith
}

func (r *LegacyReader) validate(ctx context.Context, limit int) error {
	if ctx == nil {
		return fmt.Errorf("legacy history context is nil")
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if r == nil || r.db == nil {
		return fmt.Errorf("legacy history database is unavailable")
	}
	if limit <= 0 {
		return fmt.Errorf("legacy history batch limit must be positive")
	}
	return nil
}

func readSourceRows[T any](ctx context.Context, db *gorm.DB, after uint, limit int) ([]T, uint, error) {
	var rows []T
	if err := db.WithContext(ctx).Where("id > ?", after).Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return nil, after, err
	}
	if len(rows) == 0 {
		return rows, after, nil
	}
	last, err := sourceRowID(any(rows[len(rows)-1]))
	if err != nil {
		return nil, after, err
	}
	return rows, last, nil
}

func sourceRowID(row any) (uint, error) {
	switch typed := row.(type) {
	case models.UsageLog:
		return typed.ID, nil
	case models.BillingLog:
		return typed.ID, nil
	case models.UsageLogTrace:
		return typed.ID, nil
	default:
		return 0, fmt.Errorf("unsupported legacy history row %T", row)
	}
}
