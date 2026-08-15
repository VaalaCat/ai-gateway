package api_access_grant

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	apirole "github.com/VaalaCat/ai-gateway/internal/master/api/api_role"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/gorm"
)

type Handler struct {
	App       app.Application
	Publisher apirole.RolePublisher
	Writer    grantWriteOperations
}

type grantWriteOperations interface {
	Replace(dao.Context, PrincipalRef, uint, GrantScope, []uint) (ConfiguredGrant, error)
	Delete(dao.Context, PrincipalRef, uint) error
}

func (h *Handler) writer() grantWriteOperations {
	if h.Writer != nil {
		return h.Writer
	}
	return GrantWriter{}
}

type ReplaceRequest struct {
	PrincipalType models.APIPrincipalType `uri:"principal_type" binding:"required"`
	PrincipalID   uint                    `uri:"principal_id" binding:"required"`
	APIServiceID  uint                    `uri:"service_id" binding:"required"`
	Scope         GrantScope              `json:"scope" binding:"required"`
	RouteIDs      []uint                  `json:"route_ids"`
}

type DeleteRequest struct {
	PrincipalType models.APIPrincipalType `uri:"principal_type" binding:"required"`
	PrincipalID   uint                    `uri:"principal_id" binding:"required"`
	APIServiceID  uint                    `uri:"service_id" binding:"required"`
}

func (h *Handler) Replace(c *app.Context, req ReplaceRequest) (ConfiguredGrant, error) {
	principal := PrincipalRef{Type: req.PrincipalType, ID: req.PrincipalID}
	grant, err := h.writer().Replace(dao.NewContextWithContext(h.App, c.RequestContext()), principal, req.APIServiceID, req.Scope, req.RouteIDs)
	if err != nil {
		return ConfiguredGrant{}, api.BadRequestError("replace API access grant failed", err)
	}
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	role, err := managedRole(dao.NewContextWithContext(h.App, publishCtx), principal)
	if err != nil {
		return ConfiguredGrant{}, api.InternalError("find managed API access role failed", err)
	}
	if err := h.publish(c, publishCtx, principal, events.ActionUpdate, role.ID); err != nil {
		return ConfiguredGrant{}, err
	}
	return grant, nil
}

func (h *Handler) Delete(c *app.Context, req DeleteRequest) (api.StatusResponse, error) {
	principal := PrincipalRef{Type: req.PrincipalType, ID: req.PrincipalID}
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	before, err := managedRole(ctx, principal)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return api.StatusResponse{}, api.BadRequestError("find API access grant failed", err)
	}
	if err := h.writer().Delete(ctx, principal, req.APIServiceID); err != nil {
		return api.StatusResponse{}, api.BadRequestError("delete API access grant failed", err)
	}
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	action := events.ActionUpdate
	if _, err := managedRole(dao.NewContextWithContext(h.App, publishCtx), principal); errors.Is(err, gorm.ErrRecordNotFound) {
		action = events.ActionDelete
	} else if err != nil {
		return api.StatusResponse{}, api.InternalError("find managed API access role failed", err)
	}
	if before.ID != 0 {
		if err := h.publish(c, publishCtx, principal, action, before.ID); err != nil {
			return api.StatusResponse{}, err
		}
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func (h *Handler) publish(c *app.Context, publishCtx context.Context, principal PrincipalRef, action string, roleID uint) error {
	if h.Publisher == nil {
		return nil
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, publishCtx))
	if err := h.Publisher.PublishRole(publishCtx, query, action, roleID); err != nil {
		return api.InternalError("publish managed API role failed", err)
	}
	if err := h.Publisher.PublishRoleBindingChange(publishCtx, query, principal.Type, principal.ID); err != nil {
		return api.InternalError("publish API access grant failed", err)
	}
	return nil
}

func managedRole(ctx dao.Context, principal PrincipalRef) (models.Role, error) {
	var role models.Role
	err := ctx.GetCoreDB().Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).First(&role).Error
	return role, err
}
