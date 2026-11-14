package plan

import (
	"context"
	"math/rand"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const routingMaxDepth = 5

// RoutingStore 是 routing 解析需要的最小存储接口。
// *cache.Store 自然满足；测试和 BYOK 扩展可以用 stub 替换，
// 避免 relay 包对 cache 具体类型的硬依赖。
type RoutingStore interface {
	ResolveRouting(ctx context.Context, name string, owner protocol.RoutingOwner) *protocol.SyncedRouting
	GetGlobalRouting(ctx context.Context, name string) *protocol.SyncedRouting
}

// ResolveCtx 在单次请求内贯穿，记录已尝试成员、已访问 routing、深度、trace。
// 由 ModelChainBuilder.Build 创建并复用：每次链失败后调 MarkMemberExhausted
// 重新 ResolveToRealModel 寻找下一个成员。
type ResolveCtx struct {
	excluded   map[uint]map[string]bool // routingID → ref → tried
	visited    map[uint]bool
	depth      int
	trace      []string
	lastChain  []uint // 上次成功 resolve 经过的 routing IDs（深→浅，resolveStep 成功路径自下而上 append）
	pickMember func([]protocol.RoutingMember, map[string]bool) *protocol.RoutingMember
	// Inspection must keep exhausting a different routing path even when it
	// converges on the same real model as the previous path. Online Relay keeps
	// the historical stop-on-repeat behavior.
	exhaustRepeatedModels bool
	topLevelRouting       bool
}

type modelResolution struct {
	model           string
	topLevelRouting bool
}

func NewResolveCtx() *ResolveCtx {
	return &ResolveCtx{
		excluded:   make(map[uint]map[string]bool),
		visited:    make(map[uint]bool),
		pickMember: selectRoutingMember,
	}
}

// newDeterministicResolveCtx keeps the production routing semantics while
// replacing weighted random ordering with a stable priority/name order. It is
// used only by read-only exhaustive inspection; online Relay keeps NewResolveCtx.
func newDeterministicResolveCtx() *ResolveCtx {
	walk := NewResolveCtx()
	walk.pickMember = selectDeterministicRoutingMember
	walk.exhaustRepeatedModels = true
	return walk
}

// Trace 返回解析路径快照（仅调试 / UsageLog 写入用）。
func (c *ResolveCtx) Trace() []string {
	out := make([]string, len(c.trace))
	copy(out, c.trace)
	return out
}

// MarkMemberExhausted 让本次请求后续 ResolveToRealModel 跳过 realModel。
// 通过把 lastChain 上每一层 routing 的 excluded 都加上 realModel 实现。
// lastChain 由 resolveStep 成功路径上自下而上 append。
func (c *ResolveCtx) MarkMemberExhausted(realModel string) {
	for _, routingID := range c.lastChain {
		if c.excluded[routingID] == nil {
			c.excluded[routingID] = make(map[string]bool)
		}
		c.excluded[routingID]["model:"+realModel] = true
	}
	// visited/depth 已被 defer 清理，无需重置
	// trace 不动，由调用方决定是否清空
}

// ResolveToRealModel 把入参 ref 解析为真实 model 名；空串表示该次解析整链失败。
// 见 spec §3.2-3.4。
func ResolveToRealModel(requestCtx context.Context, store RoutingStore, ref string, owner protocol.RoutingOwner, walk *ResolveCtx) string {
	return resolveModel(requestCtx, store, ref, owner, walk).model
}

func resolveModel(requestCtx context.Context, store RoutingStore, ref string, owner protocol.RoutingOwner, walk *ResolveCtx) modelResolution {
	walk.lastChain = walk.lastChain[:0] // 清空上轮路径，保留 cap
	walk.topLevelRouting = false
	model := resolveStep(requestCtx, store, ref, owner, walk, "" /* topLevel */)
	return modelResolution{model: model, topLevelRouting: walk.topLevelRouting}
}

func resolveStep(requestCtx context.Context, store RoutingStore, ref string, owner protocol.RoutingOwner, walk *ResolveCtx, kind string) string {
	if kind == "model" {
		return ref
	}
	if kind != "" && kind != "routing" {
		walk.trace = append(walk.trace, ref+":invalid_kind")
		return ""
	}
	if walk.depth >= routingMaxDepth {
		walk.trace = append(walk.trace, ref+":depth_exceeded")
		return ""
	}

	var r *protocol.SyncedRouting
	if kind == "" {
		r = store.ResolveRouting(requestCtx, ref, owner)
		walk.topLevelRouting = r != nil
	} else {
		// 递归：成员只能引用全局 routing
		r = store.GetGlobalRouting(requestCtx, ref)
	}
	if r == nil {
		if kind == "" {
			return ref
		}
		walk.trace = append(walk.trace, ref+":routing_not_found")
		return ""
	}
	if walk.visited[r.ID] {
		walk.trace = append(walk.trace, ref+":cycle")
		return ""
	}

	walk.visited[r.ID] = true
	if walk.excluded[r.ID] == nil {
		walk.excluded[r.ID] = make(map[string]bool)
	}
	walk.depth++
	walk.trace = append(walk.trace, r.Scope+":"+r.Name)

	defer func() {
		delete(walk.visited, r.ID)
		walk.depth--
	}()

	for {
		pickMember := walk.pickMember
		if pickMember == nil {
			pickMember = selectRoutingMember
		}
		member := pickMember(r.Members, walk.excluded[r.ID])
		if member == nil {
			return ""
		}
		if member.Kind != "model" && member.Kind != "routing" {
			walk.trace = append(walk.trace, member.Ref+":invalid_kind")
			walk.excluded[r.ID][routingMemberKey(*member)] = true
			continue
		}
		result := resolveStep(requestCtx, store, member.Ref, owner, walk, member.Kind)
		if result != "" {
			walk.lastChain = append(walk.lastChain, r.ID)
			return result
		}
		// 递归失败：才把这个 member 加 excluded，成功路径不 mark
		walk.excluded[r.ID][routingMemberKey(*member)] = true
	}
}

func selectDeterministicRoutingMember(
	members []protocol.RoutingMember,
	excluded map[string]bool,
) *protocol.RoutingMember {
	candidates := make([]protocol.RoutingMember, 0, len(members))
	for _, member := range members {
		if !excluded[routingMemberKey(member)] {
			candidates = append(candidates, member)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return candidates[i].Ref < candidates[j].Ref
	})
	return &candidates[0]
}

// selectRoutingMember 按 priority 分组取最高组、组内按 weight 加权随机选一个 ref。
// excluded 中的 ref 跳过。
func selectRoutingMember(members []protocol.RoutingMember, excluded map[string]bool) *protocol.RoutingMember {
	var candidates []protocol.RoutingMember
	for _, m := range members {
		if excluded[routingMemberKey(m)] {
			continue
		}
		candidates = append(candidates, m)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Priority > candidates[j].Priority
	})
	top := candidates[0].Priority
	var group []protocol.RoutingMember
	for _, m := range candidates {
		if m.Priority != top {
			break
		}
		group = append(group, m)
	}
	return weightedRandomMember(group)
}

func weightedRandomMember(members []protocol.RoutingMember) *protocol.RoutingMember {
	total := 0
	for _, m := range members {
		w := m.Weight
		if w <= 0 {
			w = 1
		}
		total += w
	}
	// total 恒 > 0（members 非空 + 每个 w 至少 1）
	r := rand.Intn(total)
	for _, m := range members {
		w := m.Weight
		if w <= 0 {
			w = 1
		}
		r -= w
		if r < 0 {
			return &m
		}
	}
	return &members[len(members)-1]
}

func routingMemberKey(m protocol.RoutingMember) string { return m.Kind + ":" + m.Ref }
