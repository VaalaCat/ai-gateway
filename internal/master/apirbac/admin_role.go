package apirbac

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

const GatewayAdminRoleKey = models.GatewayAdminRoleKey

type GatewayAdminRoleAppender struct {
	roles dao.APIRBACQuery
}

func NewGatewayAdminRoleAppender(roles dao.APIRBACQuery) *GatewayAdminRoleAppender {
	return &GatewayAdminRoleAppender{roles: roles}
}

func (a *GatewayAdminRoleAppender) AppendForUser(
	ctx context.Context,
	user models.User,
	roleIDs []uint,
) ([]uint, error) {
	if user.Role != consts.RoleAdmin {
		return roleIDs, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roles, err := a.roles.ListRolesByKey(GatewayAdminRoleKey)
	if err != nil {
		return nil, fmt.Errorf("find gateway administrator API role: %w", err)
	}
	if len(roles) != 1 {
		return nil, fmt.Errorf("gateway administrator API role count is %d, want 1", len(roles))
	}
	role := roles[0]
	if !role.BuiltIn || role.Status != consts.StatusEnabled {
		return nil, fmt.Errorf("gateway administrator API role is not an enabled built-in role")
	}
	return append(roleIDs, role.ID), nil
}
