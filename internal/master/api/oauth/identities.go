package oauth

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type IdentityItem struct {
	ID                  uint   `json:"id"`
	ProviderID          uint   `json:"provider_id"`
	ProviderName        string `json:"provider_name"`
	ProviderDisplayName string `json:"provider_display_name"`
	Subject             string `json:"subject"`
	Email               string `json:"email"`
	DisplayName         string `json:"display_name"`
	CreatedAt           int64  `json:"created_at"`
}

func (h *Handler) ListMyIdentities(c *app.Context, _ api.EmptyRequest) ([]IdentityItem, error) {
	if c.UserInfo == nil {
		return nil, api.UnauthorizedError("missing auth")
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	idents, err := q.OAuthIdentity().ListByUserID(c.UserInfo.UserID)
	if err != nil {
		return nil, api.InternalError("list failed", err)
	}
	if len(idents) == 0 {
		return []IdentityItem{}, nil
	}
	all, err := q.OAuthProvider().List()
	if err != nil {
		return nil, api.InternalError("list providers failed", err)
	}
	pmap := map[uint]models.OAuthProvider{}
	for _, p := range all {
		pmap[p.ID] = p
	}
	out := make([]IdentityItem, 0, len(idents))
	for _, i := range idents {
		p := pmap[i.ProviderID]
		out = append(out, IdentityItem{
			ID:                  i.ID,
			ProviderID:          i.ProviderID,
			ProviderName:        p.Name,
			ProviderDisplayName: p.DisplayName,
			Subject:             i.Subject,
			Email:               i.Email,
			DisplayName:         i.DisplayName,
			CreatedAt:           i.CreatedAt,
		})
	}
	return out, nil
}

type DeleteIdentityRequest = api.IDPathRequest

func (h *Handler) DeleteIdentity(c *app.Context, req DeleteIdentityRequest) (api.StatusResponse, error) {
	if c.UserInfo == nil {
		return api.StatusResponse{}, api.UnauthorizedError("missing auth")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError("not found")
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	user, err := q.User().GetByID(c.UserInfo.UserID)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("user lookup", err)
	}
	cnt, err := q.OAuthIdentity().CountByUserID(c.UserInfo.UserID)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("count identities", err)
	}
	// 解绑后用户必须仍有可用登录方式
	if cnt <= 1 && !user.PasswordSet {
		return api.StatusResponse{}, api.ConflictError("last_login_method", nil)
	}
	m := dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext()))
	affected, err := m.OAuthIdentity().DeleteByIDForUser(uint(id), c.UserInfo.UserID)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete failed", err)
	}
	if affected == 0 {
		return api.StatusResponse{}, api.NotFoundError("not found")
	}
	return api.StatusResponse{Status: "ok"}, nil
}
