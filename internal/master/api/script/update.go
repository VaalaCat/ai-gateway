package script

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/dop251/goja"
	"gorm.io/datatypes"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.AdminScript, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.AdminScript().GetByID(uint(id))
	if err != nil {
		return models.AdminScript{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")

	// code 变更需先校验编译
	if code, ok := updates["code"].(string); ok {
		name := existing.Name
		if n, ok := updates["name"].(string); ok {
			name = n
		}
		if _, err := goja.Compile(name, code, true); err != nil {
			return models.AdminScript{}, api.BadRequestError("script compile error: "+err.Error(), err)
		}
	}

	// scope 是 JSONType 列，需把 JSON map 重新封装成 datatypes.JSONType
	if raw, ok := updates["scope"]; ok {
		b, err := json.Marshal(raw)
		if err != nil {
			return models.AdminScript{}, api.InternalError("marshal scope failed", err)
		}
		var sc models.ScriptScope
		if err := json.Unmarshal(b, &sc); err != nil {
			return models.AdminScript{}, api.BadRequestError("invalid scope", err)
		}
		updates["scope"] = datatypes.NewJSONType(sc)
	}

	if err := m.AdminScript().Update(uint(id), updates); err != nil {
		return models.AdminScript{}, api.InternalError("update script failed", err)
	}
	updated, err := q.AdminScript().GetByID(uint(id))
	if err != nil {
		return models.AdminScript{}, api.InternalError("update script failed", err)
	}
	if err := events.PublishScriptUpdate(context.Background(), c.GetBus(), *updated); err != nil {
		return models.AdminScript{}, api.InternalError("publish script.update failed", err)
	}
	return *updated, nil
}
