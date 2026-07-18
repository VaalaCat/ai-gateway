package agent

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) GenerateEnrollmentToken(c *app.Context, req GenerateEnrollmentTokenRequest) (GenerateEnrollmentTokenResponse, error) {
	if req.TTL <= 0 {
		req.TTL = 3600
	}

	token := GenerateRandomID("enroll-")
	et := models.EnrollmentToken{
		Token:     token,
		ExpiresAt: time.Now().Unix() + req.TTL,
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)

	if err := m.EnrollmentToken().Create(&et); err != nil {
		return GenerateEnrollmentTokenResponse{}, api.InternalError(err.Error(), err)
	}
	return GenerateEnrollmentTokenResponse{
		EnrollmentToken: token,
		ExpiresAt:       et.ExpiresAt,
	}, nil
}
