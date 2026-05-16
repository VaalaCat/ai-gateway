package model_routing

import (
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// GetResponse 在 ModelRouting 基础上附加展开后的真实模型名列表。
type GetResponse struct {
	models.ModelRouting
	ExpandedModels []string `json:"expanded_models"`
}

func (h *Handler) Get(c *app.Context, req api.IDPathRequest) (GetResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	r, err := q.ModelRouting().GetByID(uint(id))
	if err != nil || r == nil {
		return GetResponse{}, api.NotFoundError("model routing not found")
	}
	return GetResponse{
		ModelRouting:   *r,
		ExpandedModels: expandRoutingToModels(r, q),
	}, nil
}

// expandRoutingToModels 递归展开 routing 成员，返回去重后的真实 model 名集合。
// 遇到子 routing 则继续递归（最深 5 层），否则视为真实 model 名。
func expandRoutingToModels(r *models.ModelRouting, q dao.AdminQuery) []string {
	seen := map[string]bool{}
	var visit func(routing *models.ModelRouting, depth int)
	visit = func(routing *models.ModelRouting, depth int) {
		if depth > 5 {
			return
		}
		var members []models.RoutingMember
		if err := json.Unmarshal([]byte(routing.Members), &members); err != nil {
			return
		}
		for _, m := range members {
			child, _ := q.ModelRouting().GetByName(models.RoutingScopeGlobal, 0, m.Ref)
			if child != nil && child.Enabled {
				visit(child, depth+1)
			} else {
				seen[m.Ref] = true
			}
		}
	}
	visit(r, 0)

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}
