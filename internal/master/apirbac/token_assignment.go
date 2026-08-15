package apirbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

var (
	ErrRoleNotAssignable  = errors.New("API role is not assignable to a token")
	ErrRoleOutsideUserSet = errors.New("API role is outside the token owner's effective role set")
)

type TokenRoleAssignmentValidator struct {
	roles   dao.APIRBACQuery
	roleSet *RoleSetFinder
}

func NewTokenRoleAssignmentValidator(
	users UserFinder,
	tokens TokenFinder,
	roles dao.APIRBACQuery,
) *TokenRoleAssignmentValidator {
	return &TokenRoleAssignmentValidator{roles: roles, roleSet: NewRoleSetFinder(users, tokens, roles)}
}

func (v *TokenRoleAssignmentValidator) Validate(
	ctx context.Context,
	ownerUserID uint,
	roleIDs []uint,
	administrator bool,
) error {
	for _, roleID := range roleIDs {
		role, err := v.roles.GetRoleByID(roleID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: role %d does not exist", ErrRoleNotAssignable, roleID)
		}
		if err != nil {
			return fmt.Errorf("load assignable API role %d: %w", roleID, err)
		}
		if role.Kind != models.APIRoleKindCustom || role.Key == GatewayAdminRoleKey || role.Status != consts.StatusEnabled {
			return fmt.Errorf("%w: role %d", ErrRoleNotAssignable, roleID)
		}
	}
	if administrator {
		return nil
	}
	ownerRoles, err := v.roleSet.FindUser(ctx, ownerUserID)
	if err != nil {
		return err
	}
	if !ownerRoles.Exists {
		return fmt.Errorf("token owner %d does not exist", ownerUserID)
	}
	effective := make(map[uint]struct{}, len(ownerRoles.RoleIDs))
	for _, roleID := range ownerRoles.RoleIDs {
		effective[roleID] = struct{}{}
	}
	for _, roleID := range roleIDs {
		if _, ok := effective[roleID]; !ok {
			return fmt.Errorf("%w: role %d", ErrRoleOutsideUserSet, roleID)
		}
	}
	return nil
}
