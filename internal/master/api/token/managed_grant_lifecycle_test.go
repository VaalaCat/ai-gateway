package token

import (
	"errors"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	apiaccessgrant "github.com/VaalaCat/ai-gateway/internal/master/api/api_access_grant"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenExplicitCustomRoleUpdatePreservesManagedGrant(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	token := seedToken(t, db, nil)
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Update("api_role_mode", models.APIRoleModeExplicit).Error)
	service, _ := seedManagedGrantService(t, db, "preserve")
	grantTokenService(t, ctx, token.ID, service.ID)
	custom := seedTokenAPIRoles(t, db, models.Role{Key: "ordinary", Name: "Ordinary", Status: consts.StatusEnabled})[0]
	setScope(ctx, true, 99)
	req := UpdateRequest{ID: strconv.FormatUint(uint64(token.ID), 10)}
	req.SetBodyMap(map[string]any{"api_role_mode": "explicit", "api_role_ids": []any{float64(custom.ID)}})

	_, err := h.Update(ctx, req)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint{custom.ID, managedTokenRoleID(t, db, token.ID)}, tokenBindingRoleIDs(t, db, token.ID))
}

func TestTokenInheritDeletesManagedGrantAndCannotReviveStaleServices(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	token := seedToken(t, db, nil)
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Update("api_role_mode", models.APIRoleModeExplicit).Error)
	serviceA, _ := seedManagedGrantService(t, db, "stale-a")
	serviceB, _ := seedManagedGrantService(t, db, "stale-b")
	writer := apiaccessgrant.GrantWriter{}
	principal := apiaccessgrant.PrincipalRef{Type: models.APIPrincipalToken, ID: token.ID}
	_, err := writer.Replace(dao.NewContext(ctx.App), principal, serviceA.ID, apiaccessgrant.GrantScopeService, nil)
	require.NoError(t, err)
	_, err = writer.Replace(dao.NewContext(ctx.App), principal, serviceB.ID, apiaccessgrant.GrantScopeService, nil)
	require.NoError(t, err)
	setScope(ctx, true, 99)
	req := UpdateRequest{ID: strconv.FormatUint(uint64(token.ID), 10)}
	req.SetBodyMap(map[string]any{"api_role_mode": "inherit", "api_role_ids": []any{}})

	_, err = h.Update(ctx, req)
	require.NoError(t, err)
	require.Zero(t, managedTokenRoleID(t, db, token.ID))
	require.Empty(t, tokenBindingRoleIDs(t, db, token.ID))

	req.SetBodyMap(map[string]any{"api_role_mode": "explicit", "api_role_ids": []any{}})
	_, err = h.Update(ctx, req)
	require.NoError(t, err)
	_, err = writer.Replace(dao.NewContext(ctx.App), principal, serviceA.ID, apiaccessgrant.GrantScopeService, nil)
	require.NoError(t, err)
	require.Equal(t, []uint{serviceA.ID}, managedTokenServicePermissions(t, db, token.ID))
}

func TestTokenInheritManagedGrantCleanupRollsBackOnDeleteFailure(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	token := seedToken(t, db, nil)
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Update("api_role_mode", models.APIRoleModeExplicit).Error)
	service, _ := seedManagedGrantService(t, db, "atomic")
	grantTokenService(t, ctx, token.ID, service.ID)
	callbackName := "test:fail_managed_grant_permission_delete"
	sentinel := errors.New("managed permission cleanup failed")
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "role_permissions" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(callbackName) })
	setScope(ctx, true, 99)
	req := UpdateRequest{ID: strconv.FormatUint(uint64(token.ID), 10)}
	req.SetBodyMap(map[string]any{"api_role_mode": "inherit", "api_role_ids": []any{}})

	_, err := h.Update(ctx, req)
	require.Error(t, err)
	var reloaded models.Token
	require.NoError(t, db.First(&reloaded, token.ID).Error)
	require.Equal(t, models.APIRoleModeExplicit, reloaded.APIRoleMode)
	require.NotZero(t, managedTokenRoleID(t, db, token.ID))
	require.Equal(t, []uint{service.ID}, managedTokenServicePermissions(t, db, token.ID))
}

func seedManagedGrantService(t *testing.T, db *gorm.DB, slug string) (models.APIService, models.APIRoute) {
	t.Helper()
	service := models.APIService{Slug: slug, Name: slug, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, db.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&route).Error)
	return service, route
}

func grantTokenService(t *testing.T, ctx *app.Context, tokenID, serviceID uint) {
	t.Helper()
	_, err := apiaccessgrant.GrantWriter{}.Replace(dao.NewContext(ctx.App), apiaccessgrant.PrincipalRef{Type: models.APIPrincipalToken, ID: tokenID}, serviceID, apiaccessgrant.GrantScopeService, nil)
	require.NoError(t, err)
}

func managedTokenRoleID(t *testing.T, db *gorm.DB, tokenID uint) uint {
	t.Helper()
	var role models.Role
	err := db.Where("`key` = ?", models.ManagedAPIRoleKey(models.APIPrincipalToken, tokenID)).First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0
	}
	require.NoError(t, err)
	return role.ID
}

func managedTokenServicePermissions(t *testing.T, db *gorm.DB, tokenID uint) []uint {
	t.Helper()
	roleID := managedTokenRoleID(t, db, tokenID)
	if roleID == 0 {
		return nil
	}
	var ids []uint
	require.NoError(t, db.Table("permissions").Select("permissions.resource_id").
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Where("role_permissions.role_id = ? AND permissions.resource = ?", roleID, models.APIResourceService).
		Order("permissions.resource_id ASC").Pluck("permissions.resource_id", &ids).Error)
	return ids
}
