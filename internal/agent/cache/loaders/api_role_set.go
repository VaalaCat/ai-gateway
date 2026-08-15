package loaders

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type UserAPIRoleSetLoader struct {
	Client app.WSClient
}

func (l *UserAPIRoleSetLoader) Load(ctx context.Context, principalID uint) (*protocol.APIRoleSet, error) {
	return loadAPIRoleSet(ctx, l.Client, consts.RPCSyncFetchUserAPIRoles, principalID)
}

type TokenAPIRoleSetLoader struct {
	Client app.WSClient
}

func (l *TokenAPIRoleSetLoader) Load(ctx context.Context, principalID uint) (*protocol.APIRoleSet, error) {
	return loadAPIRoleSet(ctx, l.Client, consts.RPCSyncFetchTokenAPIRoles, principalID)
}

func loadAPIRoleSet(
	ctx context.Context,
	client app.WSClient,
	method string,
	principalID uint,
) (*protocol.APIRoleSet, error) {
	if client == nil {
		return nil, fmt.Errorf("%s: client unavailable", method)
	}
	raw, err := client.Call(ctx, method, protocol.APIRoleSetFetchRequest{PrincipalID: principalID})
	if err != nil {
		return nil, err
	}
	var result protocol.APIRoleSetFetchResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.PrincipalID != principalID {
		return nil, fmt.Errorf("API role set principal mismatch: got %d want %d", result.PrincipalID, principalID)
	}
	if !result.Exists {
		return nil, entitycache.ErrNotFound
	}
	result.RoleSet.RoleIDs = append([]uint{}, result.RoleSet.RoleIDs...)
	return &result.RoleSet, nil
}
