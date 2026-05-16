package model_routing

import (
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Candidates(c *app.Context, _ struct{}) (CandidatesResponse, error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	// 收集 model 名：遍历 status=enabled 的 channel，解析 models CSV 字段
	channels, err := q.Channel().ListAll()
	if err != nil {
		return CandidatesResponse{}, api.InternalError("list channels", err)
	}
	modelSet := map[string]bool{}
	for _, ch := range channels {
		if ch.Status != consts.StatusEnabled {
			continue
		}
		for _, m := range strings.Split(ch.Models, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				modelSet[m] = true
			}
		}
	}

	// 收集 global routing 名称：scope=global + enabled
	routings, err := q.ModelRouting().ListAllGlobal()
	if err != nil {
		return CandidatesResponse{}, api.InternalError("list global routings", err)
	}
	routingNames := make([]string, 0, len(routings))
	for _, r := range routings {
		if !r.Enabled {
			continue
		}
		routingNames = append(routingNames, r.Name)
	}

	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)
	sort.Strings(routingNames)

	return CandidatesResponse{Models: models, GlobalRoutings: routingNames}, nil
}
