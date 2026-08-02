package model_routing

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// PreviewNode 是预览树的一个节点。
// kind=routing 时是中间节点（含 children），kind=model 时是叶子。
type PreviewNode struct {
	Ref          string        `json:"ref"`
	Kind         string        `json:"kind"`            // model | routing | invalid
	Scope        string        `json:"scope,omitempty"` // routing 节点的 scope
	Priority     int           `json:"priority"`
	Weight       int           `json:"weight"`
	EffectivePct float64       `json:"effective_pct"`
	Children     []PreviewNode `json:"children,omitempty"`
	Error        string        `json:"error,omitempty"` // not_found | disabled | cycle | max_depth
}

// PreviewResponse 是 preview 接口的响应结构。
type PreviewResponse struct {
	Root             PreviewNode       `json:"root"`
	EffectiveWeights []EffectiveWeight `json:"effective_weights"`
	Warnings         []string          `json:"warnings"`
}

// EffectiveWeight 表示某个真实 model 的最终权重百分比。
type EffectiveWeight struct {
	Ref     string  `json:"ref"`
	Percent float64 `json:"percent"`
}

func (h *Handler) Preview(c *app.Context, req PreviewRequest) (PreviewResponse, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	// 加载所有 global routings，构建 enabled/disabled 两份索引
	allRoutings, err := q.ModelRouting().ListAllGlobal()
	if err != nil {
		return PreviewResponse{}, api.InternalError("list global routings", err)
	}
	rIdx := make(map[string]*models.ModelRouting, len(allRoutings))
	disabledRIdx := make(map[string]*models.ModelRouting)
	for i := range allRoutings {
		r := &allRoutings[i]
		if r.Enabled {
			rIdx[r.Name] = r
		} else {
			disabledRIdx[r.Name] = r
		}
	}

	// 真实 model 集合：来自 status=enabled 的 channel
	chans, err := q.Channel().ListAll()
	if err != nil {
		return PreviewResponse{}, api.InternalError("list channels", err)
	}
	modelSet := map[string]bool{}
	for _, ch := range chans {
		if ch.Status != consts.StatusEnabled {
			continue
		}
		for _, m := range csvSplit(ch.Models) {
			if m != "" {
				modelSet[m] = true
			}
		}
	}

	// root 是虚拟的"当前正在编辑的 routing"节点
	root := PreviewNode{
		Ref:          req.SelfName,
		Kind:         "routing",
		Scope:        req.SelfScope,
		Priority:     0,
		Weight:       1,
		EffectivePct: 100,
	}
	rootMembers := make([]models.RoutingMember, len(req.Members))
	for i, member := range req.Members {
		rootMembers[i] = models.RoutingMember{Ref: member.Ref, Priority: member.Priority, Weight: member.Weight}
	}
	rootRouteKey := ""
	if existing := rIdx[req.SelfName]; existing != nil {
		rootRouteKey = RoutingWalkKey(existing)
	}
	walked, err := WalkRoutingDestinations(RoutingWalkRequest{
		RootRouteKey: rootRouteKey,
		Members:      rootMembers,
	}, previewRoutingTargetFinder{enabled: rIdx, disabled: disabledRIdx, realModels: modelSet})
	if err != nil {
		return PreviewResponse{}, api.InternalError("walk routing preview", err)
	}
	root.Children = previewNodes(walked)

	// 将叶子节点（真实 model）的 effective_pct 汇总
	weightMap := map[string]float64{}
	flattenWeights(root, weightMap)
	weights := make([]EffectiveWeight, 0, len(weightMap))
	for ref, pct := range weightMap {
		weights = append(weights, EffectiveWeight{Ref: ref, Percent: pct})
	}
	sort.Slice(weights, func(i, j int) bool { return weights[i].Percent > weights[j].Percent })

	return PreviewResponse{Root: root, EffectiveWeights: weights, Warnings: []string{}}, nil
}

// csvSplit 按逗号分割字符串，并去除空白。
func csvSplit(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		out = append(out, p)
	}
	return out
}

type previewRoutingTargetFinder struct {
	enabled    map[string]*models.ModelRouting
	disabled   map[string]*models.ModelRouting
	realModels map[string]bool
}

func (f previewRoutingTargetFinder) FindEnabledRouting(ref string) (RoutingWalkRoute, bool, error) {
	route, ok := f.enabled[ref]
	if !ok {
		return RoutingWalkRoute{}, false, nil
	}
	return previewWalkRoute(route), true, nil
}

func (f previewRoutingTargetFinder) FindDisabledRouting(ref string) (RoutingWalkRoute, bool, error) {
	route, ok := f.disabled[ref]
	if !ok {
		return RoutingWalkRoute{}, false, nil
	}
	return previewWalkRoute(route), true, nil
}

func (f previewRoutingTargetFinder) HasRealModel(ref string) bool { return f.realModels[ref] }

func previewWalkRoute(route *models.ModelRouting) RoutingWalkRoute {
	var members []models.RoutingMember
	// Existing administrator preview behavior is intentionally best-effort for
	// malformed legacy rows. Marketplace uses a separate strict parser.
	_ = json.Unmarshal([]byte(route.Members), &members)
	return RoutingWalkRoute{Key: RoutingWalkKey(route), Scope: route.Scope, Members: members}
}

func previewNodes(nodes []RoutingWalkNode) []PreviewNode {
	if len(nodes) == 0 {
		return nil
	}
	result := make([]PreviewNode, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, PreviewNode{
			Ref:          node.Ref,
			Kind:         string(node.Kind),
			Scope:        node.Scope,
			Priority:     node.Priority,
			Weight:       node.Weight,
			EffectivePct: node.EffectivePercent,
			Children:     previewNodes(node.Children),
			Error:        string(node.Issue),
		})
	}
	return result
}

// flattenWeights 递归收集所有叶子（kind=model）节点的 effective_pct。
func flattenWeights(node PreviewNode, out map[string]float64) {
	if node.Kind == "model" && node.EffectivePct > 0 {
		out[node.Ref] += node.EffectivePct
	}
	for _, c := range node.Children {
		flattenWeights(c, out)
	}
}
