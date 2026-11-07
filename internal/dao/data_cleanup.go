package dao

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

var (
	// ErrCleanupTableNotAllowed rejects every table outside the static cleanup registry.
	ErrCleanupTableNotAllowed = errors.New("cleanup table not allowed")
	// ErrInvalidCleanupSnapshot identifies a malformed preview watermark supplied to deletion.
	ErrInvalidCleanupSnapshot = errors.New("invalid cleanup snapshot")
)

type CleanupTablePolicy string

const (
	CleanupTablePolicyPlain CleanupTablePolicy = "plain"
)

// CleanupTableSpec is a static, audited mapping from an API table key to its physical table.
type CleanupTableSpec struct {
	Database        string
	Name            string
	TimeColumn      string
	SnapshotColumn  string
	DeleteKeyColumn string
	Policy          CleanupTablePolicy
}

type CleanupTableRequest struct {
	Database       string
	Table          string
	Cutoff         time.Time
	SnapshotMaxKey string
}

type CleanupTablePreview struct {
	Database       string
	Table          string
	Total          int64
	ToDelete       int64
	SnapshotMaxKey string
}

type CleanupBatchResult struct {
	Deleted int64
	HasMore bool
}

var cleanupTableRegistry = []CleanupTableSpec{
	{Database: "core", Name: "billing_logs", TimeColumn: "created_at", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "channel_daily_billings", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "request_logs", TimeColumn: "created_at", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "request_traces", TimeColumn: "created_at", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "token_daily_billings", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "usage_duration_histograms", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "usage_hourly_buckets", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "usage_tps_histograms", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "usage_ttft_histograms", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "usage_user_tps_histograms", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
	{Database: "log", Name: "usage_user_ttft_histograms", TimeColumn: "date", SnapshotColumn: "id", DeleteKeyColumn: "id", Policy: CleanupTablePolicyPlain},
}

var cleanupTableByKey = makeCleanupTableIndex(cleanupTableRegistry)

func makeCleanupTableIndex(specs []CleanupTableSpec) map[string]CleanupTableSpec {
	result := make(map[string]CleanupTableSpec, len(specs))
	for _, spec := range specs {
		result[cleanupTableKey(spec.Database, spec.Name)] = spec
	}
	return result
}

func cleanupTableKey(database, table string) string { return database + ":" + table }

// CleanupTableKeys returns all allowed physical table keys in deterministic order.
func CleanupTableKeys() []string {
	keys := make([]string, 0, len(cleanupTableRegistry))
	for _, spec := range cleanupTableRegistry {
		keys = append(keys, cleanupTableKey(spec.Database, spec.Name))
	}
	sort.Strings(keys)
	return keys
}

// FindCleanupTable performs only exact matching against the static registry.
func FindCleanupTable(database, table string) (CleanupTableSpec, bool) {
	spec, ok := cleanupTableByKey[cleanupTableKey(database, table)]
	return spec, ok
}

// PreviewCleanupTable counts the complete physical table and the eligible rows frozen by its watermark.
func PreviewCleanupTable(ctx context.Context, coreDB, logDB *gorm.DB, req CleanupTableRequest) (CleanupTablePreview, error) {
	spec, db, err := cleanupTargetDB(coreDB, logDB, req.Database, req.Table)
	if err != nil {
		return CleanupTablePreview{}, err
	}
	db = db.WithContext(ctx)
	preview := CleanupTablePreview{Database: spec.Database, Table: spec.Name}
	if err := db.Table(spec.Name).Count(&preview.Total).Error; err != nil {
		return CleanupTablePreview{}, err
	}
	var aggregate cleanupPreviewAggregate
	if err := cleanupEligibleScope(db, spec, req.Cutoff).
		Select("COUNT(*) AS to_delete, MAX(" + spec.SnapshotColumn + ") AS snapshot_max_key").
		Scan(&aggregate).Error; err != nil {
		return CleanupTablePreview{}, err
	}
	preview.ToDelete = aggregate.ToDelete
	if aggregate.SnapshotMaxKey.Valid {
		preview.SnapshotMaxKey = strconv.FormatInt(aggregate.SnapshotMaxKey.Int64, 10)
	}
	return preview, nil
}

type cleanupPreviewAggregate struct {
	ToDelete       int64
	SnapshotMaxKey sql.NullInt64
}

// DeleteCleanupTableBatch 按截止时间和预览上限，在一个写事务中删除最多 500 条记录。
func DeleteCleanupTableBatch(ctx context.Context, coreDB, logDB *gorm.DB, req CleanupTableRequest) (CleanupBatchResult, error) {
	// 1. 校验请求上下文、清理表和预览上限。
	const limit = 500
	if err := ctx.Err(); err != nil {
		return CleanupBatchResult{}, err
	}
	spec, db, err := cleanupTargetDB(coreDB, logDB, req.Database, req.Table)
	if err != nil {
		return CleanupBatchResult{}, err
	}
	snapshot, err := parseCleanupSnapshot(req.SnapshotMaxKey)
	if err != nil {
		return CleanupBatchResult{}, err
	}

	// 2. 在一条 DELETE 中选取并删除本批记录，由 GORM 提交或回滚写事务。
	db = db.WithContext(ctx)
	keys := cleanupEligibleScope(db, spec, req.Cutoff).
		Select(spec.DeleteKeyColumn).
		Where(spec.SnapshotColumn+" <= ?", snapshot).
		Order(spec.SnapshotColumn + " ASC").
		Order(spec.DeleteKeyColumn + " ASC").
		Limit(limit)
	deleted := db.Table(spec.Name).Where(spec.DeleteKeyColumn+" IN (?)", keys).Delete(nil)
	if deleted.Error != nil {
		if err := ctx.Err(); err != nil {
			return CleanupBatchResult{}, err
		}
		return CleanupBatchResult{}, deleted.Error
	}

	// 3. 返回实际删除条数，删满一批时继续请求下一批。
	return CleanupBatchResult{Deleted: deleted.RowsAffected, HasMore: deleted.RowsAffected == int64(limit)}, nil
}

func cleanupTargetDB(coreDB, logDB *gorm.DB, database, table string) (CleanupTableSpec, *gorm.DB, error) {
	spec, ok := FindCleanupTable(database, table)
	if !ok {
		return CleanupTableSpec{}, nil, ErrCleanupTableNotAllowed
	}
	if spec.Database == "log" {
		if logDB == nil {
			return CleanupTableSpec{}, nil, ErrLogDatabaseUnavailable
		}
		return spec, logDB, nil
	}
	if coreDB == nil {
		return CleanupTableSpec{}, nil, fmt.Errorf("core database unavailable")
	}
	return spec, coreDB, nil
}

func cleanupEligibleScope(db *gorm.DB, spec CleanupTableSpec, cutoff time.Time) *gorm.DB {
	scope := db.Table(spec.Name)
	switch spec.TimeColumn {
	case "date":
		scope = scope.Where(spec.TimeColumn+" < ?", cutoff.UTC().Format("2006-01-02"))
	default:
		scope = scope.Where(spec.TimeColumn+" < ?", cutoff.Unix())
	}
	return scope
}

func parseCleanupSnapshot(value string) (uint, error) {
	snapshot, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil {
		return 0, errors.Join(ErrInvalidCleanupSnapshot, err)
	}
	return uint(snapshot), nil
}

type BillingLogDeleteCursor struct {
	CreatedAt int64
	ID        uint
}

type BillingLogDeleteBatchResult struct {
	Deleted int64
	HasMore bool
	Next    BillingLogDeleteCursor
}

func DeleteBillingLogsBeforeBatch(ctx context.Context, db *gorm.DB, cutoff time.Time, after BillingLogDeleteCursor, limit int) (BillingLogDeleteBatchResult, error) {
	if err := ctx.Err(); err != nil {
		return BillingLogDeleteBatchResult{}, err
	}
	if db == nil {
		return BillingLogDeleteBatchResult{}, fmt.Errorf("core database unavailable")
	}
	if limit <= 0 {
		return BillingLogDeleteBatchResult{}, fmt.Errorf("billing log batch limit must be positive")
	}
	var result BillingLogDeleteBatchResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var keys []BillingLogDeleteCursor
		query := tx.Model(&models.BillingLog{}).
			Select("created_at, id").
			Where("created_at < ?", cutoff.UTC().Unix())
		if after.ID != 0 {
			query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", after.CreatedAt, after.CreatedAt, after.ID)
		}
		if err := query.Order("created_at ASC").Order("id ASC").Limit(limit + 1).Scan(&keys).Error; err != nil {
			return err
		}
		result.HasMore = len(keys) > limit
		if result.HasMore {
			keys = keys[:limit]
		}
		if len(keys) == 0 {
			return nil
		}
		ids := make([]uint, 0, len(keys))
		for _, key := range keys {
			ids = append(ids, key.ID)
		}
		deleted := tx.Where("id IN ?", ids).Delete(&models.BillingLog{})
		if deleted.Error != nil {
			return deleted.Error
		}
		result.Deleted = deleted.RowsAffected
		result.Next = keys[len(keys)-1]
		return nil
	})
	if err != nil {
		return BillingLogDeleteBatchResult{}, err
	}
	return result, nil
}
