package script

import (
	"strconv"
	"testing"

	"net/http/httptest"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupScriptTest(t *testing.T) (*Handler, *app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ctx := &app.Context{Context: ginCtx, App: application, UserInfo: &app.UserInfo{UserID: 1, GroupID: 1, Role: 2}, OwnerContext: t.Context()}
	return &Handler{}, ctx, db
}

func boolPtr(b bool) *bool { return &b }

func TestCreate_Success(t *testing.T) {
	h, ctx, db := setupScriptTest(t)
	res, err := h.Create(ctx, CreateRequest{
		Name: "trim", Code: "function onRequest(c){ c.body.x=1 }", Enabled: boolPtr(true), Priority: 3,
		Scope: models.ScriptScope{ModelNames: []string{"gpt-4o"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "trim", res.Value.Name)

	var got models.AdminScript
	require.NoError(t, db.First(&got, res.Value.ID).Error)
	assert.Equal(t, []string{"gpt-4o"}, got.Scope.Data().ModelNames)
	assert.Equal(t, 3, got.Priority)
}

func TestCreate_DisabledHonored(t *testing.T) {
	h, ctx, db := setupScriptTest(t)
	res, err := h.Create(ctx, CreateRequest{
		Name: "off", Code: "function onRequest(c){}", Enabled: boolPtr(false),
	})
	require.NoError(t, err)
	assert.False(t, res.Value.Enabled)

	var got models.AdminScript
	require.NoError(t, db.First(&got, res.Value.ID).Error)
	assert.False(t, got.Enabled, "explicit enabled:false must persist")
}

func TestCreate_EnabledDefaultsTrueWhenOmitted(t *testing.T) {
	h, ctx, _ := setupScriptTest(t)
	res, err := h.Create(ctx, CreateRequest{Name: "deflt", Code: "function onRequest(c){}"})
	require.NoError(t, err)
	assert.True(t, res.Value.Enabled, "omitted enabled must default to true")
}

func TestCreate_CompileErrorRejected(t *testing.T) {
	h, ctx, _ := setupScriptTest(t)
	_, err := h.Create(ctx, CreateRequest{Name: "bad", Code: "function onRequest( {"})
	require.Error(t, err)
	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	assert.Equal(t, 400, apiErr.Status)
}

func TestUpdate_ScopeRoundTrip(t *testing.T) {
	h, ctx, db := setupScriptTest(t)
	created, err := h.Create(ctx, CreateRequest{Name: "s", Code: "function onRequest(c){}"})
	require.NoError(t, err)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(created.Value.ID), 10)}
	req.SetBodyMap(map[string]any{"scope": map[string]any{"channel_ids": []any{float64(5)}}})
	_, err = h.Update(ctx, req)
	require.NoError(t, err)

	var got models.AdminScript
	require.NoError(t, db.First(&got, created.Value.ID).Error)
	assert.Equal(t, []uint{5}, got.Scope.Data().ChannelIDs)
}
