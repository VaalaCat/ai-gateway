package plan

import (
	"math/rand"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
)

// ChannelSorter 把 candidate 列表按 (source, priority) 分组 + 组内加权随机洗牌，
// 排出完整顺序。Solver 拿到结果后按下标递增重试，不再每次重新算。
type ChannelSorter interface {
	Sort(cands []ScoredCandidate) []ScoredCandidate
}

// priorityWeightedSorter 是默认实现：
//
//	第一维度：source rank（private 在前，admin 在后）—— 替代旧的 +10000 priority offset，
//	          确保 admin priority 任意大都不会颠倒"私优先 + 共享兜底"。
//	第二维度：priority 降序
//	第三维度：同 (source, priority) 组内加权随机洗牌
type priorityWeightedSorter struct{}

// Sort:
//  1. 过滤 disabled
//  2. (source rank, -priority) 字典序排序（非稳定，组内由加权随机决定顺序）
//  3. 每个 (source, priority) 组内做加权随机洗牌
//  4. 跨组按 (source rank, priority) 顺序 append
//
// 使用 sort.Slice 而非 sort.SliceStable——同 (source, priority) 的 candidate 组内顺序
// 由加权随机洗牌决定。TestSortByPriorityDesc_IsNotStable 守护此契约。
func (priorityWeightedSorter) Sort(cands []ScoredCandidate) []ScoredCandidate {
	var enabled []ScoredCandidate
	for _, sc := range cands {
		if sc.Channel.Status == consts.StatusEnabled {
			enabled = append(enabled, sc)
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	enabled = sortByPriorityDesc(enabled)

	var out []ScoredCandidate
	for i := 0; i < len(enabled); {
		j := i
		for j < len(enabled) &&
			enabled[j].Source == enabled[i].Source &&
			enabled[j].Channel.Priority == enabled[i].Channel.Priority {
			j++
		}
		out = append(out, weightedShuffle(enabled[i:j])...)
		i = j
	}
	return out
}

// sortByPriorityDesc 按 (source rank, priority desc) 排序（非稳定，由后续 weightedShuffle
// 决定组内顺序）。
//
// 命名保留 "PriorityDesc"——多数测试与外部读者把 priority 当作主导维度阅读；source
// rank 是改造引入的"私优先 + 共享兜底"约束，priority 仍是同 source 内的主排序键。
func sortByPriorityDesc(cands []ScoredCandidate) []ScoredCandidate {
	sort.Slice(cands, func(i, j int) bool {
		ri, rj := sourceRank(cands[i].Source), sourceRank(cands[j].Source)
		if ri != rj {
			return ri < rj
		}
		return cands[i].Channel.Priority > cands[j].Channel.Priority
	})
	return cands
}

// sourceRank 决定跨 source 的排序：private 优先（0），admin 兜底（1）。
// 未知 source 与 admin 同 rank——保守兜底，避免 panic。
func sourceRank(src state.ChannelSource) int {
	if src == state.SourcePrivate {
		return 0
	}
	return 1
}

// weightedShuffle 按 weight 加权随机抽序；weight <= 0 视作 1。
func weightedShuffle(group []ScoredCandidate) []ScoredCandidate {
	if len(group) == 0 {
		return nil
	}
	if len(group) == 1 {
		return []ScoredCandidate{group[0]}
	}
	remaining := append([]ScoredCandidate{}, group...)
	out := make([]ScoredCandidate, 0, len(remaining))
	for len(remaining) > 0 {
		total := 0
		for _, sc := range remaining {
			w := int(sc.Channel.Weight)
			if w <= 0 {
				w = 1
			}
			total += w
		}
		r := rand.Intn(total)
		pick := 0
		for k, sc := range remaining {
			w := int(sc.Channel.Weight)
			if w <= 0 {
				w = 1
			}
			r -= w
			if r < 0 {
				pick = k
				break
			}
		}
		out = append(out, remaining[pick])
		remaining = append(remaining[:pick], remaining[pick+1:]...)
	}
	return out
}
