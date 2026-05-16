package oauth

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type RegisterRequest struct {
	Ticket string `json:"ticket" binding:"required"`
}

type RegisterResponse struct {
	Token string `json:"token"`
}

func (h *Handler) Register(c *app.Context, req RegisterRequest) (RegisterResponse, error) {
	claims, err := ParseBindTicket(h.JWTSecret, req.Ticket)
	if err != nil {
		return RegisterResponse{}, api.UnauthorizedError("ticket_invalid")
	}
	if !h.readAutoCreateSetting() {
		return RegisterResponse{}, api.ForbiddenError("auto_create_disabled")
	}
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	if _, found, err := q.OAuthIdentity().GetByProviderSubject(claims.ProviderID, claims.Subject); err != nil {
		return RegisterResponse{}, api.InternalError("lookup identity failed", err)
	} else if found {
		return RegisterResponse{}, api.ConflictError("already_bound", nil)
	}
	userID, err := h.createUserFromClaims(claims)
	if err != nil {
		return RegisterResponse{}, api.InternalError("create user failed", err)
	}
	u, err := q.User().GetByID(userID)
	if err != nil {
		return RegisterResponse{}, api.InternalError("lookup user failed", err)
	}
	tok, err := middleware.GenerateToken(h.JWTSecret, u.ID, u.Role, u.Username, u.DisplayName, u.AvatarURL)
	if err != nil {
		return RegisterResponse{}, api.InternalError("token failed", err)
	}
	return RegisterResponse{Token: tok}, nil
}
