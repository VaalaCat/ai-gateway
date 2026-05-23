package modelview

import (
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// stubStore 是 modelview 测试用的 ModelStore 实现。
type stubStore struct {
	adminModels   []string
	channelsByMdl map[string][]*models.Channel
	globalRouting []string
	userRouting   map[uint][]string
	byokByUser    map[uint][]string
}

func (s *stubStore) GetAllModelNames() []string { return s.adminModels }
func (s *stubStore) GetChannelsForModel(name string) []*models.Channel {
	return s.channelsByMdl[name]
}
func (s *stubStore) ListGlobalRoutingNames() []string       { return s.globalRouting }
func (s *stubStore) ListUserRoutingNames(uid uint) []string { return s.userRouting[uid] }
func (s *stubStore) ListVisibleBYOKModelNamesForUser(uid uint) []string {
	return s.byokByUser[uid]
}

// compileAssert: stubStore 必须满足 ModelStore。
var _ ModelStore = (*stubStore)(nil)

// asNamesAndOwners 把结果折叠成 [(name, owned_by), ...]，便于断言。
func asNamesAndOwners(got []ListedModel) [][2]string {
	out := make([][2]string, 0, len(got))
	for _, m := range got {
		out = append(out, [2]string{m.Name, m.OwnedBy})
	}
	return out
}

func mustEqual(t *testing.T, got, want [][2]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v\nwant=%v", got, want)
	}
}

// Case 3（核心 bug 场景）：admin 段被 AllowedChannelIDs 排除，BYOK 段有该 model。
// 当前 main 分支会缺失 gpt-5；本 PR 必须让它出现。
func TestListVisibleModels_AdminBlockedButBYOKHas(t *testing.T) {
	store := &stubStore{
		adminModels: []string{"gpt-5"},
		channelsByMdl: map[string][]*models.Channel{
			"gpt-5": {{ChannelCore: models.ChannelCore{ID: 99, Status: 1}}},
		},
		byokByUser: map[uint][]string{42: {"gpt-5"}},
	}
	ui := &app.UserInfo{UserID: 42, AllowedChannelIDs: []uint{1}} // 1 != 99 故 admin gpt-5 被滤掉
	got := ListVisibleModels(store, ui)
	mustEqual(t, asNamesAndOwners(got), [][2]string{
		{"gpt-5", "ai-gateway-byok"},
	})
}

// Case 1：仅 admin，无 BYOK。
func TestListVisibleModels_OnlyAdmin(t *testing.T) {
	store := &stubStore{adminModels: []string{"gpt-4"}}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 42})
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-4", "ai-gateway"}})
}

// Case 2：仅 BYOK，admin 无。
func TestListVisibleModels_OnlyBYOK(t *testing.T) {
	store := &stubStore{byokByUser: map[uint][]string{42: {"gpt-5"}}}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 42})
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-5", "ai-gateway-byok"}})
}

// Case 4：admin 与 BYOK 同名 → BYOK 覆盖，保留 admin 原位。
func TestListVisibleModels_BYOKOverridesAdmin(t *testing.T) {
	store := &stubStore{
		adminModels:   []string{"gpt-4"},
		channelsByMdl: map[string][]*models.Channel{"gpt-4": {{ChannelCore: models.ChannelCore{ID: 1, Status: 1}}}},
		byokByUser:    map[uint][]string{42: {"gpt-4"}},
	}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 42})
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-4", "ai-gateway-byok"}})
}

// Case 5：TokenModels 也对 BYOK 生效。
func TestListVisibleModels_TokenModelsAppliesToBYOK(t *testing.T) {
	store := &stubStore{
		adminModels:   []string{"gpt-4"},
		channelsByMdl: map[string][]*models.Channel{"gpt-4": {{ChannelCore: models.ChannelCore{ID: 1, Status: 1}}}},
		byokByUser:    map[uint][]string{42: {"gpt-5"}},
	}
	ui := &app.UserInfo{UserID: 42, TokenModels: []string{"gpt-4"}}
	got := ListVisibleModels(store, ui)
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-4", "ai-gateway"}})
}

// Case 6：GroupModels 也对 BYOK 生效。
func TestListVisibleModels_GroupModelsAppliesToBYOK(t *testing.T) {
	store := &stubStore{
		adminModels:   []string{"gpt-4"},
		channelsByMdl: map[string][]*models.Channel{"gpt-4": {{ChannelCore: models.ChannelCore{ID: 1, Status: 1}}}},
		byokByUser:    map[uint][]string{42: {"gpt-5"}},
	}
	ui := &app.UserInfo{UserID: 42, GroupModels: []string{"gpt-4"}}
	got := ListVisibleModels(store, ui)
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-4", "ai-gateway"}})
}

// Case 7：BYOK 名与 routing 同名 → 让位 routing，BYOK 段不列。
func TestListVisibleModels_BYOKDefersToRouting(t *testing.T) {
	store := &stubStore{
		globalRouting: []string{"foo"},
		byokByUser:    map[uint][]string{42: {"foo"}},
	}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 42})
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"foo", "ai-gateway-routing"}})
}

// Case 8：UserInfo == nil → admin 全集，BYOK 段空，仅 global routing。
func TestListVisibleModels_NilUserInfo(t *testing.T) {
	store := &stubStore{
		adminModels: []string{"gpt-4"},
		byokByUser:  map[uint][]string{42: {"gpt-5"}},
	}
	got := ListVisibleModels(store, nil)
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-4", "ai-gateway"}})
}

// Case 9：UserID == 0 → 同 nil 语义。
func TestListVisibleModels_ZeroUserID(t *testing.T) {
	store := &stubStore{
		adminModels: []string{"gpt-4"},
		byokByUser:  map[uint][]string{42: {"gpt-5"}},
	}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 0})
	mustEqual(t, asNamesAndOwners(got), [][2]string{{"gpt-4", "ai-gateway"}})
}

// Case 10：routing 段保持现有顺序（global → user）；admin → byok → routing。
func TestListVisibleModels_RoutingOrderPreserved(t *testing.T) {
	store := &stubStore{
		adminModels:   []string{"gpt-4"},
		channelsByMdl: map[string][]*models.Channel{"gpt-4": {{ChannelCore: models.ChannelCore{ID: 1, Status: 1}}}},
		byokByUser:    map[uint][]string{42: {"gpt-5"}},
		globalRouting: []string{"r1"},
		userRouting:   map[uint][]string{42: {"r2"}},
	}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 42})
	mustEqual(t, asNamesAndOwners(got), [][2]string{
		{"gpt-4", "ai-gateway"},
		{"gpt-5", "ai-gateway-byok"},
		{"r1", "ai-gateway-routing"},
		{"r2", "ai-gateway-routing"},
	})
}

// Case 11：三段全空 → 空切片。
func TestListVisibleModels_AllEmpty(t *testing.T) {
	store := &stubStore{}
	got := ListVisibleModels(store, &app.UserInfo{UserID: 42})
	if len(got) != 0 {
		t.Fatalf("expected empty, got=%v", got)
	}
}

// Case 12：channel 白名单只过 admin，不过 BYOK。
func TestListVisibleModels_ChannelWhitelistSkipsBYOK(t *testing.T) {
	store := &stubStore{
		adminModels: []string{"gpt-4", "gpt-5"},
		channelsByMdl: map[string][]*models.Channel{
			"gpt-4": {{ChannelCore: models.ChannelCore{ID: 10, Status: 1}}},
			"gpt-5": {{ChannelCore: models.ChannelCore{ID: 20, Status: 1}}},
		},
		byokByUser: map[uint][]string{42: {"gpt-7"}},
	}
	ui := &app.UserInfo{UserID: 42, AllowedChannelIDs: []uint{10}}
	got := ListVisibleModels(store, ui)
	mustEqual(t, asNamesAndOwners(got), [][2]string{
		{"gpt-4", "ai-gateway"},
		{"gpt-7", "ai-gateway-byok"},
	})
}
