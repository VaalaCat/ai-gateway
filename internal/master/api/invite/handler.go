package invite

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// requireInviteEnabled 是所有 invite 端点的总开关 gate。
func requireInviteEnabled(c *app.Context) error {
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	if !q.Setting().LookupBool(consts.SettingKeyInviteEnabled, false) {
		return api.ForbiddenError("invite feature disabled")
	}
	return nil
}

// generateInviteCode 生成一个未占用的随机码(16 位大写 hex)。
func generateInviteCode(q dao.AdminQuery) (string, error) {
	for i := 0; i < 5; i++ {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		code := strings.ToUpper(hex.EncodeToString(b))
		_, err := q.InviteCode().GetByCode(code)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return code, nil
		}
		if err != nil {
			return "", err
		}
		// err == nil:code 已存在,继续下一次尝试
	}
	return "", errors.New("failed to generate unique invite code")
}

// checkUserCreateQuota 对非管理员校验"名下有效码数量"与"单码次数"上限。
func checkUserCreateQuota(q dao.AdminQuery, uid uint, maxUses int) error {
	maxCodes := q.Setting().LookupInt(consts.SettingKeyInviteUserMaxCodes, 5)
	if maxCodes == 0 {
		return api.ForbiddenError("invite code creation disabled for users")
	}
	active, err := q.InviteCode().CountActiveByCreator(uid, time.Now().Unix())
	if err != nil {
		return api.InternalError("count active invite codes failed", err)
	}
	if active >= int64(maxCodes) {
		return api.BadRequestError("active invite code limit reached", nil)
	}
	if maxUses > q.Setting().LookupInt(consts.SettingKeyInviteUserMaxUses, 1) {
		return api.BadRequestError("max_uses exceeds allowed limit", nil)
	}
	return nil
}

// Create 建一个邀请码,归属当前用户。非管理员受双重限额约束。
func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.InviteCode], error) {
	if err := requireInviteEnabled(c); err != nil {
		return api.Created[models.InviteCode]{}, err
	}
	if c.UserInfo == nil {
		return api.Created[models.InviteCode]{}, api.UnauthorizedError("not authenticated")
	}
	uid := c.UserInfo.UserID
	scope := middleware.GetScope(c.Context)
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	maxUses := req.MaxUses
	if maxUses < 1 {
		maxUses = 1
	}
	if scope == nil || !scope.IsAdmin {
		if err := checkUserCreateQuota(q, uid, maxUses); err != nil {
			return api.Created[models.InviteCode]{}, err
		}
	}
	code, err := generateInviteCode(q)
	if err != nil {
		return api.Created[models.InviteCode]{}, api.InternalError("generate invite code failed", err)
	}
	ic := models.InviteCode{Code: code, CreatorID: uid, MaxUses: maxUses, ExpiresAt: req.ExpiresAt, Note: req.Note}
	if err := m.InviteCode().Create(&ic); err != nil {
		return api.Created[models.InviteCode]{}, api.InternalError("create invite code failed", err)
	}
	return api.Created[models.InviteCode]{Value: ic}, nil
}
