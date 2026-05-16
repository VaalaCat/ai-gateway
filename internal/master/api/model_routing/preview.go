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
	Root             PreviewNode     `json:"root"`
	EffectiveWeights []EffectiveWeight `json:"effective_weights"`
	Warnings         []string        `json:"warnings"`
}

// EffectiveWeight 表示某个真实 model 的最终权重百分比。
type EffectiveWeight struct {
	Ref     string  `json:"ref"`
	Percent float64 `json:"percent"`
}

const previewMaxDepth = 5

func (h *Handler) Preview(c *app.Context, req PreviewRequest) (PreviewResponse, error) {
	daoCtx := dao.NewContext(c.App)
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

	// visited 用于环检测；若在编辑已存在的 routing，先把自己加入
	visited := map[string]bool{}
	if req.SelfName != "" {
		visited[req.SelfName] = true
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
	root.Children = buildChildren(req.Members, rIdx, disabledRIdx, modelSet, visited, 1, 100.0)

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

// buildChildren 按 spec §3.1 构建子节点列表：
//   - 找出最高 priority 组，按 weight 加权分配 parentPct
//   - 低 priority 组也加入 children，但 effective_pct=0（作为 backup 展示）
func buildChildren(members []MemberInput, rIdx map[string]*models.ModelRouting,
	disabledRIdx map[string]*models.ModelRouting, modelSet map[string]bool,
	visited map[string]bool, depth int, parentPct float64) []PreviewNode {

	if depth > previewMaxDepth {
		return []PreviewNode{{Kind: "invalid", Error: "max_depth"}}
	}
	if len(members) == 0 {
		return nil
	}

	// 找最高 priority
	maxPriority := members[0].Priority
	for _, m := range members[1:] {
		if m.Priority > maxPriority {
			maxPriority = m.Priority
		}
	}

	// 顶组：最高 priority 成员
	var topGroup []MemberInput
	for _, m := range members {
		if m.Priority == maxPriority {
			topGroup = append(topGroup, m)
		}
	}

	// 顶组总权重
	totalW := 0
	for _, m := range topGroup {
		w := m.Weight
		if w <= 0 {
			w = 1
		}
		totalW += w
	}

	var children []PreviewNode

	// 顶组成员：按 weight 分配 effective_pct
	for _, m := range topGroup {
		w := m.Weight
		if w <= 0 {
			w = 1
		}
		share := parentPct * float64(w) / float64(totalW)
		node := PreviewNode{
			Ref:          m.Ref,
			Priority:     m.Priority,
			Weight:       m.Weight,
			EffectivePct: share,
		}
		if r, isRouting := rIdx[m.Ref]; isRouting {
			if visited[m.Ref] {
				node.Kind = "routing"
				node.Error = "cycle"
			} else {
				node.Kind = "routing"
				node.Scope = r.Scope
				visited[m.Ref] = true
				subMembers := parseRoutingMembers(r.Members)
				inputs := make([]MemberInput, len(subMembers))
				for i, sm := range subMembers {
					inputs[i] = MemberInput{Ref: sm.Ref, Priority: sm.Priority, Weight: sm.Weight}
				}
				node.Children = buildChildren(inputs, rIdx, disabledRIdx, modelSet, visited, depth+1, share)
				delete(visited, m.Ref)
			}
		} else if dr, disabled := disabledRIdx[m.Ref]; disabled {
			node.Kind = "routing"
			node.Scope = dr.Scope
			node.Error = "disabled"
		} else if modelSet[m.Ref] {
			node.Kind = "model"
		} else {
			node.Kind = "invalid"
			node.Error = "not_found"
		}
		children = append(children, node)
	}

	// 低 priority 成员：effective_pct=0，递归展开 routing children（parentPct=0 保证子树权重为 0）
	for _, m := range members {
		if m.Priority == maxPriority {
			continue
		}
		node := PreviewNode{
			Ref:          m.Ref,
			Priority:     m.Priority,
			Weight:       m.Weight,
			EffectivePct: 0,
		}
		if r, isRouting := rIdx[m.Ref]; isRouting {
			if visited[m.Ref] {
				node.Kind = "routing"
				node.Error = "cycle"
			} else {
				node.Kind = "routing"
				node.Scope = r.Scope
				visited[m.Ref] = true
				var subMembers []MemberInput
				for _, sm := range parseRoutingMembers(r.Members) {
					subMembers = append(subMembers, MemberInput{Ref: sm.Ref, Priority: sm.Priority, Weight: sm.Weight})
				}
				// parentPct=0：整棵子树 effective_pct 为 0，不影响累加结果
				node.Children = buildChildren(subMembers, rIdx, disabledRIdx, modelSet, visited, depth+1, 0)
				delete(visited, m.Ref)
			}
		} else if dr, disabled := disabledRIdx[m.Ref]; disabled {
			node.Kind = "routing"
			node.Scope = dr.Scope
			node.Error = "disabled"
		} else if modelSet[m.Ref] {
			node.Kind = "model"
		} else {
			node.Kind = "invalid"
			node.Error = "not_found"
		}
		children = append(children, node)
	}

	return children
}

// parseRoutingMembers 解析 routing.Members JSON 字段。
func parseRoutingMembers(jsonStr string) []models.RoutingMember {
	var ms []models.RoutingMember
	_ = json.Unmarshal([]byte(jsonStr), &ms)
	return ms
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
