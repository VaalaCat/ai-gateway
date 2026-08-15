package api_role

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

var errBuiltinRoleProtected = errors.New("protected API role cannot be modified or deleted")

func isProtectedOrdinaryRole(role models.Role) bool {
	return role.Kind != models.APIRoleKindCustom || role.BuiltIn || role.Key == models.GatewayAdminRoleKey
}

// Handler owns administrative generic API role and binding reads.
type Handler struct {
	App       app.Application
	Publisher RolePublisher
}

type RolePublisher interface {
	PublishRole(context.Context, dao.AdminQuery, string, uint) error
	PublishRoleBindingChange(context.Context, dao.AdminQuery, models.APIPrincipalType, uint) error
}
