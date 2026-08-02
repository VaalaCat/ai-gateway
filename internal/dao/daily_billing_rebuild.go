package dao

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

const dailyDateLayout = "2006-01-02"

const tokenDailyAggregateQuery = `
	WITH ranked AS (
		SELECT
			id, user_id, token_id, token_name, status,
			prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
			input_cost, output_cost, total_cost, created_at,
			ROW_NUMBER() OVER (
				PARTITION BY user_id, token_id
				ORDER BY created_at DESC, id DESC
			) AS descriptor_rank
		FROM request_logs
		WHERE created_at >= ? AND created_at < ?
	)
	SELECT
	user_id,
	token_id,
	MAX(CASE WHEN descriptor_rank = 1 THEN token_name END) AS token_name,
	COUNT(*) AS request_count,
	SUM(CASE WHEN status <> 0 THEN 1 ELSE 0 END) AS success_count,
	SUM(CASE WHEN status = 0 THEN 1 ELSE 0 END) AS failed_count,
	SUM(prompt_tokens) AS prompt_tokens,
	SUM(completion_tokens) AS completion_tokens,
	SUM(cache_read_tokens) AS cache_read_tokens,
	SUM(cache_write_tokens) AS cache_write_tokens,
	SUM(input_cost) AS input_cost,
	SUM(output_cost) AS output_cost,
	SUM(total_cost) AS total_cost,
	MAX(created_at) AS last_used_at
	FROM ranked
	GROUP BY user_id, token_id`

const channelDailyAggregateQuery = `
	WITH ranked AS (
		SELECT
			id, channel_id, private_channel_id, owner_type, channel_name, channel_type, status,
			prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
			input_cost, output_cost, total_cost,
			raw_input_cost, raw_output_cost, raw_cache_read_cost, raw_cache_write_cost,
			created_at,
			ROW_NUMBER() OVER (
				PARTITION BY channel_id, private_channel_id
				ORDER BY created_at DESC, id DESC
			) AS descriptor_rank
		FROM request_logs
		WHERE created_at >= ? AND created_at < ?
	)
	SELECT
	channel_id,
	private_channel_id,
	COALESCE(NULLIF(MAX(CASE WHEN descriptor_rank = 1 THEN owner_type END), ''), 'admin') AS owner_type,
	MAX(CASE WHEN descriptor_rank = 1 THEN channel_name END) AS channel_name,
	MAX(CASE WHEN descriptor_rank = 1 THEN channel_type END) AS channel_type,
	COUNT(*) AS request_count,
	SUM(CASE WHEN status <> 0 THEN 1 ELSE 0 END) AS success_count,
	SUM(CASE WHEN status = 0 THEN 1 ELSE 0 END) AS failed_count,
	SUM(prompt_tokens) AS prompt_tokens,
	SUM(completion_tokens) AS completion_tokens,
	SUM(cache_read_tokens) AS cache_read_tokens,
	SUM(cache_write_tokens) AS cache_write_tokens,
	SUM(input_cost) AS input_cost,
	SUM(output_cost) AS output_cost,
	SUM(total_cost) AS total_cost,
	SUM(CASE
		WHEN raw_input_cost IS NULL AND raw_output_cost IS NULL
			AND raw_cache_read_cost IS NULL AND raw_cache_write_cost IS NULL
		THEN total_cost
		ELSE COALESCE(raw_input_cost, 0) + COALESCE(raw_output_cost, 0)
			+ COALESCE(raw_cache_read_cost, 0) + COALESCE(raw_cache_write_cost, 0)
	END) AS raw_cost,
	MAX(created_at) AS last_used_at
	FROM ranked
	GROUP BY channel_id, private_channel_id`

type RequestLogDateBounds struct {
	StartDate string
	EndDate   string
	Empty     bool
}

// LogDailyBillingRebuilder is the persistence boundary shared by automatic
// backfill and manually submitted daily rebuild jobs.
type LogDailyBillingRebuilder interface {
	FindRequestLogDateBounds(context.Context) (RequestLogDateBounds, error)
	FindNextRequestLogDate(context.Context, string, string) (string, bool, error)
	RebuildLogDailyDate(context.Context, string, uint, DailyBillingRebuildTargets) (*BillingRebuildResult, error)
}

type logDailyBillingRebuilder struct {
	app AppProvider
}

func NewLogDailyBillingRebuilder(app AppProvider) LogDailyBillingRebuilder {
	return &logDailyBillingRebuilder{app: app}
}

func (r *logDailyBillingRebuilder) FindRequestLogDateBounds(ctx context.Context) (RequestLogDateBounds, error) {
	db, err := r.logDatabase(ctx)
	if err != nil {
		return RequestLogDateBounds{}, err
	}
	var row struct {
		Minimum sql.NullInt64
		Maximum sql.NullInt64
	}
	if err := db.Model(&models.RequestLog{}).
		Select("MIN(created_at) AS minimum, MAX(created_at) AS maximum").
		Scan(&row).Error; err != nil {
		return RequestLogDateBounds{}, fmt.Errorf("find request log date bounds: %w", err)
	}
	if !row.Minimum.Valid || !row.Maximum.Valid {
		return RequestLogDateBounds{Empty: true}, nil
	}
	return RequestLogDateBounds{
		StartDate: time.Unix(row.Minimum.Int64, 0).UTC().Format(dailyDateLayout),
		EndDate:   time.Unix(row.Maximum.Int64, 0).UTC().Format(dailyDateLayout),
	}, nil
}

func (r *logDailyBillingRebuilder) FindNextRequestLogDate(ctx context.Context, after, end string) (string, bool, error) {
	db, err := r.logDatabase(ctx)
	if err != nil {
		return "", false, err
	}
	afterDate, err := parseDailyDate(after)
	if err != nil {
		return "", false, fmt.Errorf("find next request log date: parse after: %w", err)
	}
	endDate, err := parseDailyDate(end)
	if err != nil {
		return "", false, fmt.Errorf("find next request log date: parse end: %w", err)
	}
	if !afterDate.Before(endDate) {
		return "", false, nil
	}
	var row struct{ CreatedAt int64 }
	result := db.Model(&models.RequestLog{}).
		Select("created_at").
		Where("created_at >= ? AND created_at < ?", afterDate.AddDate(0, 0, 1).Unix(), endDate.AddDate(0, 0, 1).Unix()).
		Order("created_at ASC").
		Limit(1).
		Take(&row)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if result.Error != nil {
		return "", false, fmt.Errorf("find next request log date: %w", result.Error)
	}
	return time.Unix(row.CreatedAt, 0).UTC().Format(dailyDateLayout), true, nil
}

func (r *logDailyBillingRebuilder) RebuildLogDailyDate(ctx context.Context, date string, version uint, targets DailyBillingRebuildTargets) (*BillingRebuildResult, error) {
	if ctx == nil {
		return nil, errors.New("rebuild log daily date: nil context")
	}
	day, err := parseDailyDate(date)
	if err != nil {
		return nil, fmt.Errorf("rebuild log daily date: %w", err)
	}
	db, err := r.logDatabase(ctx)
	if err != nil {
		return nil, err
	}
	start, end := day.Unix(), day.AddDate(0, 0, 1).Unix()
	result := &BillingRebuildResult{EffectiveFrom: start, EffectiveTo: end}
	err = db.Transaction(func(tx *gorm.DB) error {
		if !targets.TokenDaily && !targets.ChannelDaily {
			return ErrInvalidRebuildTarget
		}
		if err := applyDailyRebuildTargets(tx, date, start, end, targets); err != nil {
			return err
		}
		replayed, err := countRequestLogsForDailyRebuild(tx, start, end)
		if err != nil {
			return err
		}
		if version > 0 {
			checkpoint := tx.Model(&models.DailyBillingBackfill{}).
				Where("version = ?", version).
				Updates(map[string]any{"last_completed_date": date, "updated_at_unix": time.Now().Unix()})
			if checkpoint.Error != nil {
				return fmt.Errorf("update daily billing checkpoint: %w", checkpoint.Error)
			}
			if checkpoint.RowsAffected != 1 {
				return fmt.Errorf("update daily billing checkpoint version %d: marker not found", version)
			}
		}
		result.ReplayedLogs = replayed
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("rebuild log daily date %s: %w", date, err)
	}
	return result, nil
}

func (r *logDailyBillingRebuilder) logDatabase(ctx context.Context) (*gorm.DB, error) {
	if r == nil || r.app == nil {
		return nil, fmt.Errorf("%w: application is nil", ErrLogDatabaseUnavailable)
	}
	db := r.app.GetLogDB()
	if db == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrLogDatabaseUnavailable)
	}
	if db.Error != nil {
		return nil, fmt.Errorf("%w: %v", ErrLogDatabaseUnavailable, db.Error)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(ctx), nil
}

func parseDailyDate(date string) (time.Time, error) {
	parsed, err := time.Parse(dailyDateLayout, date)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date %q: %w", date, err)
	}
	return parsed.UTC(), nil
}

func applyDailyRebuildTargets(tx *gorm.DB, date string, start, end int64, targets DailyBillingRebuildTargets) error {
	actions := []struct {
		enabled bool
		delete  func() error
		rebuild func() error
	}{
		{targets.TokenDaily,
			func() error { return deleteTokenDailyDate(tx, date) },
			func() error { return rebuildTokenDailyDate(tx, date, start, end) }},
		{targets.ChannelDaily,
			func() error { return deleteChannelDailyDate(tx, date) },
			func() error { return rebuildChannelDailyDate(tx, date, start, end) }},
	}
	for _, action := range actions {
		if action.enabled {
			if err := action.delete(); err != nil {
				return err
			}
		}
	}
	for _, action := range actions {
		if action.enabled {
			if err := action.rebuild(); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteTokenDailyDate(tx *gorm.DB, date string) error {
	if err := tx.Where("date = ?", date).Delete(&models.TokenDailyBilling{}).Error; err != nil {
		return fmt.Errorf("delete token daily billing: %w", err)
	}
	return nil
}

func deleteChannelDailyDate(tx *gorm.DB, date string) error {
	if err := tx.Where("date = ?", date).Delete(&models.ChannelDailyBilling{}).Error; err != nil {
		return fmt.Errorf("delete channel daily billing: %w", err)
	}
	return nil
}

func rebuildTokenDailyDate(tx *gorm.DB, date string, start, end int64) error {
	var rows []models.TokenDailyBilling
	if err := tx.Raw(tokenDailyAggregateQuery, start, end).Scan(&rows).Error; err != nil {
		return fmt.Errorf("aggregate token daily billing: %w", err)
	}
	for i := range rows {
		rows[i].Date = date
		rows[i].CreatedAt = rows[i].LastUsedAt
		rows[i].UpdatedAt = rows[i].LastUsedAt
	}
	if len(rows) > 0 {
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("insert token daily billing: %w", err)
		}
	}
	return nil
}

func countRequestLogsForDailyRebuild(tx *gorm.DB, start, end int64) (int64, error) {
	var replayed int64
	if err := tx.Model(&models.RequestLog{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Count(&replayed).Error; err != nil {
		return 0, fmt.Errorf("count rebuilt request logs: %w", err)
	}
	return replayed, nil
}

func rebuildChannelDailyDate(tx *gorm.DB, date string, start, end int64) error {
	var rows []models.ChannelDailyBilling
	if err := tx.Raw(channelDailyAggregateQuery, start, end).Scan(&rows).Error; err != nil {
		return fmt.Errorf("aggregate channel daily billing: %w", err)
	}
	for i := range rows {
		rows[i].Date = date
		rows[i].CreatedAt = rows[i].LastUsedAt
		rows[i].UpdatedAt = rows[i].LastUsedAt
	}
	if len(rows) == 0 {
		return nil
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("insert channel daily billing: %w", err)
	}
	return nil
}
