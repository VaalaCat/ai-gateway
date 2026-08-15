package genericapi

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type TokenFactFinder interface {
	FindTokenByID(context.Context, uint) (*models.Token, bool, error)
}

type APIRoleSetFinder interface {
	FindUserAPIRoleSet(context.Context, uint) (*protocol.APIRoleSet, bool, error)
	FindTokenAPIRoleSet(context.Context, uint) (*protocol.APIRoleSet, bool, error)
}

type PermissionGate struct {
	tokens   TokenFactFinder
	roleSets APIRoleSetFinder
	index    *cache.APIIndex
}

func NewPermissionGate(tokens TokenFactFinder, roleSets APIRoleSetFinder, index *cache.APIIndex) *PermissionGate {
	return &PermissionGate{tokens: tokens, roleSets: roleSets, index: index}
}

func (g *PermissionGate) AllowInvoke(
	ctx context.Context,
	tokenID, userID, serviceID, routeID uint,
) error {
	if g == nil || g.tokens == nil || g.roleSets == nil || g.index == nil {
		return ErrPermissionFactsUnavailable
	}
	if err := g.index.RequireReady(); err != nil {
		return err
	}
	token, found, err := g.tokens.FindTokenByID(ctx, tokenID)
	if err != nil || !found || token == nil || token.ID != tokenID || token.UserID != userID {
		return ErrPermissionFactsUnavailable
	}
	// behavior change: a system token has no owner whose roles it can inherit.
	if token.UserID == 0 && token.APIRoleMode == models.APIRoleModeInherit {
		return ErrAPIForbidden
	}
	roleSet, found, err := g.findRoleSet(ctx, token)
	if err != nil {
		if errors.Is(err, entitycache.ErrNotFound) {
			return ErrAPIForbidden
		}
		return ErrPermissionFactsUnavailable
	}
	if !found || roleSet == nil {
		return ErrPermissionFactsUnavailable
	}
	// behavior change: readiness and grant are checked from one final snapshot.
	allowed, err := g.index.CheckInvoke(roleSet.RoleIDs, serviceID, routeID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrAPIForbidden
	}
	return nil
}

func (g *PermissionGate) findRoleSet(
	ctx context.Context,
	token *models.Token,
) (*protocol.APIRoleSet, bool, error) {
	finders := map[models.APIRoleMode]func(context.Context, uint) (*protocol.APIRoleSet, bool, error){
		models.APIRoleModeExplicit: g.roleSets.FindTokenAPIRoleSet,
		models.APIRoleModeInherit:  g.roleSets.FindUserAPIRoleSet,
	}
	finder := finders[token.APIRoleMode]
	if finder == nil {
		return nil, false, ErrPermissionFactsUnavailable
	}
	principalID := token.ID
	if token.APIRoleMode == models.APIRoleModeInherit {
		principalID = token.UserID
	}
	return finder(ctx, principalID)
}
