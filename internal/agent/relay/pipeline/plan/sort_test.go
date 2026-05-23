package plan

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// toScoredAdmin 把 []*models.Channel 包成 []ScoredCandidate（Source=SourceAdmin）。
func toScoredAdmin(chans []*models.Channel) []ScoredCandidate {
	out := make([]ScoredCandidate, 0, len(chans))
	for _, ch := range chans {
		out = append(out, ScoredCandidate{Channel: ch, Source: state.SourceAdmin, SourceID: ch.ID})
	}
	return out
}

// TestSorter_ByPriorityDescending: success — priority 高的排前面。
func TestSorter_ByPriorityDescending(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 3, Priority: 3, Weight: 1, Status: consts.StatusEnabled}},
	}
	got := priorityWeightedSorter{}.Sort(toScoredAdmin(ch))
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Channel.ID != 2 || got[1].Channel.ID != 3 || got[2].Channel.ID != 1 {
		t.Errorf("order wrong: got [%d %d %d], want [2 3 1]",
			got[0].Channel.ID, got[1].Channel.ID, got[2].Channel.ID)
	}
}

// TestSorter_SkipsDisabled: success — Status != Enabled 的 channel 不进结果。
func TestSorter_SkipsDisabled(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusDisabled, Weight: 1}},
	}
	got := priorityWeightedSorter{}.Sort(toScoredAdmin(ch))
	if len(got) != 1 || got[0].Channel.ID != 1 {
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
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusDisabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusDisabled}},
	}
	if got := (priorityWeightedSorter{}).Sort(toScoredAdmin(ch)); got != nil {
		t.Errorf("all disabled → nil, got %v", got)
	}
}

// TestSorter_SamePriorityContainsAll: boundary — 同 priority 同 weight，洗牌后必须含全部 channel。
func TestSorter_SamePriorityContainsAll(t *testing.T) {
	rand.Seed(1)
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 3, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
	}
	got := priorityWeightedSorter{}.Sort(toScoredAdmin(ch))
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	seen := map[uint]bool{}
	for _, sc := range got {
		seen[sc.Channel.ID] = true
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
			{ChannelCore: models.ChannelCore{ID: 1, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
			{ChannelCore: models.ChannelCore{ID: 2, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
			{ChannelCore: models.ChannelCore{ID: 3, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
		}
		got := priorityWeightedSorter{}.Sort(toScoredAdmin(ch))
		if len(got) != 3 {
			t.Fatalf("trial %d: len = %d, want 3", i, len(got))
		}
		first[got[0].Channel.ID]++
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
			in[i] = &models.Channel{ChannelCore: models.ChannelCore{ID: uint(i + 1), Priority: i % 2, Status: consts.StatusEnabled}}
		}
		return in
	}

	sliceOut := sortByPriorityDesc(toScoredAdmin(mkInput()))

	stableInput := mkInput()
	sort.SliceStable(stableInput, func(i, j int) bool {
		return stableInput[i].Priority > stableInput[j].Priority
	})

	sameOrder := true
	for i, sc := range sliceOut {
		if sc.Channel.ID != stableInput[i].ID {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Errorf("expected sort.Slice output to differ from sort.SliceStable on %d alternating-priority elements; sortByPriorityDesc may have been changed to SliceStable. sliceOut IDs: %v stableOut IDs: %v",
			n, scoredIDs(sliceOut), channelIDs(stableInput))
	}
}

func channelIDs(channels []*models.Channel) []uint {
	ids := make([]uint, len(channels))
	for i, ch := range channels {
		ids[i] = ch.ID
	}
	return ids
}

func scoredIDs(cands []ScoredCandidate) []uint {
	ids := make([]uint, len(cands))
	for i, sc := range cands {
		ids[i] = sc.Channel.ID
	}
	return ids
}

// TestSorter_PrivateBeforeAdmin_SamePriority: success —
// 同 priority 时 private 必排在 admin 前，与输入顺序无关。
// 守护 Task 15 改造：source 是 sort 第一维度，priority 是第二维度。
func TestSorter_PrivateBeforeAdmin_SamePriority(t *testing.T) {
	cands := []ScoredCandidate{
		{
			Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Priority: 100, Weight: 1, Status: consts.StatusEnabled}},
			Source:   state.SourceAdmin,
			SourceID: 1,
		},
		{
			Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 2, Priority: 100, Weight: 1, Status: consts.StatusEnabled}},
			Source:   state.SourcePrivate,
			SourceID: 2,
		},
	}
	got := priorityWeightedSorter{}.Sort(cands)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Source != state.SourcePrivate {
		t.Fatalf("private must rank first regardless of input order, got %v (ID=%d)", got[0].Source, got[0].Channel.ID)
	}
	if got[1].Source != state.SourceAdmin {
		t.Fatalf("admin must rank second, got %v (ID=%d)", got[1].Source, got[1].Channel.ID)
	}
}

// TestSorter_PrivateBeforeAdmin_PrivateLowerPriority: success —
// private priority 极低 + admin priority 极高，结果仍是 private 在前。
// 守护"source rank 主导 priority"契约（替代旧的 +10000 priority offset）。
func TestSorter_PrivateBeforeAdmin_PrivateLowerPriority(t *testing.T) {
	cands := []ScoredCandidate{
		{
			Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Priority: 999999, Weight: 1, Status: consts.StatusEnabled}},
			Source:   state.SourceAdmin,
			SourceID: 1,
		},
		{
			Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 2, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
			Source:   state.SourcePrivate,
			SourceID: 2,
		},
	}
	got := priorityWeightedSorter{}.Sort(cands)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Source != state.SourcePrivate {
		t.Fatalf("private rank must dominate priority: expected SourcePrivate first, got %v (ID=%d Priority=%d)",
			got[0].Source, got[0].Channel.ID, got[0].Channel.Priority)
	}
}

// TestSorter_WithinSameSource_PriorityOrder: success —
// 同 source 内 priority 高的排前。private vs private 与 admin vs admin 行为一致。
func TestSorter_WithinSameSource_PriorityOrder(t *testing.T) {
	cands := []ScoredCandidate{
		{
			Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Priority: 10, Weight: 1, Status: consts.StatusEnabled}},
			Source:   state.SourcePrivate,
			SourceID: 1,
		},
		{
			Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 2, Priority: 100, Weight: 1, Status: consts.StatusEnabled}},
			Source:   state.SourcePrivate,
			SourceID: 2,
		},
	}
	got := priorityWeightedSorter{}.Sort(cands)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Channel.ID != 2 {
		t.Fatalf("higher priority should rank first within same source, got order [%d, %d]",
			got[0].Channel.ID, got[1].Channel.ID)
	}
}

// TestSorter_SourceGroupBoundary_NoCrossSourceShuffle: boundary —
// 不同 source 即使同 priority 也分到不同 group，不能混入 weightedShuffle。
// 通过统计多次 trial，第一位永远是 private 来验证 group 边界。
func TestSorter_SourceGroupBoundary_NoCrossSourceShuffle(t *testing.T) {
	rand.Seed(0xBEEF)
	const trials = 50
	for i := 0; i < trials; i++ {
		cands := []ScoredCandidate{
			{
				Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
				Source:   state.SourceAdmin,
				SourceID: 1,
			},
			{
				Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 2, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
				Source:   state.SourcePrivate,
				SourceID: 2,
			},
		}
		got := priorityWeightedSorter{}.Sort(cands)
		if got[0].Source != state.SourcePrivate {
			t.Fatalf("trial %d: cross-source shuffle leaked — expected SourcePrivate at index 0 in every trial, got %v",
				i, got[0].Source)
		}
	}
}

// TestSorter_MixedPriorityGroupOrder: success — 跨 priority 组的顺序 (高→低)，组内允许随机。
func TestSorter_MixedPriorityGroupOrder(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 10, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 20, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 30, Priority: 5, Weight: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 40, Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
	}
	got := priorityWeightedSorter{}.Sort(toScoredAdmin(ch))
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	// 前 2 个必须是 priority 5 组（ID 20 / 30，顺序随机）；后 2 个必须是 priority 1 组（ID 10 / 40）
	hi := map[uint]bool{got[0].Channel.ID: true, got[1].Channel.ID: true}
	lo := map[uint]bool{got[2].Channel.ID: true, got[3].Channel.ID: true}
	if !(hi[20] && hi[30]) {
		t.Errorf("first 2 should be priority-5 group [20,30], got [%d,%d]", got[0].Channel.ID, got[1].Channel.ID)
	}
	if !(lo[10] && lo[40]) {
		t.Errorf("last 2 should be priority-1 group [10,40], got [%d,%d]", got[2].Channel.ID, got[3].Channel.ID)
	}
}
