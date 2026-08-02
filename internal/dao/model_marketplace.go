package dao

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

// MarketplacePrivateChannelScope describes which BYOK channels a marketplace
// viewer may see. AdminGlobal is reserved for the administrator's unscoped
// diagnostic view; scoped views use UserID and GroupIDs for authorization but
// retain disabled metadata. Runtime availability comes only from the embedded
// Agent planner.
type MarketplacePrivateChannelScope struct {
	UserID      uint
	GroupIDs    []uint
	AdminGlobal bool
}

// MarketplaceRoutingScope describes the concrete runtime routing view. A token
// preview carries both its token and owner; GroupIDs are retained so every
// marketplace finder receives the same authorization context even though the
// current model_routings schema has no group-owned scope.
type MarketplaceRoutingScope struct {
	UserID      uint
	TokenID     uint
	GroupIDs    []uint
	AdminGlobal bool
}

// ModelMarketplaceQuery is the batched read surface used to compose the model
// marketplace. Every method issues at most one SQL query regardless of how many
// models are returned.
type ModelMarketplaceQuery interface {
	ListEnabledMarketplaceModels(context.Context) ([]models.ModelConfig, error)
	ListMarketplaceChannels(context.Context) ([]models.Channel, error)
	ListMarketplacePrivateChannels(context.Context, MarketplacePrivateChannelScope) ([]models.PrivateChannel, error)
	ListMarketplaceRoutings(context.Context, MarketplaceRoutingScope) ([]models.ModelRouting, error)
}

type modelMarketplaceQuery struct {
	ctx Context
}

func NewModelMarketplaceQuery(ctx Context) ModelMarketplaceQuery {
	return &modelMarketplaceQuery{ctx: ctx}
}

func (q *modelMarketplaceQuery) ListEnabledMarketplaceModels(ctx context.Context) ([]models.ModelConfig, error) {
	var rows []models.ModelConfig
	err := q.db(ctx).
		Where("status = ?", 1).
		Order("model_name ASC").
		Find(&rows).Error
	return rows, err
}

func (q *modelMarketplaceQuery) ListMarketplaceChannels(ctx context.Context) ([]models.Channel, error) {
	var rows []models.Channel
	err := q.db(ctx).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

func (q *modelMarketplaceQuery) ListMarketplacePrivateChannels(
	ctx context.Context,
	scope MarketplacePrivateChannelScope,
) ([]models.PrivateChannel, error) {
	if !scope.AdminGlobal && scope.UserID == 0 {
		return []models.PrivateChannel{}, nil
	}

	db := q.db(ctx).Model(&models.PrivateChannel{})
	if !scope.AdminGlobal {
		db = db.Where(`
			owner_id = ? OR id IN (SELECT channel_id FROM private_channel_shares
				WHERE (target_type = 'user' AND target_id = ?)
				   OR (target_type = 'group' AND target_id IN ?))`,
			scope.UserID, scope.UserID, normalizeGroupIDs(scope.GroupIDs))
	}

	var rows []models.PrivateChannel
	err := db.Order("id ASC").Find(&rows).Error
	return rows, err
}

// ListMarketplaceRoutings loads the entire effective routing namespace with a
// single SQL query. Top-level precedence is applied by RoutingModelFinder;
// recursive members may still need disabled global rows to emit diagnostics.
func (q *modelMarketplaceQuery) ListMarketplaceRoutings(
	ctx context.Context,
	scope MarketplaceRoutingScope,
) ([]models.ModelRouting, error) {
	if scope.AdminGlobal {
		if scope.UserID != 0 || scope.TokenID != 0 || len(scope.GroupIDs) != 0 {
			return []models.ModelRouting{}, nil
		}
		var rows []models.ModelRouting
		err := q.db(ctx).
			Where("scope = ?", models.RoutingScopeGlobal).
			Order("scope ASC, name ASC, id ASC").
			Find(&rows).Error
		return rows, err
	}
	if scope.UserID == 0 && scope.TokenID == 0 {
		return []models.ModelRouting{}, nil
	}

	db := q.db(ctx).Where("scope = ?", models.RoutingScopeGlobal)
	if scope.UserID != 0 {
		db = db.Or("scope = ? AND user_id = ?", models.RoutingScopeUser, scope.UserID)
	}
	if scope.TokenID != 0 {
		db = db.Or("scope = ? AND token_id = ?", models.RoutingScopeToken, scope.TokenID)
	}
	var rows []models.ModelRouting
	err := db.Order("scope ASC, name ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (q *modelMarketplaceQuery) db(ctx context.Context) *gorm.DB {
	db := q.ctx.GetCoreDB()
	if ctx != nil {
		db = db.WithContext(ctx)
	}
	return db
}
