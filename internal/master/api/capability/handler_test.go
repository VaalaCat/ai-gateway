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
	application := app.NewApplication()
	application.SetCoreDB(db)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: 7})
	return &app.Context{Context: ginCtx, App: application, OwnerContext: t.Context()}, db
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

			got, err := (&Handler{}).Get(ctx, api.EmptyRequest{})
			require.NoError(t, err)
			raw, err := json.Marshal(got)
			require.NoError(t, err)
			if tc.wantAvailable {
				require.NotNil(t, got.ModelMarketplace)
				require.True(t, *got.ModelMarketplace)
				require.JSONEq(t, `{"token":{"can_edit_model_whitelist":`+boolJSON(got.Token.CanEditModelWhitelist)+`},"model_marketplace":true}`, string(raw))
				return
			}
			require.Nil(t, got.ModelMarketplace)
			require.NotContains(t, string(raw), "model_marketplace")
		})
	}
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
			got, err := (&Handler{}).Get(ctx, api.EmptyRequest{})
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Token.CanEditModelWhitelist)
		})
	}
}

func stringPointer(value string) *string { return &value }
