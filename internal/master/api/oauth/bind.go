package oauth

import (
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

type BindRequest struct {
	Ticket   string `json:"ticket" binding:"required"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type BindResponse struct {
	Token string `json:"token"`
}

func (h *Handler) Bind(c *app.Context, req BindRequest) (BindResponse, error) {
	claims, err := ParseBindTicket(h.JWTSecret, req.Ticket)
	if err != nil {
		return BindResponse{}, api.UnauthorizedError("ticket_invalid")
	}
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	u, err := q.User().GetByUsername(req.Username)
	if err != nil {
		return BindResponse{}, api.UnauthorizedError("invalid_credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		return BindResponse{}, api.UnauthorizedError("invalid_credentials")
	}
	if u.Status != consts.StatusEnabled {
		return BindResponse{}, api.ForbiddenError("user_disabled")
	}
	if _, found, err := q.OAuthIdentity().GetByProviderSubject(claims.ProviderID, claims.Subject); err != nil {
		return BindResponse{}, api.InternalError("lookup identity failed", err)
	} else if found {
		return BindResponse{}, api.ConflictError("already_bound", nil)
	}
	m := dao.NewAdminMutation(dao.NewContext(c.App))
	if err := m.OAuthIdentity().Create(&models.OAuthIdentity{
		UserID:      u.ID,
		ProviderID:  claims.ProviderID,
		Subject:     claims.Subject,
		Email:       claims.Email,
		DisplayName: claims.DisplayName,
	}); err != nil {
		return BindResponse{}, api.InternalError("create identity failed", err)
	}
	tok, err := middleware.GenerateToken(h.JWTSecret, u.ID, u.Role, u.Username, u.DisplayName, u.AvatarURL)
	if err != nil {
		return BindResponse{}, api.InternalError("token failed", err)
	}
	return BindResponse{Token: tok}, nil
}
