package capability

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func capabilityTestContext(t *testing.T, isAdmin bool) (*app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	require.NoError(t, db.AutoMigrate(&models.Role{}, &models.Permission{}, &models.RolePermission{}, &models.RoleBinding{}))
	require.NoError(t, models.SeedDefaultUserGroup(db))
	require.NoError(t, db.Create(&models.User{ID: 7, Username: "capability-user", Role: consts.RoleUser, GroupID: 1}).Error)
	application := app.NewApplication()
	application.SetCoreDB(db)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: 7})
	return &app.Context{
		Context: ginCtx, App: application, UserInfo: &app.UserInfo{UserID: 7}, OwnerContext: t.Context(),
	}, db
}

func TestCapabilities_ModelMarketplace(t *testing.T) {
	for _, tc := range []struct {
		name          string
		admin         bool
		setting       *string
		wantAvailable bool
	}{
		{name: "admin always manages marketplace", admin: true, wantAvailable: true},
		{name: "user sees enabled marketplace", setting: stringPointer("true"), wantAvailable: true},
		{name: "user default omits marketplace"},
		{name: "user explicitly disabled omits marketplace", setting: stringPointer("false")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := capabilityTestContext(t, tc.admin)
			if tc.setting != nil {
				require.NoError(t, db.Create(&models.Setting{Key: consts.SettingKeyModelMarketplaceEnabled, Value: *tc.setting}).Error)
			}

			got, err := (&Handler{App: ctx.App}).Get(ctx, api.EmptyRequest{})
			require.NoError(t, err)
			raw, err := json.Marshal(got)
			require.NoError(t, err)
			if tc.wantAvailable {
				require.NotNil(t, got.ModelMarketplace)
				require.True(t, *got.ModelMarketplace)
				require.JSONEq(t, `{"token":{"can_edit_model_whitelist":`+boolJSON(got.Token.CanEditModelWhitelist)+`},"model_marketplace":true,"generic_api":`+genericAPIJSON(tc.admin)+`}`, string(raw))
				return
			}
			require.Nil(t, got.ModelMarketplace)
			require.NotContains(t, string(raw), "model_marketplace")
		})
	}
}

func genericAPIJSON(admin bool) string {
	if admin {
		return `{"services":true,"access":true,"logs":true,"websocket":true,"service_actions":{"create":true,"manage_all":true,"manage_ids":[]}}`
	}
	return `{"services":false,"access":false,"logs":true,"websocket":false,"service_actions":{"create":false,"manage_all":false,"manage_ids":[]}}`
}

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestCapabilities_TokenModelWhitelist(t *testing.T) {
	for _, tc := range []struct {
		name    string
		admin   bool
		setting *string
		want    bool
	}{
		{name: "admin always allowed", admin: true, want: true},
		{name: "user default denied", want: false},
		{name: "user enabled", setting: stringPointer("true"), want: true},
		{name: "user explicitly disabled", setting: stringPointer("false"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := capabilityTestContext(t, tc.admin)
			if tc.setting != nil {
				require.NoError(t, db.Create(&models.Setting{Key: consts.SettingKeyTokenModelWhitelistSelfService, Value: *tc.setting}).Error)
			}
			got, err := (&Handler{App: ctx.App}).Get(ctx, api.EmptyRequest{})
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Token.CanEditModelWhitelist)
		})
	}
}

// behavior change: the personal log surface is available to every user, while
// Generic API management capabilities remain administrator-only.
func TestCapabilities_GenericAPIControlPlaneIsAdminOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		admin bool
	}{
		{name: "ordinary user has no management surface"},
		{name: "administrator manages the full surface", admin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := capabilityTestContext(t, tc.admin)
			got, err := (&Handler{App: ctx.App}).Get(ctx, api.EmptyRequest{})
			require.NoError(t, err)
			require.Equal(t, tc.admin, got.GenericAPI.Services)
			require.Equal(t, tc.admin, got.GenericAPI.Access)
			require.True(t, got.GenericAPI.Logs)
			require.Equal(t, tc.admin, got.GenericAPI.WebSocket)
			require.Equal(t, tc.admin, got.GenericAPI.ServiceActions.Create)
			require.Equal(t, tc.admin, got.GenericAPI.ServiceActions.ManageAll)
			require.Empty(t, got.GenericAPI.ServiceActions.ManageIDs)
		})
	}
}

func stringPointer(value string) *string { return &value }
