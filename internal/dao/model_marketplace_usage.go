package dao

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/safeint"
	"gorm.io/gorm"
)

type MarketplaceUsageOfferKind string

const (
	MarketplaceUsageOfferPlatform MarketplaceUsageOfferKind = "platform"
	MarketplaceUsageOfferPrivate  MarketplaceUsageOfferKind = "private"
)

var ErrMarketplaceUsageNotOwner = errors.New("marketplace usage offer is not owned by viewer")

// MarketplaceUsageOffer is the complete internal request-fact identity. It is
// built from canonical offers already re-enumerated for the current viewer and
// is never decoded from an HTTP request.
type MarketplaceUsageOffer struct {
	ModelName string
	Kind      MarketplaceUsageOfferKind
	SourceID  uint
}

type MarketplaceUsageRange struct {
	Start int64
	End   int64
}

type MarketplaceUsageTotals struct {
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	ReferenceCost     *int64
	GatewayChargeCost int64
}

// SelectedTokenUsageScope has no free-form filters: the current user's ID,
// selected persisted token, canonical offers, and validated window are all
// mandatory and simultaneously applied by FindSelectedTokenUsage.
type SelectedTokenUsageScope struct {
	UserID  uint
	TokenID uint
	Offers  []MarketplaceUsageOffer
	Range   MarketplaceUsageRange
}

// OwnerChannelUsageScope is constructed from MarketplaceViewer.UserID plus
// offers marked owned by server-side enumeration. The DAO rechecks every
// private_channel.owner_id in one core query before reading log facts.
type OwnerChannelUsageScope struct {
	ViewerUserID uint
	Offers       []MarketplaceUsageOffer
	Range        MarketplaceUsageRange
}

// AdminOfferUsageScope is reachable only from the administrator finder path.
// It intentionally has no caller-supplied owner/user restriction.
type AdminOfferUsageScope struct {
	Offers []MarketplaceUsageOffer
	Range  MarketplaceUsageRange
}

type ModelMarketplaceUsageQuery interface {
	FindSelectedTokenUsage(context.Context, SelectedTokenUsageScope) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error)
	FindOwnerChannelUsage(context.Context, OwnerChannelUsageScope) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error)
	FindAdminOfferUsage(context.Context, AdminOfferUsageScope) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error)
}

type modelMarketplaceUsageQuery struct {
	ctx Context
}

func NewModelMarketplaceUsageQuery(ctx Context) ModelMarketplaceUsageQuery {
	return &modelMarketplaceUsageQuery{ctx: ctx}
}

func (q *modelMarketplaceUsageQuery) FindSelectedTokenUsage(
	ctx context.Context,
	scope SelectedTokenUsageScope,
) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error) {
	if scope.UserID == 0 {
		return nil, errors.New("selected-token marketplace usage requires user")
	}
	if scope.TokenID == 0 {
		return nil, errors.New("selected-token marketplace usage requires token")
	}
	offers, err := validateMarketplaceUsageScope(scope.Offers, scope.Range, false)
	if err != nil {
		return nil, err
	}
	if len(offers) == 0 {
		return map[MarketplaceUsageOffer]MarketplaceUsageTotals{}, nil
	}
	return q.aggregate(ctx, offers, scope.Range, func(db *gorm.DB) *gorm.DB {
		return db.Where("ul.user_id = ? AND ul.token_id = ?", scope.UserID, scope.TokenID)
	})
}

func (q *modelMarketplaceUsageQuery) FindOwnerChannelUsage(
	ctx context.Context,
	scope OwnerChannelUsageScope,
) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error) {
	if scope.ViewerUserID == 0 {
		return nil, errors.New("owner-channel marketplace usage requires viewer user")
	}
	offers, err := validateMarketplaceUsageScope(scope.Offers, scope.Range, true)
	if err != nil {
		return nil, err
	}
	if len(offers) == 0 {
		return map[MarketplaceUsageOffer]MarketplaceUsageTotals{}, nil
	}
	ids := uniqueMarketplaceUsageSourceIDs(offers)
	var ownedIDs []uint
	if err := q.ctx.GetCoreDB().WithContext(ctx).
		Model(&models.PrivateChannel{}).
		Where("owner_id = ? AND id IN ?", scope.ViewerUserID, ids).
		Order("id ASC").
		Pluck("id", &ownedIDs).Error; err != nil {
		return nil, fmt.Errorf("verify marketplace usage private offer ownership: %w", err)
	}
	if len(ownedIDs) != len(ids) {
		return nil, ErrMarketplaceUsageNotOwner
	}
	return q.aggregate(ctx, offers, scope.Range, func(db *gorm.DB) *gorm.DB { return db })
}

func (q *modelMarketplaceUsageQuery) FindAdminOfferUsage(
	ctx context.Context,
	scope AdminOfferUsageScope,
) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error) {
	offers, err := validateMarketplaceUsageScope(scope.Offers, scope.Range, false)
	if err != nil {
		return nil, err
	}
	if len(offers) == 0 {
		return map[MarketplaceUsageOffer]MarketplaceUsageTotals{}, nil
	}
	return q.aggregate(ctx, offers, scope.Range, func(db *gorm.DB) *gorm.DB { return db })
}

type marketplaceUsageAggregateRow struct {
	ModelName         string
	Kind              MarketplaceUsageOfferKind
	SourceID          uint
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	ReferenceCost     int64
	ReferenceMissing  int64
	GatewayChargeCost int64
}

// marketplaceUsageOfferBatchSize keeps each exact-identity OR tree below
// SQLite's expression-depth limit while remaining far from per-offer N+1.
// Selected/admin log query count is ceil(offers/250); owner scope adds one
// batched core ownership query.
const marketplaceUsageOfferBatchSize = 250

const marketplaceUsageRawBucketsMissingExpression = `(ul.raw_input_cost IS NULL
		 AND ul.raw_output_cost IS NULL
		 AND ul.raw_cache_read_cost IS NULL
		 AND ul.raw_cache_write_cost IS NULL)`

const marketplaceUsageKnownRawCostExpression = `(COALESCE(ul.raw_input_cost, 0)
		   + COALESCE(ul.raw_output_cost, 0)
		   + COALESCE(ul.raw_cache_read_cost, 0)
		   + COALESCE(ul.raw_cache_write_cost, 0))`

const marketplaceUsageRawCostExpression = `CASE
		WHEN ` + marketplaceUsageRawBucketsMissingExpression + `
		THEN CASE
			WHEN ul.owner_type = 'admin' OR ul.owner_type = '' OR ul.owner_type IS NULL
			THEN ul.total_cost
			ELSE 0
		END
		ELSE COALESCE(ul.raw_input_cost, 0)
		   + COALESCE(ul.raw_output_cost, 0)
	   + COALESCE(ul.raw_cache_read_cost, 0)
	   + COALESCE(ul.raw_cache_write_cost, 0)
	END`

func (q *modelMarketplaceUsageQuery) aggregate(
	ctx context.Context,
	offers []MarketplaceUsageOffer,
	usageRange MarketplaceUsageRange,
	applyScope func(*gorm.DB) *gorm.DB,
) (map[MarketplaceUsageOffer]MarketplaceUsageTotals, error) {
	db, requestLogModel, err := q.ctx.RequestLogModel()
	if err != nil {
		return nil, fmt.Errorf("marketplace usage query: %w", err)
	}
	table := "usage_logs"
	if _, split := requestLogModel.(*models.RequestLog); split {
		table = "request_logs"
	}
	result := make(map[MarketplaceUsageOffer]MarketplaceUsageTotals)
	for start := 0; start < len(offers); start += marketplaceUsageOfferBatchSize {
		end := min(start+marketplaceUsageOfferBatchSize, len(offers))
		rows, err := findMarketplaceUsageBatch(db.WithContext(ctx), table, offers[start:end], usageRange, applyScope)
		if err != nil {
			return nil, err
		}
		if err := mergeMarketplaceUsageRows(result, rows); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func findMarketplaceUsageBatch(
	db *gorm.DB,
	table string,
	offers []MarketplaceUsageOffer,
	usageRange MarketplaceUsageRange,
	applyScope func(*gorm.DB) *gorm.DB,
) ([]marketplaceUsageAggregateRow, error) {
	query := db.Table(table+" AS ul").
		Where("ul.created_at >= ? AND ul.created_at < ?", usageRange.Start, usageRange.End)
	query = applyScope(query)
	query = applyMarketplaceUsageOffers(query, offers)

	var rows []marketplaceUsageAggregateRow
	err := query.Select(
		"ul.model_name AS model_name, " +
			"CASE " +
			"WHEN ul.owner_type = 'private' THEN 'private' " +
			"WHEN ul.owner_type = 'admin' OR ul.owner_type = '' OR ul.owner_type IS NULL THEN 'platform' " +
			"ELSE NULL END AS kind, " +
			"CASE " +
			"WHEN ul.owner_type = 'private' THEN ul.private_channel_id " +
			"WHEN ul.owner_type = 'admin' OR ul.owner_type = '' OR ul.owner_type IS NULL THEN ul.channel_id " +
			"ELSE NULL END AS source_id, " +
			"COALESCE(SUM(ul.prompt_tokens), 0) AS input_tokens, " +
			"COALESCE(SUM(ul.completion_tokens), 0) AS output_tokens, " +
			"COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens, " +
			"COALESCE(SUM(ul.cache_write_tokens), 0) AS cache_write_tokens, " +
			"COALESCE(SUM(" + marketplaceUsageRawCostExpression + "), 0) AS reference_cost, " +
			"COALESCE(SUM(CASE WHEN ul.owner_type = 'private' AND " + marketplaceUsageRawBucketsMissingExpression + " THEN 1 ELSE 0 END), 0) AS reference_missing, " +
			"COALESCE(SUM(ul.total_cost), 0) AS gateway_charge_cost",
	).
		Group("ul.model_name, kind, source_id").
		Scan(&rows).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	return rows, nil
}

func mergeMarketplaceUsageRows(
	result map[MarketplaceUsageOffer]MarketplaceUsageTotals,
	rows []marketplaceUsageAggregateRow,
) error {
	for _, row := range rows {
		key := MarketplaceUsageOffer{ModelName: row.ModelName, Kind: row.Kind, SourceID: row.SourceID}
		total, exists := result[key]
		var err error
		if total.InputTokens, err = safeint.AddNonNegativeInt64(total.InputTokens, row.InputTokens); err != nil {
			return fmt.Errorf("sum marketplace input tokens: %w", err)
		}
		if total.OutputTokens, err = safeint.AddNonNegativeInt64(total.OutputTokens, row.OutputTokens); err != nil {
			return fmt.Errorf("sum marketplace output tokens: %w", err)
		}
		if total.CacheReadTokens, err = safeint.AddNonNegativeInt64(total.CacheReadTokens, row.CacheReadTokens); err != nil {
			return fmt.Errorf("sum marketplace cache-read tokens: %w", err)
		}
		if total.CacheWriteTokens, err = safeint.AddNonNegativeInt64(total.CacheWriteTokens, row.CacheWriteTokens); err != nil {
			return fmt.Errorf("sum marketplace cache-write tokens: %w", err)
		}
		if total.GatewayChargeCost, err = safeint.AddNonNegativeInt64(total.GatewayChargeCost, row.GatewayChargeCost); err != nil {
			return fmt.Errorf("sum marketplace gateway charge: %w", err)
		}
		switch {
		case row.ReferenceMissing > 0:
			total.ReferenceCost = nil
		case exists && total.ReferenceCost == nil:
			// Once any row is incomplete, later known rows cannot restore completeness.
		case exists:
			value, addErr := safeint.AddNonNegativeInt64(*total.ReferenceCost, row.ReferenceCost)
			if addErr != nil {
				return fmt.Errorf("sum marketplace reference cost: %w", addErr)
			}
			total.ReferenceCost = &value
		default:
			value, addErr := safeint.AddNonNegativeInt64(row.ReferenceCost)
			if addErr != nil {
				return fmt.Errorf("sum marketplace reference cost: %w", addErr)
			}
			total.ReferenceCost = &value
		}
		result[key] = total
	}
	return nil
}

func applyMarketplaceUsageOffers(db *gorm.DB, offers []MarketplaceUsageOffer) *gorm.DB {
	conditions := make([]string, 0, len(offers))
	args := make([]any, 0, len(offers)*3)
	for _, offer := range offers {
		switch offer.Kind {
		case MarketplaceUsageOfferPlatform:
			conditions = append(conditions, "((ul.owner_type = 'admin' OR ul.owner_type = '' OR ul.owner_type IS NULL) AND ul.channel_id = ? AND ul.model_name = ?)")
			args = append(args, offer.SourceID, offer.ModelName)
		case MarketplaceUsageOfferPrivate:
			conditions = append(conditions, "(ul.owner_type = ? AND ul.private_channel_id = ? AND ul.model_name = ?)")
			args = append(args, "private", offer.SourceID, offer.ModelName)
		}
	}
	return db.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func validateMarketplaceUsageScope(
	offers []MarketplaceUsageOffer,
	usageRange MarketplaceUsageRange,
	privateOnly bool,
) ([]MarketplaceUsageOffer, error) {
	if usageRange.Start < 0 || usageRange.End <= usageRange.Start {
		return nil, errors.New("invalid marketplace usage range")
	}
	if len(offers) == 0 {
		return []MarketplaceUsageOffer{}, nil
	}
	result := append([]MarketplaceUsageOffer(nil), offers...)
	seen := make(map[MarketplaceUsageOffer]struct{}, len(result))
	for _, offer := range result {
		if strings.TrimSpace(offer.ModelName) == "" || offer.ModelName != strings.TrimSpace(offer.ModelName) {
			return nil, errors.New("marketplace usage offer model is required")
		}
		if offer.SourceID == 0 {
			return nil, errors.New("marketplace usage offer source ID is required")
		}
		if offer.Kind != MarketplaceUsageOfferPlatform && offer.Kind != MarketplaceUsageOfferPrivate {
			return nil, errors.New("invalid marketplace usage offer kind")
		}
		if privateOnly && offer.Kind != MarketplaceUsageOfferPrivate {
			return nil, errors.New("owner-channel marketplace usage accepts only private offers")
		}
		if _, duplicate := seen[offer]; duplicate {
			return nil, errors.New("duplicate marketplace usage offer")
		}
		seen[offer] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ModelName != result[j].ModelName {
			return result[i].ModelName < result[j].ModelName
		}
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].SourceID < result[j].SourceID
	})
	return result, nil
}

func uniqueMarketplaceUsageSourceIDs(offers []MarketplaceUsageOffer) []uint {
	seen := make(map[uint]struct{}, len(offers))
	result := make([]uint, 0, len(offers))
	for _, offer := range offers {
		if _, duplicate := seen[offer.SourceID]; duplicate {
			continue
		}
		seen[offer.SourceID] = struct{}{}
		result = append(result, offer.SourceID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
