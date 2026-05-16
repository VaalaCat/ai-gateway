package plan

import (
	"math/rand"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// ChannelSorter 把 channel 列表按 priority 分组 + 组内加权随机，排出完整顺序。
// Solver 拿到结果后按下标递增重试，不再每次重新算。
type ChannelSorter interface {
	Sort(channels []*models.Channel) []*models.Channel
}

// priorityWeightedSorter 是 ChannelSorter 的默认实现。
// 按 priority 降序排序 + 同 priority 组内加权随机洗牌。
type priorityWeightedSorter struct{}

// Sort:
//  1. 过滤 disabled；
//  2. priority 降序排序（非稳定，priority 相同时由组内加权随机决定顺序）；
//  3. 每个 priority 组内做加权随机洗牌；
//  4. 跨组按 priority 顺序 append。
//
// nil 或全 disabled 返回 nil（不 panic、不返回 [] 空切片）。
//
// 排序使用 sort.Slice 而非 sort.SliceStable——priority 相同的 channel 组内顺序
// 由加权随机洗牌决定，不应当依赖输入顺序。
func (priorityWeightedSorter) Sort(channels []*models.Channel) []*models.Channel {
	var enabled []*models.Channel
	for _, ch := range channels {
		if ch.Status == consts.StatusEnabled {
			enabled = append(enabled, ch)
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	enabled = sortByPriorityDesc(enabled)

	var out []*models.Channel
	for i := 0; i < len(enabled); {
		j := i
		for j < len(enabled) && enabled[j].Priority == enabled[i].Priority {
			j++
		}
		out = append(out, weightedShuffle(enabled[i:j])...)
		i = j
	}
	return out
}

// sortByPriorityDesc 把 channels 按 priority 降序排序（in-place 后返回同一 slice）。
// 故意用 sort.Slice 而非 sort.SliceStable——priority 相同的元素允许交换顺序，
// 后续 weightedShuffle 会在 priority 相同的组内重新做加权随机抽序。
//
// 这种"非稳定"行为是有意为之，由 TestSortByPriorityDesc_IsNotStable 守护：
// 若有人改成 sort.SliceStable，该测试会挂。
func sortByPriorityDesc(channels []*models.Channel) []*models.Channel {
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].Priority > channels[j].Priority
	})
	return channels
}

// weightedShuffle 按权重抽序：每次按权重抽一个，剩余的递归抽到队列空。
// weight <= 0 视作 1。
func weightedShuffle(group []*models.Channel) []*models.Channel {
	if len(group) == 0 {
		return nil
	}
	if len(group) == 1 {
		return []*models.Channel{group[0]}
	}
	remaining := append([]*models.Channel{}, group...)
	out := make([]*models.Channel, 0, len(remaining))
	for len(remaining) > 0 {
		total := 0
		for _, ch := range remaining {
			w := int(ch.Weight)
			if w <= 0 {
				w = 1
			}
			total += w
		}
		r := rand.Intn(total)
		pick := 0
		for k, ch := range remaining {
			w := int(ch.Weight)
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
