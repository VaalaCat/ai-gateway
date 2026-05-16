package plan

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// TestSorter_ByPriorityDescending: success — priority 高的排前面。
func TestSorter_ByPriorityDescending(t *testing.T) {
	ch := []*models.Channel{
		{ID: 1, Priority: 1, Weight: 1, Status: consts.StatusEnabled},
		{ID: 2, Priority: 5, Weight: 1, Status: consts.StatusEnabled},
		{ID: 3, Priority: 3, Weight: 1, Status: consts.StatusEnabled},
	}
	got := priorityWeightedSorter{}.Sort(ch)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != 2 || got[1].ID != 3 || got[2].ID != 1 {
		t.Errorf("order wrong: got [%d %d %d], want [2 3 1]",
			got[0].ID, got[1].ID, got[2].ID)
	}
}

// TestSorter_SkipsDisabled: success — Status != Enabled 的 channel 不进结果。
func TestSorter_SkipsDisabled(t *testing.T) {
	ch := []*models.Channel{
		{ID: 1, Status: consts.StatusEnabled, Weight: 1},
		{ID: 2, Status: consts.StatusDisabled, Weight: 1},
	}
	got := priorityWeightedSorter{}.Sort(ch)
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("disabled should skip, got %v", got)
	}
}

// TestSorter_NilInput: boundary — nil 输入返回 nil。
func TestSorter_NilInput(t *testing.T) {
	if got := (priorityWeightedSorter{}).Sort(nil); got != nil {
		t.Errorf("nil → nil, got %v", got)
	}
}

// TestSorter_AllDisabledReturnsNil: failure — 全 disabled 返回 nil。
func TestSorter_AllDisabledReturnsNil(t *testing.T) {
	ch := []*models.Channel{
		{ID: 1, Status: consts.StatusDisabled},
		{ID: 2, Status: consts.StatusDisabled},
	}
	if got := (priorityWeightedSorter{}).Sort(ch); got != nil {
		t.Errorf("all disabled → nil, got %v", got)
	}
}

// TestSorter_SamePriorityContainsAll: boundary — 同 priority 同 weight，洗牌后必须含全部 channel。
func TestSorter_SamePriorityContainsAll(t *testing.T) {
	rand.Seed(1)
	ch := []*models.Channel{
		{ID: 1, Priority: 1, Weight: 1, Status: consts.StatusEnabled},
		{ID: 2, Priority: 1, Weight: 1, Status: consts.StatusEnabled},
		{ID: 3, Priority: 1, Weight: 1, Status: consts.StatusEnabled},
	}
	got := priorityWeightedSorter{}.Sort(ch)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	seen := map[uint]bool{}
	for _, c := range got {
		seen[c.ID] = true
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Errorf("missing channel: %v", got)
	}
}

// TestSorter_SamePriorityGroupNotInputOrderDependent:
//
// priority 相同的 channel 组内顺序不应当依赖输入顺序——sort.Slice（非稳定）配合
// weightedShuffle 的随机性实现负载均衡。误用 sort.SliceStable 会让 priority
// 相同时退化为"先 push 先选"，丢失随机性。
//
// 验证方法：构造同 priority 同 weight 的 3 个 channel，多次 Sort 应至少出现 2 种不同的
// 头位 ID（统计意义上 100 次 ~99.99% 触发）。失败说明排序是稳定的退化分支。
func TestSorter_SamePriorityGroupNotInputOrderDependent(t *testing.T) {
	rand.Seed(0xC0FFEE)
	first := map[uint]int{}
	const trials = 100
	for i := 0; i < trials; i++ {
		ch := []*models.Channel{
			{ID: 1, Priority: 5, Weight: 1, Status: consts.StatusEnabled},
			{ID: 2, Priority: 5, Weight: 1, Status: consts.StatusEnabled},
			{ID: 3, Priority: 5, Weight: 1, Status: consts.StatusEnabled},
		}
		got := priorityWeightedSorter{}.Sort(ch)
		if len(got) != 3 {
			t.Fatalf("trial %d: len = %d, want 3", i, len(got))
		}
		first[got[0].ID]++
	}
	// 期望 3 个 ID 都出现过头位（同概率 1/3，100 trials 漏一个 ~3e-18 概率）
	if len(first) < 2 {
		t.Errorf("priority-equal 组内排序退化为稳定/确定性，所有 trial 都选 %v 当头位 — sort.Slice 的非稳定性是有意为之", first)
	}
}

// TestSortByPriorityDesc_IsNotStable: mutation guard — sortByPriorityDesc 必须用 sort.Slice（非稳定），
// 而不是 sort.SliceStable。修审计 D-C1 / 第二轮审计 #2：旧的
// TestSorter_SamePriorityGroupNotInputOrderDependent 用的是 Sort 全链路，组内顺序被
// weightedShuffle 重洗，无法捕捉到 sort.Slice → sort.SliceStable 的回归。
//
// 构造方法：16 个 channel，priority 交替 0/1（不能用全同 priority，因为 pdqsort
// 对全等序列有"已排序"快速路径不做 swap；也不能用 N<=12，因为小数据集走 insertion
// sort 是稳定的）。分别调 sortByPriorityDesc 和 sort.SliceStable，对比输出 ID 序列：
// pdqsort 的 partition 会在同 priority 元素间做不少量级 swap，结果必然与 stable 版不同。
//
// 若 Go 未来某版本对 N=16 也变稳定，把 N 加大到 32 或 64。
func TestSortByPriorityDesc_IsNotStable(t *testing.T) {
	const n = 16
	mkInput := func() []*models.Channel {
		in := make([]*models.Channel, n)
		for i := range in {
			// priority 交替 0/1 — 强制 pdqsort 走 partition 路径而非快速路径
			in[i] = &models.Channel{ID: uint(i + 1), Priority: i % 2, Status: consts.StatusEnabled}
		}
		return in
	}

	sliceOut := sortByPriorityDesc(mkInput())

	stableOut := mkInput()
	sort.SliceStable(stableOut, func(i, j int) bool {
		return stableOut[i].Priority > stableOut[j].Priority
	})

	sameOrder := true
	for i, ch := range sliceOut {
		if ch.ID != stableOut[i].ID {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Errorf("expected sort.Slice output to differ from sort.SliceStable on %d alternating-priority elements; sortByPriorityDesc may have been changed to SliceStable. sliceOut IDs: %v stableOut IDs: %v",
			n, channelIDs(sliceOut), channelIDs(stableOut))
	}
}

func channelIDs(channels []*models.Channel) []uint {
	ids := make([]uint, len(channels))
	for i, ch := range channels {
		ids[i] = ch.ID
	}
	return ids
}

// TestSorter_MixedPriorityGroupOrder: success — 跨 priority 组的顺序 (高→低)，组内允许随机。
func TestSorter_MixedPriorityGroupOrder(t *testing.T) {
	ch := []*models.Channel{
		{ID: 10, Priority: 1, Weight: 1, Status: consts.StatusEnabled},
		{ID: 20, Priority: 5, Weight: 1, Status: consts.StatusEnabled},
		{ID: 30, Priority: 5, Weight: 1, Status: consts.StatusEnabled},
		{ID: 40, Priority: 1, Weight: 1, Status: consts.StatusEnabled},
	}
	got := priorityWeightedSorter{}.Sort(ch)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	// 前 2 个必须是 priority 5 组（ID 20 / 30，顺序随机）；后 2 个必须是 priority 1 组（ID 10 / 40）
	hi := map[uint]bool{got[0].ID: true, got[1].ID: true}
	lo := map[uint]bool{got[2].ID: true, got[3].ID: true}
	if !(hi[20] && hi[30]) {
		t.Errorf("first 2 should be priority-5 group [20,30], got [%d,%d]", got[0].ID, got[1].ID)
	}
	if !(lo[10] && lo[40]) {
		t.Errorf("last 2 should be priority-1 group [10,40], got [%d,%d]", got[2].ID, got[3].ID)
	}
}
