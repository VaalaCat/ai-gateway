package user

import (
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) Login(c *app.Context, req LoginRequest) (LoginResponse, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	identifier := strings.TrimSpace(req.Username)
	user, err := q.User().GetByUsername(identifier)
	if err != nil {
		user, err = q.User().GetByEmail(strings.ToLower(identifier))
		if err != nil {
			return LoginResponse{}, api.UnauthorizedError("invalid credentials")
		}
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return LoginResponse{}, api.UnauthorizedError("invalid credentials")
	}
	if c.Settings == nil || c.Settings.Master.JWTSecret == "" {
		return LoginResponse{}, api.InternalError("token generation failed", nil)
	}
	token, err := middleware.GenerateToken(c.Settings.Master.JWTSecret, user.ID, user.Role, user.Username, user.DisplayName, user.AvatarURL)
	if err != nil {
		return LoginResponse{}, api.InternalError("token generation failed", err)
	}
	return LoginResponse{Token: token}, nil
}
