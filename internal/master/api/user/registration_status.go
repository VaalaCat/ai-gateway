package user

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type RegistrationStatusResponse struct {
	RegistrationEnabled bool `json:"registration_enabled"`
}

func (h *Handler) RegistrationStatus(c *app.Context, _ api.EmptyRequest) (RegistrationStatusResponse, error) {
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	setting, found, err := q.Setting().Lookup("registration_enabled")
	enabled := err == nil && found && setting.Value == "true"
	return RegistrationStatusResponse{RegistrationEnabled: enabled}, nil
}
