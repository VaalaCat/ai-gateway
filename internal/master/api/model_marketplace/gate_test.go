package model_marketplace

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAccessGateRequireUserSuccessMatrix(t *testing.T) {
	const now = int64(1_800_000_000)
	tests := []struct {
		name             string
		token            *models.Token
		group            *models.UserGroup
		wantChannels     []uint
		wantGroupIDs     []uint
		wantBYOKOnly     bool
		allowedModels    []string
		rejectedModels   []string
		wantCallSequence []string
	}{
		{
			name: "token and group restrictions are both enforced",
			token: &models.Token{
				ID: 23, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: now + 1,
				Models: ` ["gpt-.*", "claude-3"] `, BYOKOnly: true,
				AllowedChannelIDs: datatypes.JSONSlice[uint]{2, 3, 4},
			},
			group: &models.UserGroup{
				ID: 9, Status: consts.StatusEnabled, Models: `["gpt-4o", "claude-.*"]`,
				AllowedChannelIDs: datatypes.JSONSlice[uint]{1, 2, 4},
			},
			wantChannels:     []uint{2, 4},
			wantGroupIDs:     []uint{9},
			wantBYOKOnly:     true,
			allowedModels:    []string{"gpt-4o", "claude-3"},
			rejectedModels:   []string{"gpt-5", "claude-2"},
			wantCallSequence: []string{"settings", "owned-token", "user-group"},
		},
		{
			name: "empty group restrictions preserve token restrictions",
			token: &models.Token{
				ID: 24, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1,
				Models: `["gpt-4o"]`, AllowedChannelIDs: datatypes.JSONSlice[uint]{5, 5, 6},
			},
			group:            &models.UserGroup{ID: 1, Status: consts.StatusEnabled},
			wantChannels:     []uint{5, 6},
			wantGroupIDs:     []uint{1},
			allowedModels:    []string{"gpt-4o"},
			rejectedModels:   []string{"gpt-5"},
			wantCallSequence: []string{"settings", "owned-token", "user-group"},
		},
		{
			name: "empty token restrictions inherit group restrictions at expiry boundary",
			token: &models.Token{
				ID: 25, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: now,
			},
			group: &models.UserGroup{
				ID: 8, Status: consts.StatusEnabled, Models: `["claude-.*"]`,
				AllowedChannelIDs: datatypes.JSONSlice[uint]{7, 8},
			},
			wantChannels:     []uint{7, 8},
			wantGroupIDs:     []uint{8},
			allowedModels:    []string{"claude-3-7-sonnet"},
			rejectedModels:   []string{"gpt-4o"},
			wantCallSequence: []string{"settings", "owned-token", "user-group"},
		},
		{
			name: "disabled flag on default group is ignored",
			token: &models.Token{
				ID: 26, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1,
			},
			group:            &models.UserGroup{ID: models.DefaultUserGroupID, Status: consts.StatusDisabled},
			wantGroupIDs:     []uint{models.DefaultUserGroupID},
			allowedModels:    []string{"gpt-4o"},
			wantCallSequence: []string{"settings", "owned-token", "user-group"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			settings := &fakeMarketplaceEnabledFinder{enabled: true, calls: &calls}
			tokens := &fakeMarketplaceTokenFinder{owned: test.token, calls: &calls}
			groups := &fakeMarketplaceUserGroupFinder{group: test.group, calls: &calls}
			gate := MarketplaceAccessGate{
				Settings:   settings,
				Tokens:     tokens,
				UserGroups: groups,
				Now:        func() time.Time { return time.Unix(now, 0) },
			}

			viewer, err := gate.RequireUser(userMarketplaceContext(t, 7), test.token.ID)
			if err != nil {
				t.Fatalf("RequireUser() error = %v", err)
			}
			if viewer.Token != test.token {
				t.Fatalf("viewer.Token = %p, want original token %p", viewer.Token, test.token)
			}
			if viewer.UserID != 7 || viewer.BYOKOnly != test.wantBYOKOnly {
				t.Fatalf("viewer identity/BYOK = (%d, %v), want (7, %v)", viewer.UserID, viewer.BYOKOnly, test.wantBYOKOnly)
			}
			if !reflect.DeepEqual(viewer.GroupIDs, test.wantGroupIDs) {
				t.Fatalf("GroupIDs = %v, want %v", viewer.GroupIDs, test.wantGroupIDs)
			}
			if viewer.GroupID != test.group.ID {
				t.Fatalf("GroupID = %d, want %d", viewer.GroupID, test.group.ID)
			}
			wantTokenChannels := normalizeMarketplaceChannelIDs(test.token.AllowedChannelIDs)
			if !reflect.DeepEqual(viewer.TokenAllowedChannelIDs, wantTokenChannels) {
				t.Fatalf("TokenAllowedChannelIDs = %v, want %v", viewer.TokenAllowedChannelIDs, wantTokenChannels)
			}
			wantGroupChannels := normalizeMarketplaceChannelIDs(test.group.AllowedChannelIDs)
			if !reflect.DeepEqual(viewer.GroupAllowedChannelIDs, wantGroupChannels) {
				t.Fatalf("GroupAllowedChannelIDs = %v, want %v", viewer.GroupAllowedChannelIDs, wantGroupChannels)
			}
			if !reflect.DeepEqual(viewer.AllowedChannelIDs, test.wantChannels) {
				t.Fatalf("AllowedChannelIDs = %v, want %v", viewer.AllowedChannelIDs, test.wantChannels)
			}
			for _, model := range test.allowedModels {
				if !viewer.AllowedModels.Allows(model) {
					t.Errorf("AllowedModels.Allows(%q) = false, want true", model)
				}
			}
			for _, model := range test.rejectedModels {
				if viewer.AllowedModels.Allows(model) {
					t.Errorf("AllowedModels.Allows(%q) = true, want false", model)
				}
			}
			if !reflect.DeepEqual(calls, test.wantCallSequence) {
				t.Fatalf("call sequence = %v, want %v", calls, test.wantCallSequence)
			}
			if tokens.ownedTokenID != test.token.ID || tokens.ownedUserID != 7 {
				t.Fatalf("FindOwned arguments = (%d, %d), want (%d, 7)", tokens.ownedTokenID, tokens.ownedUserID, test.token.ID)
			}
			if groups.userID != test.token.UserID {
				t.Fatalf("FindIdentityAndAuthorizationForUser userID = %d, want token owner %d", groups.userID, test.token.UserID)
			}
		})
	}
}

func TestMarketplaceGatePreservesGroupIdentityWhileBorrowingAuthorization(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.UserGroup{}))
	defaultGroup := models.UserGroup{
		ID: models.DefaultUserGroupID, Name: "default", Status: consts.StatusEnabled,
		Models: `["default-model"]`, AllowedChannelIDs: datatypes.JSONSlice[uint]{10},
	}
	assignedGroup := models.UserGroup{
		ID: 2, Name: "assigned", Status: consts.StatusEnabled,
		Models: `["assigned-model"]`, AllowedChannelIDs: datatypes.JSONSlice[uint]{20},
	}
	require.NoError(t, db.Create(&[]models.UserGroup{defaultGroup, assignedGroup}).Error)
	users := []models.User{
		{ID: 7, Username: "assigned-user", GroupID: assignedGroup.ID},
		{ID: 8, Username: "zero-group-user", GroupID: 0},
		{ID: 9, Username: "dangling-group-user", GroupID: 999999},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Model(&models.User{}).
		Where("id = ?", 8).
		UpdateColumn("group_id", 0).Error)
	var persistedZeroGroup models.User
	require.NoError(t, db.Select("group_id").First(&persistedZeroGroup, 8).Error)
	require.Zero(t, persistedZeroGroup.GroupID)

	application := app.NewApplication()
	application.SetCoreDB(db)
	c := userMarketplaceContext(t, 7)
	c.App = application
	gate := MarketplaceAccessGate{UserGroups: coreMarketplaceUserGroupFinder{}}
	tests := []struct {
		name         string
		userID       uint
		wantIdentity uint
		wantModels   []string
		wantChannels []uint
	}{
		{name: "assigned", userID: 7, wantIdentity: 2, wantModels: []string{"assigned-model"}, wantChannels: []uint{20}},
		{name: "zero group", userID: 8, wantIdentity: 1, wantModels: []string{"default-model"}, wantChannels: []uint{10}},
		{name: "missing user", userID: 404, wantIdentity: 1, wantModels: []string{"default-model"}, wantChannels: []uint{10}},
		{name: "system token", userID: 0, wantIdentity: 1, wantModels: []string{"default-model"}, wantChannels: []uint{10}},
		{name: "dangling group", userID: 9, wantIdentity: 999999, wantModels: []string{"default-model"}, wantChannels: []uint{10}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := &models.Token{
				ID: uint(100 + i), UserID: test.userID, Status: consts.StatusEnabled, ExpiredAt: -1,
			}
			viewer, gateErr := gate.viewerForToken(c, token)
			require.NoError(t, gateErr)
			require.Equal(t, test.wantIdentity, viewer.GroupID)
			require.Equal(t, []uint{test.wantIdentity}, viewer.GroupIDs)
			plannerUser := marketplaceRelayUserInfo(viewer)
			require.Equal(t, test.wantIdentity, plannerUser.GroupID)
			require.Equal(t, test.wantModels, plannerUser.GroupModels)
			require.Equal(t, test.wantChannels, plannerUser.GroupAllowedChannelIDs)
		})
	}
}

func TestAccessGateDisabledMarketplaceDoesNotQueryDatabase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.Setting{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Setting{
		Key: consts.SettingKeyModelMarketplaceEnabled, Value: "false",
	}).Error; err != nil {
		t.Fatal(err)
	}

	application := app.NewApplication()
	application.SetCoreDB(db)
	application.GetMasterSettings().Replace(map[string]string{
		consts.SettingKeyModelMarketplaceEnabled: "false",
	})
	c := userMarketplaceContext(t, 7)
	c.App = application
	tokens := &fakeMarketplaceTokenFinder{}
	groups := &fakeMarketplaceUserGroupFinder{}
	gate := NewMarketplaceAccessGate()
	gate.Tokens = tokens
	gate.UserGroups = groups

	queryCount := 0
	callbackName := "test:disabled_marketplace_query_count"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	_, err = gate.RequireUser(c, 23)
	requireMarketplaceAPIError(t, err, http.StatusNotFound, "", consts.ErrNotFound)
	if queryCount != 0 || tokens.ownedCount != 0 || groups.count != 0 {
		t.Fatalf("disabled gate dependencies = SQL:%d token:%d group:%d, want 0/0/0",
			queryCount, tokens.ownedCount, groups.count)
	}
}

func TestMarketplaceViewerCannotBeSerialized(t *testing.T) {
	const secret = "sk-marketplace-must-not-leak"
	viewer := MarketplaceViewer{
		UserID:            7,
		Token:             &models.Token{ID: 23, UserID: 7, Key: secret},
		GroupIDs:          []uint{models.DefaultUserGroupID, 9},
		AllowedChannelIDs: []uint{11, 12},
		AllowedModels: MarketplaceModelWhitelist{
			TokenPatterns: []string{"secret-token-pattern"},
			GroupPatterns: []string{"secret-group-pattern"},
		},
		BYOKOnly:    true,
		AdminGlobal: true,
	}

	encoded, err := json.Marshal(viewer)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "{}" {
		t.Fatalf("json.Marshal(MarketplaceViewer) = %s, want {} without %q or authorization internals", encoded, secret)
	}
}

func TestAccessGateRequireUserFailureMatrix(t *testing.T) {
	const now = int64(1_800_000_000)
	tests := []struct {
		name             string
		enabled          bool
		tokenID          uint
		token            *models.Token
		tokenErr         error
		group            *models.UserGroup
		groupErr         error
		wantStatus       int
		wantMessage      string
		wantCode         string
		wantSettingsCall int
		wantTokenCall    int
		wantGroupCall    int
	}{
		{
			name:       "feature disabled is an early 404",
			wantStatus: http.StatusNotFound, wantMessage: consts.ErrNotFound,
			wantSettingsCall: 1,
		},
		{
			name:    "cross user token is hidden as 404",
			enabled: true, tokenID: 23, tokenErr: gorm.ErrRecordNotFound,
			wantStatus: http.StatusNotFound, wantMessage: consts.ErrNotFound,
			wantSettingsCall: 1, wantTokenCall: 1,
		},
		{
			name:    "disabled token returns stable 422",
			enabled: true, tokenID: 23,
			token:      &models.Token{ID: 23, UserID: 7, Status: consts.StatusDisabled, ExpiredAt: -1},
			wantStatus: http.StatusUnprocessableEntity, wantMessage: consts.ErrTokenDisabled,
			wantCode: "marketplace_token_disabled", wantSettingsCall: 1, wantTokenCall: 1,
		},
		{
			name:    "expired token returns stable 422",
			enabled: true, tokenID: 23,
			token:      &models.Token{ID: 23, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: now - 1},
			wantStatus: http.StatusUnprocessableEntity, wantMessage: consts.ErrTokenExpired,
			wantCode: "marketplace_token_expired", wantSettingsCall: 1, wantTokenCall: 1,
		},
		{
			name:    "malformed token model whitelist fails before group lookup",
			enabled: true, tokenID: 23,
			token:      &models.Token{ID: 23, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1, Models: `{"bad":true}`},
			wantStatus: http.StatusInternalServerError, wantMessage: "parse marketplace token models",
			wantSettingsCall: 1, wantTokenCall: 1,
		},
		{
			name:    "missing user group is an internal identity error",
			enabled: true, tokenID: 23,
			token:      &models.Token{ID: 23, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1},
			groupErr:   gorm.ErrRecordNotFound,
			wantStatus: http.StatusInternalServerError, wantMessage: "find marketplace user group",
			wantSettingsCall: 1, wantTokenCall: 1, wantGroupCall: 1,
		},
		{
			name:    "disabled user group returns stable 422",
			enabled: true, tokenID: 23,
			token:      &models.Token{ID: 23, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1},
			group:      &models.UserGroup{ID: 9, Status: consts.StatusDisabled},
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "user group disabled",
			wantCode: "marketplace_user_group_disabled", wantSettingsCall: 1, wantTokenCall: 1, wantGroupCall: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			settings := &fakeMarketplaceEnabledFinder{enabled: test.enabled, calls: &calls}
			tokens := &fakeMarketplaceTokenFinder{owned: test.token, ownedErr: test.tokenErr, calls: &calls}
			groups := &fakeMarketplaceUserGroupFinder{group: test.group, err: test.groupErr, calls: &calls}
			gate := MarketplaceAccessGate{
				Settings:   settings,
				Tokens:     tokens,
				UserGroups: groups,
				Now:        func() time.Time { return time.Unix(now, 0) },
			}

			_, err := gate.RequireUser(userMarketplaceContext(t, 7), test.tokenID)
			requireMarketplaceAPIError(t, err, test.wantStatus, test.wantCode, test.wantMessage)
			if settings.count != test.wantSettingsCall || tokens.ownedCount != test.wantTokenCall || groups.count != test.wantGroupCall {
				t.Fatalf("dependency calls = settings:%d token:%d group:%d, want %d/%d/%d (sequence %v)",
					settings.count, tokens.ownedCount, groups.count,
					test.wantSettingsCall, test.wantTokenCall, test.wantGroupCall, calls)
			}
		})
	}
}

func TestAccessGateRequireUserBoundaryMatrix(t *testing.T) {
	t.Run("nil and empty restrictions mean unrestricted", func(t *testing.T) {
		gate := MarketplaceAccessGate{
			Settings: &fakeMarketplaceEnabledFinder{enabled: true},
			Tokens: &fakeMarketplaceTokenFinder{owned: &models.Token{
				ID: 1, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: 0, Models: `null`,
			}},
			UserGroups: &fakeMarketplaceUserGroupFinder{group: &models.UserGroup{
				ID: 1, Status: consts.StatusEnabled, Models: `[]`,
			}},
		}
		viewer, err := gate.RequireUser(userMarketplaceContext(t, 7), 1)
		if err != nil {
			t.Fatalf("RequireUser() error = %v", err)
		}
		if !viewer.AllowedModels.Allows("any-model") || len(viewer.AllowedChannelIDs) != 0 {
			t.Fatalf("empty restrictions should be unrestricted: %+v", viewer)
		}
	})

	t.Run("disjoint channel whitelists deny every platform channel", func(t *testing.T) {
		gate := MarketplaceAccessGate{
			Settings: &fakeMarketplaceEnabledFinder{enabled: true},
			Tokens: &fakeMarketplaceTokenFinder{owned: &models.Token{
				ID: 1, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1,
				AllowedChannelIDs: datatypes.JSONSlice[uint]{2},
			}},
			UserGroups: &fakeMarketplaceUserGroupFinder{group: &models.UserGroup{
				ID: 1, Status: consts.StatusEnabled,
				AllowedChannelIDs: datatypes.JSONSlice[uint]{3},
			}},
		}
		viewer, err := gate.RequireUser(userMarketplaceContext(t, 7), 1)
		if err != nil {
			t.Fatalf("RequireUser() error = %v", err)
		}
		if viewer.AllowedChannelIDs == nil || len(viewer.AllowedChannelIDs) != 0 {
			t.Fatalf("disjoint AllowedChannelIDs = %#v, want non-nil empty deny-all whitelist", viewer.AllowedChannelIDs)
		}
		if viewer.AllowsChannel(2) || viewer.AllowsChannel(3) {
			t.Fatalf("disjoint whitelist allowed a channel: %#v", viewer.AllowedChannelIDs)
		}
	})

	t.Run("zero token ID still uses the owned query and returns 404", func(t *testing.T) {
		tokens := &fakeMarketplaceTokenFinder{ownedErr: gorm.ErrRecordNotFound}
		gate := MarketplaceAccessGate{
			Settings:   &fakeMarketplaceEnabledFinder{enabled: true},
			Tokens:     tokens,
			UserGroups: &fakeMarketplaceUserGroupFinder{},
		}
		_, err := gate.RequireUser(userMarketplaceContext(t, 7), 0)
		requireMarketplaceAPIError(t, err, http.StatusNotFound, "", consts.ErrNotFound)
		if tokens.ownedCount != 1 || tokens.ownedTokenID != 0 || tokens.ownedUserID != 7 {
			t.Fatalf("FindOwned calls/args = %d (%d, %d), want 1 (0, 7)", tokens.ownedCount, tokens.ownedTokenID, tokens.ownedUserID)
		}
	})

	t.Run("owned finder database failure is a generic 500", func(t *testing.T) {
		gate := MarketplaceAccessGate{
			Settings:   &fakeMarketplaceEnabledFinder{enabled: true},
			Tokens:     &fakeMarketplaceTokenFinder{ownedErr: errors.New("database includes secret details")},
			UserGroups: &fakeMarketplaceUserGroupFinder{},
		}
		_, err := gate.RequireUser(userMarketplaceContext(t, 7), 1)
		requireMarketplaceAPIError(t, err, http.StatusInternalServerError, "", "find owned marketplace token")
	})
}

func TestAccessGateRequireAdminSuccessAndSecurity(t *testing.T) {
	t.Run("global admin view bypasses disabled setting and all finders", func(t *testing.T) {
		settings := &fakeMarketplaceEnabledFinder{enabled: false}
		tokens := &fakeMarketplaceTokenFinder{}
		groups := &fakeMarketplaceUserGroupFinder{}
		gate := MarketplaceAccessGate{Settings: settings, Tokens: tokens, UserGroups: groups}

		viewer, err := gate.RequireAdmin(adminMarketplaceContext(t, 99), nil)
		if err != nil {
			t.Fatalf("RequireAdmin() error = %v", err)
		}
		if viewer.Token != nil || viewer.UserID != 0 || !viewer.AdminGlobal {
			t.Fatalf("global admin viewer = %+v, want explicit admin-global viewer", viewer)
		}
		if settings.count != 0 || tokens.findCount != 0 || groups.count != 0 {
			t.Fatalf("admin global dependencies called: settings=%d token=%d group=%d", settings.count, tokens.findCount, groups.count)
		}
	})

	t.Run("admin may preview another users token with its owner context", func(t *testing.T) {
		calls := []string{}
		tokenID := uint(23)
		token := &models.Token{
			ID: tokenID, UserID: 7, Status: consts.StatusEnabled, ExpiredAt: -1,
			Models: `["gpt-.*"]`, AllowedChannelIDs: datatypes.JSONSlice[uint]{2},
		}
		tokens := &fakeMarketplaceTokenFinder{found: token, calls: &calls}
		groups := &fakeMarketplaceUserGroupFinder{
			group: &models.UserGroup{ID: 9, Status: consts.StatusEnabled, Models: `["gpt-4o"]`},
			calls: &calls,
		}
		gate := MarketplaceAccessGate{
			Settings: &fakeMarketplaceEnabledFinder{enabled: false, calls: &calls},
			Tokens:   tokens, UserGroups: groups,
		}

		viewer, err := gate.RequireAdmin(adminMarketplaceContext(t, 99), &tokenID)
		if err != nil {
			t.Fatalf("RequireAdmin() error = %v", err)
		}
		if viewer.AdminGlobal || viewer.UserID != token.UserID || viewer.Token != token || !viewer.AllowedModels.Allows("gpt-4o") || viewer.AllowedModels.Allows("gpt-5") {
			t.Fatalf("token preview viewer = %+v", viewer)
		}
		if !reflect.DeepEqual(calls, []string{"admin-token", "user-group"}) {
			t.Fatalf("call sequence = %v, want admin-token then user-group without settings", calls)
		}
	})

	t.Run("system token preview with zero user id is not admin global", func(t *testing.T) {
		tokenID := uint(24)
		token := &models.Token{ID: tokenID, UserID: 0, Status: consts.StatusEnabled, ExpiredAt: -1}
		gate := MarketplaceAccessGate{
			Tokens: &fakeMarketplaceTokenFinder{found: token},
			UserGroups: &fakeMarketplaceUserGroupFinder{group: &models.UserGroup{
				ID: models.DefaultUserGroupID, Status: consts.StatusEnabled,
			}},
		}

		viewer, err := gate.RequireAdmin(adminMarketplaceContext(t, 99), &tokenID)
		require.NoError(t, err)
		require.Same(t, token, viewer.Token)
		require.Zero(t, viewer.UserID)
		require.False(t, viewer.AdminGlobal)
	})

	t.Run("ordinary user is rejected before optional token lookup", func(t *testing.T) {
		tokenID := uint(23)
		tokens := &fakeMarketplaceTokenFinder{found: &models.Token{ID: tokenID, UserID: 8}}
		gate := MarketplaceAccessGate{
			Settings:   &fakeMarketplaceEnabledFinder{enabled: true},
			Tokens:     tokens,
			UserGroups: &fakeMarketplaceUserGroupFinder{},
		}
		_, err := gate.RequireAdmin(userMarketplaceContext(t, 7), &tokenID)
		requireMarketplaceAPIError(t, err, http.StatusForbidden, "", consts.ErrAdminOnly)
		if tokens.findCount != 0 {
			t.Fatalf("admin token finder called %d times before admin authorization", tokens.findCount)
		}
	})
}

type fakeMarketplaceEnabledFinder struct {
	enabled bool
	count   int
	calls   *[]string
}

func (f *fakeMarketplaceEnabledFinder) Enabled(*app.Context) bool {
	f.count++
	appendMarketplaceCall(f.calls, "settings")
	return f.enabled
}

type fakeMarketplaceTokenFinder struct {
	owned        *models.Token
	ownedErr     error
	found        *models.Token
	findErr      error
	ownedCount   int
	findCount    int
	ownedTokenID uint
	ownedUserID  uint
	findTokenID  uint
	calls        *[]string
}

func (f *fakeMarketplaceTokenFinder) FindOwned(_ *app.Context, tokenID, userID uint) (*models.Token, error) {
	f.ownedCount++
	f.ownedTokenID = tokenID
	f.ownedUserID = userID
	appendMarketplaceCall(f.calls, "owned-token")
	return f.owned, f.ownedErr
}

func (f *fakeMarketplaceTokenFinder) FindByID(_ *app.Context, tokenID uint) (*models.Token, error) {
	f.findCount++
	f.findTokenID = tokenID
	appendMarketplaceCall(f.calls, "admin-token")
	return f.found, f.findErr
}

type fakeMarketplaceUserGroupFinder struct {
	group           *models.UserGroup
	identityGroupID uint
	err             error
	count           int
	userID          uint
	calls           *[]string
}

func (f *fakeMarketplaceUserGroupFinder) FindIdentityAndAuthorizationForUser(
	_ *app.Context,
	userID uint,
) (*dao.UserGroupIdentityAndAuthorization, error) {
	f.count++
	f.userID = userID
	appendMarketplaceCall(f.calls, "user-group")
	if f.group == nil {
		return nil, f.err
	}
	identityGroupID := f.identityGroupID
	if identityGroupID == 0 {
		identityGroupID = f.group.ID
	}
	return &dao.UserGroupIdentityAndAuthorization{
		IdentityGroupID:    identityGroupID,
		AuthorizationGroup: *f.group,
	}, f.err
}

func appendMarketplaceCall(calls *[]string, call string) {
	if calls != nil {
		*calls = append(*calls, call)
	}
}

func userMarketplaceContext(t *testing.T, userID uint) *app.Context {
	t.Helper()
	return marketplaceContext(t, &middleware.RequestScope{UserID: userID})
}

func adminMarketplaceContext(t *testing.T, userID uint) *app.Context {
	t.Helper()
	return marketplaceContext(t, &middleware.RequestScope{IsAdmin: true, UserID: userID})
}

func marketplaceContext(t *testing.T, scope *middleware.RequestScope) *app.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set(consts.CtxKeyRequestScope, scope)
	return &app.Context{Context: c, OwnerContext: t.Context()}
}

func requireMarketplaceAPIError(t *testing.T, err error, status int, code, message string) {
	t.Helper()
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want *api.APIError", err, err)
	}
	if apiErr.Status != status || apiErr.Code != code || apiErr.Message != message {
		t.Fatalf("API error = {status:%d code:%q message:%q}, want {%d %q %q}",
			apiErr.Status, apiErr.Code, apiErr.Message, status, code, message)
	}
}
