package dao

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/safeint"
	"gorm.io/gorm"
)

const MarketplacePerformanceHours = 30 * 24

type MarketplacePerformanceOfferKind string

const (
	MarketplacePerformanceOfferPlatform MarketplacePerformanceOfferKind = "platform"
	MarketplacePerformanceOfferPrivate  MarketplacePerformanceOfferKind = "private"
)

// MarketplacePerformanceOfferKey is the complete internal identity used for
// log facts and cache maps. It is deliberately unrelated to public offer_ref.
type MarketplacePerformanceOfferKey struct {
	ModelName string
	Kind      MarketplacePerformanceOfferKind
	SourceID  uint
}

// PerformanceHistogram keeps the existing fixed 17-slot codec plus the
// observed maximum used to interpolate the overflow slot.
type PerformanceHistogram struct {
	Counts [17]int64
	Max    int64
}

// PerformanceComponents preserves every numerator and denominator needed to
// reproject summaries, health, and trends without averaging averages.
type PerformanceComponents struct {
	RequestCount              int64
	SuccessCount              int64
	StreamRequestCount        int64
	SumFirstResponseMs        int64
	SumStreamCompletionTokens int64
	SumGenerationMs           int64
	InputTokens               int64
	OutputTokens              int64
	CacheReadTokens           int64
	CacheWriteTokens          int64
	TTFTHistogram             PerformanceHistogram
	TPSHistogram              PerformanceHistogram
	DurationHistogram         PerformanceHistogram
}

// Merge returns a new set of components. Integer overflow is an invalid
// aggregate instead of silently wrapping into a healthy-looking metric.
func (c PerformanceComponents) Merge(other PerformanceComponents) (PerformanceComponents, error) {
	left := []*int64{
		&c.RequestCount, &c.SuccessCount, &c.StreamRequestCount,
		&c.SumFirstResponseMs, &c.SumStreamCompletionTokens, &c.SumGenerationMs,
		&c.InputTokens, &c.OutputTokens, &c.CacheReadTokens, &c.CacheWriteTokens,
	}
	right := []int64{
		other.RequestCount, other.SuccessCount, other.StreamRequestCount,
		other.SumFirstResponseMs, other.SumStreamCompletionTokens, other.SumGenerationMs,
		other.InputTokens, other.OutputTokens, other.CacheReadTokens, other.CacheWriteTokens,
	}
	for i := range left {
		value, err := safeint.AddNonNegativeInt64(*left[i], right[i])
		if err != nil {
			return PerformanceComponents{}, fmt.Errorf("merge marketplace performance component %d: %w", i, err)
		}
		*left[i] = value
	}
	var err error
	if c.TTFTHistogram, err = c.TTFTHistogram.merge(other.TTFTHistogram); err != nil {
		return PerformanceComponents{}, fmt.Errorf("merge TTFT histogram: %w", err)
	}
	if c.TPSHistogram, err = c.TPSHistogram.merge(other.TPSHistogram); err != nil {
		return PerformanceComponents{}, fmt.Errorf("merge TPS histogram: %w", err)
	}
	if c.DurationHistogram, err = c.DurationHistogram.merge(other.DurationHistogram); err != nil {
		return PerformanceComponents{}, fmt.Errorf("merge duration histogram: %w", err)
	}
	return c, nil
}

func (h PerformanceHistogram) merge(other PerformanceHistogram) (PerformanceHistogram, error) {
	for i := range h.Counts {
		value, err := safeint.AddNonNegativeInt64(h.Counts[i], other.Counts[i])
		if err != nil {
			return PerformanceHistogram{}, fmt.Errorf("slot %d: %w", i, err)
		}
		h.Counts[i] = value
	}
	if other.Max > h.Max {
		h.Max = other.Max
	}
	return h, nil
}

type HourlyPerformanceComponents struct {
	Hour       time.Time
	Components PerformanceComponents
}

type ModelMarketplacePerformanceQuery interface {
	FindGlobalPerformance(context.Context, time.Time) (map[MarketplacePerformanceOfferKey][]HourlyPerformanceComponents, error)
}

type modelMarketplacePerformanceQuery struct {
	ctx Context
}

func NewModelMarketplacePerformanceQuery(ctx Context) ModelMarketplacePerformanceQuery {
	return &modelMarketplacePerformanceQuery{ctx: ctx}
}

func (q *modelMarketplacePerformanceQuery) FindGlobalPerformance(
	ctx context.Context,
	observedUntil time.Time,
) (map[MarketplacePerformanceOfferKey][]HourlyPerformanceComponents, error) {
	if observedUntil.IsZero() {
		return nil, errors.New("marketplace performance observed until is required")
	}
	if q == nil || q.ctx == nil {
		return nil, errors.New("marketplace performance DAO context is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := q.ctx.LogDB()
	if err != nil {
		return nil, fmt.Errorf("marketplace performance log database: %w", err)
	}
	currentHour := observedUntil.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-(MarketplacePerformanceHours - 1) * time.Hour)
	accumulator := make(map[MarketplacePerformanceOfferKey]map[int64]PerformanceComponents)
	if err := loadMarketplaceHourlyComponents(db.WithContext(ctx), firstHour, currentHour, accumulator); err != nil {
		return nil, err
	}
	for _, spec := range marketplaceHistogramQuerySpecs() {
		if err := loadMarketplaceHistogramComponents(db.WithContext(ctx), firstHour, currentHour, spec, accumulator); err != nil {
			return nil, err
		}
	}
	return materializeMarketplacePerformance(accumulator, firstHour), nil
}

type marketplaceHourlyAggregateRow struct {
	Date                      string
	Hour                      int
	ChannelID                 uint
	PrivateChannelID          uint
	ModelName                 string
	RequestCount              int64
	SuccessCount              int64
	StreamRequestCount        int64
	SumFirstResponseMs        int64
	SumStreamCompletionTokens int64
	SumGenerationMs           int64
	InputTokens               int64
	OutputTokens              int64
	CacheReadTokens           int64
	CacheWriteTokens          int64
}

func loadMarketplaceHourlyComponents(
	db *gorm.DB,
	firstHour time.Time,
	currentHour time.Time,
	accumulator map[MarketplacePerformanceOfferKey]map[int64]PerformanceComponents,
) error {
	var rows []marketplaceHourlyAggregateRow
	err := marketplacePerformanceRange(db.Model(&models.UsageHourlyBucket{}), firstHour, currentHour).
		Where(`(
			((owner_type = 'admin' OR owner_type = '' OR owner_type IS NULL) AND channel_id > 0 AND private_channel_id = 0)
			OR (owner_type = 'private' AND channel_id = 0 AND private_channel_id > 0)
		)`).
		Select(`date, hour, channel_id, private_channel_id, model_name,
			COALESCE(SUM(request_count), 0) AS request_count,
			COALESCE(SUM(success_count), 0) AS success_count,
			COALESCE(SUM(stream_request_count), 0) AS stream_request_count,
			COALESCE(SUM(sum_first_response_ms), 0) AS sum_first_response_ms,
			COALESCE(SUM(sum_stream_completion_tokens), 0) AS sum_stream_completion_tokens,
			COALESCE(SUM(sum_generation_ms), 0) AS sum_generation_ms,
			COALESCE(SUM(prompt_tokens), 0) AS input_tokens,
			COALESCE(SUM(completion_tokens), 0) AS output_tokens,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens`).
		Group("date, hour, channel_id, private_channel_id, model_name").
		Scan(&rows).Error
	if err != nil {
		return fmt.Errorf("find marketplace hourly performance: %w", WrapLogDatabaseError(err))
	}
	for _, row := range rows {
		key, bucket, ok, err := marketplacePerformanceCoordinates(
			row.Date, row.Hour, row.ChannelID, row.PrivateChannelID, row.ModelName,
		)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		components := PerformanceComponents{
			RequestCount: row.RequestCount, SuccessCount: row.SuccessCount,
			StreamRequestCount: row.StreamRequestCount, SumFirstResponseMs: row.SumFirstResponseMs,
			SumStreamCompletionTokens: row.SumStreamCompletionTokens, SumGenerationMs: row.SumGenerationMs,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
			CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		}
		if err := mergeMarketplacePerformanceAt(accumulator, key, bucket, components); err != nil {
			return err
		}
	}
	return nil
}

type marketplaceHistogramAggregateRow struct {
	Date             string
	Hour             int
	ChannelID        uint
	PrivateChannelID uint
	ModelName        string
	H0               int64
	H1               int64
	H2               int64
	H3               int64
	H4               int64
	H5               int64
	H6               int64
	H7               int64
	H8               int64
	H9               int64
	H10              int64
	H11              int64
	H12              int64
	H13              int64
	H14              int64
	H15              int64
	H16              int64
	MaxValue         int64
}

func (r marketplaceHistogramAggregateRow) histogram() PerformanceHistogram {
	return PerformanceHistogram{
		Counts: [17]int64{r.H0, r.H1, r.H2, r.H3, r.H4, r.H5, r.H6, r.H7, r.H8, r.H9, r.H10, r.H11, r.H12, r.H13, r.H14, r.H15, r.H16},
		Max:    r.MaxValue,
	}
}

type marketplaceHistogramQuerySpec struct {
	name      string
	model     any
	maxColumn string
	attach    func(*PerformanceComponents, PerformanceHistogram)
}

func marketplaceHistogramQuerySpecs() []marketplaceHistogramQuerySpec {
	return []marketplaceHistogramQuerySpec{
		{name: "TTFT", model: &models.UsageTTFTHistogram{}, maxColumn: "max_first_response_ms", attach: func(c *PerformanceComponents, h PerformanceHistogram) { c.TTFTHistogram = h }},
		{name: "TPS", model: &models.UsageTPSHistogram{}, maxColumn: "max_tps", attach: func(c *PerformanceComponents, h PerformanceHistogram) { c.TPSHistogram = h }},
		{name: "duration", model: &models.UsageDurationHistogram{}, maxColumn: "max_duration_ms", attach: func(c *PerformanceComponents, h PerformanceHistogram) { c.DurationHistogram = h }},
	}
}

func loadMarketplaceHistogramComponents(
	db *gorm.DB,
	firstHour time.Time,
	currentHour time.Time,
	spec marketplaceHistogramQuerySpec,
	accumulator map[MarketplacePerformanceOfferKey]map[int64]PerformanceComponents,
) error {
	columns := make([]string, 0, 18)
	for i := range 17 {
		columns = append(columns, fmt.Sprintf("COALESCE(SUM(h%d), 0) AS h%d", i, i))
	}
	columns = append(columns, fmt.Sprintf("COALESCE(MAX(%s), 0) AS max_value", spec.maxColumn))
	var rows []marketplaceHistogramAggregateRow
	err := marketplacePerformanceRange(db.Model(spec.model), firstHour, currentHour).
		Select("date, hour, channel_id, private_channel_id, model_name, " + strings.Join(columns, ", ")).
		Group("date, hour, channel_id, private_channel_id, model_name").
		Scan(&rows).Error
	if err != nil {
		return fmt.Errorf("find marketplace %s histograms: %w", spec.name, WrapLogDatabaseError(err))
	}
	for _, row := range rows {
		key, bucket, ok, coordinateErr := marketplacePerformanceCoordinates(
			row.Date, row.Hour, row.ChannelID, row.PrivateChannelID, row.ModelName,
		)
		if coordinateErr != nil {
			return coordinateErr
		}
		if !ok {
			continue
		}
		components := PerformanceComponents{}
		spec.attach(&components, row.histogram())
		if mergeErr := attachMarketplaceHistogramAt(accumulator, key, bucket, components); mergeErr != nil {
			return mergeErr
		}
	}
	return nil
}

func attachMarketplaceHistogramAt(
	accumulator map[MarketplacePerformanceOfferKey]map[int64]PerformanceComponents,
	key MarketplacePerformanceOfferKey,
	bucket int64,
	components PerformanceComponents,
) error {
	hours := accumulator[key]
	if hours == nil {
		return nil
	}
	existing, ok := hours[bucket]
	if !ok {
		return nil
	}
	merged, err := existing.Merge(components)
	if err != nil {
		return fmt.Errorf("attach marketplace histogram for model %q: %w", key.ModelName, err)
	}
	hours[bucket] = merged
	return nil
}

func marketplacePerformanceRange(db *gorm.DB, firstHour, currentHour time.Time) *gorm.DB {
	return db.Where(
		`(date > ? OR (date = ? AND hour >= ?))
		 AND (date < ? OR (date = ? AND hour <= ?))`,
		firstHour.Format(time.DateOnly), firstHour.Format(time.DateOnly), firstHour.Hour(),
		currentHour.Format(time.DateOnly), currentHour.Format(time.DateOnly), currentHour.Hour(),
	)
}

func marketplacePerformanceCoordinates(
	date string,
	hour int,
	channelID uint,
	privateChannelID uint,
	modelName string,
) (MarketplacePerformanceOfferKey, int64, bool, error) {
	if hour < 0 || hour > 23 {
		return MarketplacePerformanceOfferKey{}, 0, false, fmt.Errorf("invalid marketplace performance hour %d", hour)
	}
	day, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		return MarketplacePerformanceOfferKey{}, 0, false, fmt.Errorf("parse marketplace performance date %q: %w", date, err)
	}
	if strings.TrimSpace(modelName) == "" {
		return MarketplacePerformanceOfferKey{}, 0, false, nil
	}
	key := MarketplacePerformanceOfferKey{ModelName: modelName}
	switch {
	case channelID > 0 && privateChannelID == 0:
		key.Kind, key.SourceID = MarketplacePerformanceOfferPlatform, channelID
	case channelID == 0 && privateChannelID > 0:
		key.Kind, key.SourceID = MarketplacePerformanceOfferPrivate, privateChannelID
	default:
		return MarketplacePerformanceOfferKey{}, 0, false, nil
	}
	return key, day.Add(time.Duration(hour) * time.Hour).Unix(), true, nil
}

func mergeMarketplacePerformanceAt(
	accumulator map[MarketplacePerformanceOfferKey]map[int64]PerformanceComponents,
	key MarketplacePerformanceOfferKey,
	bucket int64,
	components PerformanceComponents,
) error {
	hours := accumulator[key]
	if hours == nil {
		hours = make(map[int64]PerformanceComponents)
		accumulator[key] = hours
	}
	merged, err := hours[bucket].Merge(components)
	if err != nil {
		return fmt.Errorf("merge marketplace performance for model %q: %w", key.ModelName, err)
	}
	hours[bucket] = merged
	return nil
}

func materializeMarketplacePerformance(
	accumulator map[MarketplacePerformanceOfferKey]map[int64]PerformanceComponents,
	firstHour time.Time,
) map[MarketplacePerformanceOfferKey][]HourlyPerformanceComponents {
	result := make(map[MarketplacePerformanceOfferKey][]HourlyPerformanceComponents, len(accumulator))
	for key, values := range accumulator {
		hours := make([]HourlyPerformanceComponents, MarketplacePerformanceHours)
		for i := range hours {
			hour := firstHour.Add(time.Duration(i) * time.Hour)
			hours[i] = HourlyPerformanceComponents{Hour: hour, Components: values[hour.Unix()]}
		}
		result[key] = hours
	}
	return result
}
