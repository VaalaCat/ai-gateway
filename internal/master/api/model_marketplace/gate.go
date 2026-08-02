package model_marketplace

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

type MarketplaceEnabledFinder interface {
	Enabled(*app.Context) bool
}

type MarketplaceTokenFinder interface {
	FindOwned(*app.Context, uint, uint) (*models.Token, error)
	FindByID(*app.Context, uint) (*models.Token, error)
}

type MarketplaceUserGroupFinder interface {
	FindIdentityAndAuthorizationForUser(*app.Context, uint) (*dao.UserGroupIdentityAndAuthorization, error)
}

type MarketplaceAccessGate struct {
	Settings   MarketplaceEnabledFinder
	Tokens     MarketplaceTokenFinder
	UserGroups MarketplaceUserGroupFinder
	Now        func() time.Time
}

func NewMarketplaceAccessGate() MarketplaceAccessGate {
	return MarketplaceAccessGate{
		Settings:   coreMarketplaceEnabledFinder{},
		Tokens:     coreMarketplaceTokenFinder{},
		UserGroups: coreMarketplaceUserGroupFinder{},
		Now:        time.Now,
	}
}

func (g MarketplaceAccessGate) RequireUser(c *app.Context, tokenID uint) (MarketplaceViewer, error) {
	if g.Settings == nil || !g.Settings.Enabled(c) {
		return MarketplaceViewer{}, api.NotFoundError(consts.ErrNotFound)
	}
	scope := marketplaceRequestScope(c)
	if scope == nil || scope.UserID == 0 {
		return MarketplaceViewer{}, api.UnauthorizedError("not authenticated")
	}
	if g.Tokens == nil {
		return MarketplaceViewer{}, api.InternalError("find owned marketplace token", errors.New("token finder is unavailable"))
	}
	token, err := g.Tokens.FindOwned(c, tokenID, scope.UserID)
	if err != nil || token == nil {
		return MarketplaceViewer{}, marketplaceTokenFindError(err, "find owned marketplace token")
	}
	return g.viewerForToken(c, token)
}

func (g MarketplaceAccessGate) RequireAdmin(c *app.Context, optionalTokenID *uint) (MarketplaceViewer, error) {
	scope := marketplaceRequestScope(c)
	if scope == nil || !scope.IsAdmin {
		return MarketplaceViewer{}, api.ForbiddenError(consts.ErrAdminOnly)
	}
	if optionalTokenID == nil {
		return MarketplaceViewer{AdminGlobal: true}, nil
	}
	if g.Tokens == nil {
		return MarketplaceViewer{}, api.InternalError("find marketplace token", errors.New("token finder is unavailable"))
	}
	token, err := g.Tokens.FindByID(c, *optionalTokenID)
	if err != nil || token == nil {
		return MarketplaceViewer{}, marketplaceTokenFindError(err, "find marketplace token")
	}
	return g.viewerForToken(c, token)
}

func (g MarketplaceAccessGate) viewerForToken(c *app.Context, token *models.Token) (MarketplaceViewer, error) {
	if err := g.requireUsableToken(token); err != nil {
		return MarketplaceViewer{}, err
	}
	tokenPatterns, err := parseMarketplaceModelPatterns(token.Models)
	if err != nil {
		return MarketplaceViewer{}, api.InternalError("parse marketplace token models", err)
	}
	tokenChannelIDs := normalizeMarketplaceChannelIDs(token.AllowedChannelIDs)
	viewer := MarketplaceViewer{
		UserID:                 token.UserID,
		Token:                  token,
		TokenAllowedChannelIDs: slices.Clone(tokenChannelIDs),
		AllowedChannelIDs:      tokenChannelIDs,
		AllowedModels:          MarketplaceModelWhitelist{TokenPatterns: tokenPatterns},
		BYOKOnly:               token.BYOKOnly,
	}
	return g.applyUserGroup(c, viewer)
}

func (g MarketplaceAccessGate) applyUserGroup(c *app.Context, viewer MarketplaceViewer) (MarketplaceViewer, error) {
	if g.UserGroups == nil {
		return MarketplaceViewer{}, api.InternalError("find marketplace user group", errors.New("user group finder is unavailable"))
	}
	groupAccess, err := g.UserGroups.FindIdentityAndAuthorizationForUser(c, viewer.UserID)
	if err != nil || groupAccess == nil {
		if err == nil {
			err = gorm.ErrRecordNotFound
		}
		return MarketplaceViewer{}, api.InternalError("find marketplace user group", err)
	}
	group := &groupAccess.AuthorizationGroup
	if models.UserGroupAccessDisabled(group) {
		return MarketplaceViewer{}, api.ErrorWithCode(
			http.StatusUnprocessableEntity,
			"marketplace_user_group_disabled",
			"user group disabled",
			nil,
		)
	}
	groupPatterns, err := parseMarketplaceModelPatterns(group.Models)
	if err != nil {
		return MarketplaceViewer{}, api.InternalError("parse marketplace user group models", err)
	}
	identityGroupID := groupAccess.IdentityGroupID
	if identityGroupID == 0 {
		identityGroupID = models.DefaultUserGroupID
	}
	groupChannelIDs := normalizeMarketplaceChannelIDs(group.AllowedChannelIDs)
	viewer.GroupID = identityGroupID
	viewer.GroupIDs = []uint{identityGroupID}
	viewer.GroupAllowedChannelIDs = slices.Clone(groupChannelIDs)
	viewer.AllowedChannelIDs = intersectMarketplaceChannelIDs(
		viewer.AllowedChannelIDs,
		groupChannelIDs,
	)
	viewer.AllowedModels.GroupPatterns = groupPatterns
	return viewer, nil
}

func (g MarketplaceAccessGate) requireUsableToken(token *models.Token) error {
	if token.Status != consts.StatusEnabled {
		return api.ErrorWithCode(
			http.StatusUnprocessableEntity,
			"marketplace_token_disabled",
			consts.ErrTokenDisabled,
			nil,
		)
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	if token.ExpiredAt > 0 && token.ExpiredAt < now().Unix() {
		return api.ErrorWithCode(
			http.StatusUnprocessableEntity,
			"marketplace_token_expired",
			consts.ErrTokenExpired,
			nil,
		)
	}
	return nil
}

func marketplaceRequestScope(c *app.Context) *middleware.RequestScope {
	if c == nil || c.Context == nil {
		return nil
	}
	return middleware.GetScope(c.Context)
}

func marketplaceTokenFindError(err error, message string) error {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return api.NotFoundError(consts.ErrNotFound)
	}
	return api.InternalError(message, err)
}

func parseMarketplaceModelPatterns(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var patterns []string
	if err := json.Unmarshal([]byte(raw), &patterns); err != nil {
		return nil, err
	}
	return slices.Clone(patterns), nil
}

func normalizeMarketplaceChannelIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(ids))
	normalized := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

func intersectMarketplaceChannelIDs(tokenIDs, groupIDs []uint) []uint {
	if tokenIDs == nil {
		return slices.Clone(groupIDs)
	}
	if groupIDs == nil {
		return slices.Clone(tokenIDs)
	}
	groupSet := make(map[uint]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		groupSet[id] = struct{}{}
	}
	intersection := make([]uint, 0)
	for _, id := range tokenIDs {
		if _, allowed := groupSet[id]; allowed {
			intersection = append(intersection, id)
		}
	}
	return intersection
}

type coreMarketplaceEnabledFinder struct{}

func (coreMarketplaceEnabledFinder) Enabled(c *app.Context) bool {
	return modelMarketplaceEnabled(c.App.GetMasterSettings())
}

type coreMarketplaceTokenFinder struct{}

func (coreMarketplaceTokenFinder) FindOwned(c *app.Context, tokenID, userID uint) (*models.Token, error) {
	return marketplaceAdminQuery(c).Token().FindOwned(tokenID, userID)
}

func (coreMarketplaceTokenFinder) FindByID(c *app.Context, tokenID uint) (*models.Token, error) {
	return marketplaceAdminQuery(c).Token().GetByID(tokenID)
}

type coreMarketplaceUserGroupFinder struct{}

func (coreMarketplaceUserGroupFinder) FindIdentityAndAuthorizationForUser(
	c *app.Context,
	userID uint,
) (*dao.UserGroupIdentityAndAuthorization, error) {
	return marketplaceAdminQuery(c).UserGroup().FindIdentityAndAuthorizationForUser(userID)
}

func marketplaceAdminQuery(c *app.Context) dao.AdminQuery {
	return dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
}
