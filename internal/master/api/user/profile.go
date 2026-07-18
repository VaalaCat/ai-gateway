package user

import (
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

type ProfileResponse struct {
	models.User
	GroupName string `json:"group_name"`
}

func (h *Handler) GetProfile(c *app.Context, _ api.EmptyRequest) (ProfileResponse, error) {
	if c.UserInfo == nil || c.UserInfo.UserID == 0 {
		return ProfileResponse{}, api.UnauthorizedError(consts.ErrInvalidToken)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	user, err := q.User().GetByID(c.UserInfo.UserID)
	if err != nil {
		return ProfileResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	user.Password = ""

	resp := ProfileResponse{User: *user}
	if user.GroupID != 0 {
		if g, err := q.UserGroup().GetByID(user.GroupID); err == nil {
			resp.GroupName = g.Name
		}
	}
	return resp, nil
}

func (h *Handler) ChangePassword(c *app.Context, req ChangePasswordRequest) (api.StatusResponse, error) {
	if c.UserInfo == nil || c.UserInfo.UserID == 0 {
		return api.StatusResponse{}, api.UnauthorizedError(consts.ErrInvalidToken)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	user, err := q.User().GetByID(c.UserInfo.UserID)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)); err != nil {
		return api.StatusResponse{}, api.UnauthorizedError("wrong password")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("change password failed", err)
	}
	if err := m.User().Update(c.UserInfo.UserID, map[string]any{"password": string(hashed), "password_set": true}); err != nil {
		return api.StatusResponse{}, api.InternalError("change password failed", err)
	}

	return api.StatusResponse{Status: "ok"}, nil
}

type UpdateProfileRequest struct {
	Email       *string `json:"email"        binding:"omitempty,max=191"`
	DisplayName *string `json:"display_name" binding:"omitempty,max=64"`
	AvatarURL   *string `json:"avatar_url"   binding:"omitempty,max=512"`
}

func (h *Handler) UpdateProfile(c *app.Context, req UpdateProfileRequest) (ProfileResponse, error) {
	if c.UserInfo == nil || c.UserInfo.UserID == 0 {
		return ProfileResponse{}, api.UnauthorizedError(consts.ErrInvalidToken)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	updates, err := h.buildProfileUpdates(req, q, c.UserInfo.UserID)
	if err != nil {
		return ProfileResponse{}, err
	}

	userCtx := dao.NewUserContextWithContext(c.App, c.RequestContext(), c.UserInfo)
	m := dao.NewMutation(userCtx)
	if err := m.User().UpdateProfile(updates); err != nil {
		return ProfileResponse{}, api.InternalError("update profile failed", err)
	}

	return h.GetProfile(c, api.EmptyRequest{})
}

func (h *Handler) buildProfileUpdates(req UpdateProfileRequest, q dao.AdminQuery, selfID uint) (map[string]any, error) {
	updates := map[string]any{}
	if req.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		if email != "" {
			if existing, err := q.User().GetByEmail(email); err == nil && existing.ID != selfID {
				return nil, api.ConflictError("email_taken", nil)
			}
		}
		updates["email"] = email
	}
	if req.DisplayName != nil {
		updates["display_name"] = strings.TrimSpace(*req.DisplayName)
	}
	if req.AvatarURL != nil {
		avatarURL := strings.TrimSpace(*req.AvatarURL)
		if avatarURL != "" {
			if !hasHTTPScheme(avatarURL) {
				return nil, api.BadRequestError("avatar_url must be a valid URL", nil)
			}
		}
		updates["avatar_url"] = avatarURL
	}
	return updates, nil
}

func hasHTTPScheme(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
