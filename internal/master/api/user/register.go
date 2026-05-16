package user

import (
	"regexp"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32"`
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=64"`
}

func (h *Handler) Register(c *app.Context, req RegisterRequest) (api.Created[models.User], error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	setting, found, err := q.Setting().Lookup("registration_enabled")
	if err != nil || !found || setting.Value != "true" {
		return api.Created[models.User]{}, api.ForbiddenError("registration is disabled")
	}

	if !usernameRegex.MatchString(req.Username) {
		return api.Created[models.User]{}, api.BadRequestError("username must contain only letters, numbers, and underscores", nil)
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if _, err := q.User().GetByEmail(email); err == nil {
		return api.Created[models.User]{}, api.ConflictError("email_taken", nil)
	}

	_, err = q.User().GetByUsername(req.Username)
	if err == nil {
		return api.Created[models.User]{}, api.ConflictError("username already exists", nil)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return api.Created[models.User]{}, api.InternalError("hash password failed", err)
	}

	user := models.User{
		Username:    req.Username,
		Email:       email,
		Password:    string(hashed),
		PasswordSet: true,
		Role:        consts.RoleUser,
		Status:      consts.StatusEnabled,
	}
	if err := m.User().Create(&user); err != nil {
		return api.Created[models.User]{}, api.InternalError("create user failed", err)
	}

	user.Password = ""
	return api.Created[models.User]{Value: user}, nil
}
