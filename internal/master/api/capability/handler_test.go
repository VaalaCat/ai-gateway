package capability

import (
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
	application.SetDB(db)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: 7})
	return &app.Context{Context: ginCtx, App: application, OwnerContext: t.Context()}, db
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
