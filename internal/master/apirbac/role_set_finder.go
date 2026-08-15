package apirbac

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/gorm"
)

type UserFinder interface {
	GetByID(id uint) (*models.User, error)
}

type TokenFinder interface {
	GetByID(id uint) (*models.Token, error)
}

type RoleSetResult struct {
	Exists  bool
	RoleIDs []uint
}

func (r RoleSetResult) APIRoleSet() protocol.APIRoleSet {
	return protocol.APIRoleSet{RoleIDs: r.RoleIDs}
}

type RoleSetFinder struct {
	users  UserFinder
	tokens TokenFinder
	roles  dao.APIRBACQuery
	admins *GatewayAdminRoleAppender
}

func NewRoleSetFinder(users UserFinder, tokens TokenFinder, roles dao.APIRBACQuery) *RoleSetFinder {
	return &RoleSetFinder{
		users: users, tokens: tokens, roles: roles,
		admins: NewGatewayAdminRoleAppender(roles),
	}
}

func (f *RoleSetFinder) FindUser(ctx context.Context, userID uint) (RoleSetResult, error) {
	if err := ctx.Err(); err != nil {
		return RoleSetResult{}, err
	}
	user, err := f.users.GetByID(userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RoleSetResult{}, nil
	}
	if err != nil {
		return RoleSetResult{}, fmt.Errorf("find API role user %d: %w", userID, err)
	}
	roleIDs, err := f.findPrincipalRoleIDs(models.APIPrincipalUser, user.ID)
	if err != nil {
		return RoleSetResult{}, err
	}
	groupID := user.GroupID
	if groupID == 0 {
		groupID = models.DefaultUserGroupID
	}
	groupRoleIDs, err := f.findPrincipalRoleIDs(models.APIPrincipalUserGroup, groupID)
	if err != nil {
		return RoleSetResult{}, err
	}
	roleIDs = append(roleIDs, groupRoleIDs...)
	roleIDs, err = f.admins.AppendForUser(ctx, *user, roleIDs)
	if err != nil {
		return RoleSetResult{}, err
	}
	return RoleSetResult{Exists: true, RoleIDs: sortedUniqueIDs(roleIDs)}, nil
}

func (f *RoleSetFinder) FindToken(ctx context.Context, tokenID uint) (RoleSetResult, error) {
	if err := ctx.Err(); err != nil {
		return RoleSetResult{}, err
	}
	token, err := f.tokens.GetByID(tokenID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RoleSetResult{}, nil
	}
	if err != nil {
		return RoleSetResult{}, fmt.Errorf("find API role token %d: %w", tokenID, err)
	}
	switch token.APIRoleMode {
	case models.APIRoleModeExplicit:
		roleIDs, err := f.findPrincipalRoleIDs(models.APIPrincipalToken, token.ID)
		if err != nil {
			return RoleSetResult{}, err
		}
		return RoleSetResult{Exists: true, RoleIDs: sortedUniqueIDs(roleIDs)}, nil
	case models.APIRoleModeInherit:
		return f.findInheritedToken(ctx, *token)
	default:
		return RoleSetResult{}, fmt.Errorf("API role token %d has invalid mode %q", token.ID, token.APIRoleMode)
	}
}

func (f *RoleSetFinder) FindUserGroup(ctx context.Context, groupID uint) (RoleSetResult, error) {
	if err := ctx.Err(); err != nil {
		return RoleSetResult{}, err
	}
	roleIDs, err := f.findPrincipalRoleIDs(models.APIPrincipalUserGroup, groupID)
	if err != nil {
		return RoleSetResult{}, err
	}
	return RoleSetResult{Exists: true, RoleIDs: sortedUniqueIDs(roleIDs)}, nil
}

func (f *RoleSetFinder) findInheritedToken(ctx context.Context, token models.Token) (RoleSetResult, error) {
	owner, err := f.FindUser(ctx, token.UserID)
	if err != nil {
		return RoleSetResult{}, err
	}
	if !owner.Exists {
		return RoleSetResult{}, fmt.Errorf("API role token %d owner %d does not exist", token.ID, token.UserID)
	}
	return owner, nil
}

func (f *RoleSetFinder) findPrincipalRoleIDs(principalType models.APIPrincipalType, principalID uint) ([]uint, error) {
	bindings, err := f.roles.ListRoleSetBindingsByPrincipal(principalType, principalID)
	if err != nil {
		return nil, fmt.Errorf("list %s %d API role bindings: %w", principalType, principalID, err)
	}
	roleIDs := make([]uint, 0, len(bindings))
	for _, binding := range bindings {
		roleIDs = append(roleIDs, binding.RoleID)
	}
	return roleIDs, nil
}

func sortedUniqueIDs(ids []uint) []uint {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := ids[:0]
	for _, id := range ids {
		if len(result) == 0 || result[len(result)-1] != id {
			result = append(result, id)
		}
	}
	return result
}
