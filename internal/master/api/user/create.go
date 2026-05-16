package user

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.User], error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return api.Created[models.User]{}, api.InternalError("create user failed", err)
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	groupID := uint(1)
	if req.GroupID != nil && *req.GroupID > 0 {
		if _, err := q.UserGroup().GetByID(*req.GroupID); err != nil {
			return api.Created[models.User]{}, api.BadRequestError("user_group not found", err)
		}
		groupID = *req.GroupID
	}

	user := models.User{
		Username:    req.Username,
		Password:    string(hashed),
		PasswordSet: true,
		Role:        req.Role,
		Status:      1,
		GroupID:     groupID,
	}
	if user.Role == 0 {
		user.Role = 1
	}

	if err := m.User().Create(&user); err != nil {
		return api.Created[models.User]{}, api.ConflictError(err.Error(), err)
	}
	user.Password = ""
	return api.Created[models.User]{Value: user}, nil
}
