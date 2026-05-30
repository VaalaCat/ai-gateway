package user

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"golang.org/x/crypto/bcrypt"
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

type RegisterRequest struct {
	Username   string `json:"username"    binding:"required,min=3,max=32"`
	Email      string `json:"email"       binding:"required,email"`
	Password   string `json:"password"    binding:"required,min=8,max=64"`
	InviteCode string `json:"invite_code"`
}

func (h *Handler) Register(c *app.Context, req RegisterRequest) (api.Created[models.User], error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	setting, found, err := q.Setting().Lookup("registration_enabled")
	if err != nil || !found || setting.Value != "true" {
		return api.Created[models.User]{}, api.ForbiddenError("registration is disabled")
	}

	inviteEnabled := q.Setting().LookupBool(consts.SettingKeyInviteEnabled, false)
	if inviteEnabled && req.InviteCode == "" {
		return api.Created[models.User]{}, api.BadRequestError("invite code required", nil)
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

	now := time.Now().Unix()
	err = dao.RunInTx[dao.Context](daoCtx, func(txCtx dao.Context) error {
		txm := dao.NewAdminMutation(txCtx)
		if !inviteEnabled {
			return txm.User().Create(&user)
		}
		ic, err := txm.InviteCode().Redeem(req.InviteCode, now)
		if err != nil {
			return err
		}
		if err := txm.User().Create(&user); err != nil {
			return err
		}
		return txm.InviteCode().RecordRedemption(&models.InviteRedemption{
			InviteCodeID: ic.ID,
			Code:         ic.Code,
			InviterID:    ic.CreatorID,
			InviteeID:    user.ID,
		})
	})
	if err != nil {
		if errors.Is(err, dao.ErrInviteCodeUnavailable) {
			return api.Created[models.User]{}, api.BadRequestError("invalid or unavailable invite code", nil)
		}
		return api.Created[models.User]{}, api.InternalError("create user failed", err)
	}

	user.Password = ""
	return api.Created[models.User]{Value: user}, nil
}
