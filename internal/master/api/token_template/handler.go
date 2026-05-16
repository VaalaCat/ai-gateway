package token_template

import (
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"gorm.io/datatypes"
)

// List returns all token templates (admin, all statuses).
func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.TokenTemplate], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	templates, total, err := q.TokenTemplate().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.TokenTemplateListFilter{Search: req.Search, Status: statusFilter},
	)
	if err != nil {
		return api.PaginatedResponse[models.TokenTemplate]{}, api.InternalError("list token templates failed", err)
	}
	return api.PaginatedResponse[models.TokenTemplate]{Data: templates, Total: total, Page: page, PageSize: pageSize}, nil
}

// ListEnabled returns only enabled token templates (user-facing).
func (h *Handler) ListEnabled(c *app.Context, req ListRequest) (api.PaginatedResponse[models.TokenTemplate], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	status := consts.StatusEnabled
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	templates, total, err := q.TokenTemplate().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.TokenTemplateListFilter{Search: req.Search, Status: &status},
	)
	if err != nil {
		return api.PaginatedResponse[models.TokenTemplate]{}, api.InternalError("list token templates failed", err)
	}
	return api.PaginatedResponse[models.TokenTemplate]{Data: templates, Total: total, Page: page, PageSize: pageSize}, nil
}

// Create creates a new token template.
func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.TokenTemplate], error) {
	if req.Models != "" {
		var patterns []string
		if err := json.Unmarshal([]byte(req.Models), &patterns); err != nil {
			return api.Created[models.TokenTemplate]{}, api.BadRequestError("invalid models JSON: "+err.Error(), err)
		}
		if err := utils.ValidateModelPatterns(patterns); err != nil {
			return api.Created[models.TokenTemplate]{}, api.BadRequestError("invalid model pattern: "+err.Error(), err)
		}
	}

	if req.AllowedChannelIDs != nil {
		if err := validateAllowedChannelIDs(*req.AllowedChannelIDs); err != nil {
			return api.Created[models.TokenTemplate]{}, api.BadRequestError(err.Error(), err)
		}
	}

	tpl := models.TokenTemplate{
		Name:       req.Name,
		Models:     req.Models,
		ExpiryDays: req.ExpiryDays,
		Status:     req.Status,
	}
	if req.AllowedChannelIDs != nil {
		tpl.AllowedChannelIDs = datatypes.JSONSlice[uint](*req.AllowedChannelIDs)
	}
	if tpl.ExpiryDays == 0 {
		tpl.ExpiryDays = -1
	}
	if tpl.Status == 0 {
		tpl.Status = consts.StatusEnabled
	}

	daoCtx := dao.NewContext(c.App)
	m := dao.NewAdminMutation(daoCtx)

	if err := m.TokenTemplate().Create(&tpl); err != nil {
		return api.Created[models.TokenTemplate]{}, api.InternalError("create token template failed", err)
	}
	return api.Created[models.TokenTemplate]{Value: tpl}, nil
}

// Update updates a token template by ID.
func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.TokenTemplate, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.TokenTemplate().GetByID(uint(id)); err != nil {
		return models.TokenTemplate{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")

	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return models.TokenTemplate{}, api.BadRequestError(err.Error(), err)
		}
	}

	// Validate models if present in updates
	if modelsRaw, ok := updates["models"]; ok {
		if modelsStr, isStr := modelsRaw.(string); isStr && modelsStr != "" {
			var patterns []string
			if err := json.Unmarshal([]byte(modelsStr), &patterns); err != nil {
				return models.TokenTemplate{}, api.BadRequestError("invalid models JSON: "+err.Error(), err)
			}
			if err := utils.ValidateModelPatterns(patterns); err != nil {
				return models.TokenTemplate{}, api.BadRequestError("invalid model pattern: "+err.Error(), err)
			}
		}
	}

	if raw, ok := updates["allowed_channel_ids"]; ok {
		ids, err := normalizeAllowedChannelIDs(raw)
		if err != nil {
			return models.TokenTemplate{}, api.BadRequestError(err.Error(), err)
		}
		if err := validateAllowedChannelIDs(ids); err != nil {
			return models.TokenTemplate{}, api.BadRequestError(err.Error(), err)
		}
		updates["allowed_channel_ids"] = datatypes.JSONSlice[uint](ids)
	}

	if err := m.TokenTemplate().Update(uint(id), updates); err != nil {
		return models.TokenTemplate{}, api.InternalError("update token template failed", err)
	}

	tpl, err := q.TokenTemplate().GetByID(uint(id))
	if err != nil {
		return models.TokenTemplate{}, api.InternalError("update token template failed", err)
	}
	return *tpl, nil
}

// Delete deletes a token template by ID.
func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.TokenTemplate().GetByID(uint(id)); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	if err := m.TokenTemplate().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete token template failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}

func validateAllowedChannelIDs(ids []uint) error {
	return api.ValidateAllowedChannelIDs(ids)
}

func normalizeAllowedChannelIDs(v any) ([]uint, error) {
	return api.NormalizeAllowedChannelIDs(v)
}
