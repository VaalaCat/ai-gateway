package user

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.User, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	oldUser, err := q.User().GetByID(uint(id))
	if err != nil {
		return models.User{}, api.NotFoundError(consts.ErrNotFound)
	}
	oldGroupID := oldUser.GroupID

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")

	// Validate status if present
	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return models.User{}, api.BadRequestError(err.Error(), err)
		}
	}

	// Validate group_id if present (must be a positive uint pointing to existing group)
	var newGroupID *uint
	if raw, ok := updates["group_id"]; ok {
		fnum, ok := raw.(float64)
		if !ok {
			return models.User{}, api.BadRequestError("group_id must be number", nil)
		}
		if fnum < 0 || fnum != float64(uint(fnum)) {
			return models.User{}, api.BadRequestError("group_id must be non-negative integer", nil)
		}
		gid := uint(fnum)
		if gid == 0 {
			gid = 1
		}
		if _, err := q.UserGroup().GetByID(gid); err != nil {
			return models.User{}, api.BadRequestError("user_group not found", err)
		}
		updates["group_id"] = gid
		newGroupID = &gid
	}

	if pw, ok := updates["password"]; ok && pw != nil {
		pwStr, ok := pw.(string)
		if !ok {
			return models.User{}, api.BadRequestError("password must be string", nil)
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(pwStr), bcrypt.DefaultCost)
		if err != nil {
			return models.User{}, api.InternalError("update user failed", err)
		}
		updates["password"] = string(hashed)
	}

	if err := m.User().Update(uint(id), updates); err != nil {
		return models.User{}, api.InternalError("update user failed", err)
	}

	user, err := q.User().GetByID(uint(id))
	if err != nil {
		return models.User{}, api.InternalError("update user failed", err)
	}
	user.Password = ""

	if newGroupID != nil && h.Bus != nil && oldGroupID != *newGroupID {
		_ = events.Publish(context.Background(), h.Bus, events.UserSyncUpdateTopic,
			protocol.SyncedUser{ID: uint(id), GroupID: *newGroupID})
	}

	return *user, nil
}
