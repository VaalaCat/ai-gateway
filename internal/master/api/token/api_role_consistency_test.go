package token

import (
	"context"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenAPIRoleUpdateRejectsOwnerTransferBetweenReadAndWrite(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	owner := models.User{Username: "original-owner", Role: consts.RoleUser, GroupID: 7}
	newOwner := models.User{Username: "new-owner", Role: consts.RoleUser, GroupID: 8}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&newOwner).Error)
	token := models.Token{Name: "transferred", Key: "sk-transferred", UserID: owner.ID, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	role := seedTokenAPIRoles(t, db, models.Role{Key: "owner-role", Name: "Owner role", Status: consts.StatusEnabled})[0]
	require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: owner.ID, RoleID: role.ID}).Error)
	setScope(ctx, false, owner.ID)
	registerTokenUpdateInterruption(t, db, func(tx *gorm.DB) error {
		return tx.Exec("UPDATE tokens SET user_id = ? WHERE id = ?", newOwner.ID, token.ID).Error
	})
	req := UpdateRequest{ID: strconv.FormatUint(uint64(token.ID), 10)}
	req.SetBodyMap(map[string]any{"api_role_mode": "explicit", "api_role_ids": []any{float64(role.ID)}})

	_, err := h.Update(ctx, req)
	requireAPIStatus(t, err, 404)
	var got models.Token
	require.NoError(t, db.First(&got, token.ID).Error)
	require.Equal(t, owner.ID, got.UserID)
	require.Equal(t, models.APIRoleModeInherit, got.APIRoleMode)
	require.Empty(t, tokenBindingRoleIDs(t, db, token.ID))
}

func TestTokenAPIRoleUpdateRejectsZeroRowAfterConcurrentDelete(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	token := seedToken(t, db, nil)
	role := seedTokenAPIRoles(t, db, models.Role{Key: "admin-role", Name: "Admin role", Status: consts.StatusEnabled})[0]
	setScope(ctx, true, 99)
	registerTokenUpdateInterruption(t, db, func(tx *gorm.DB) error {
		return tx.Exec("DELETE FROM tokens WHERE id = ?", token.ID).Error
	})
	req := UpdateRequest{ID: strconv.FormatUint(uint64(token.ID), 10)}
	req.SetBodyMap(map[string]any{"api_role_mode": "explicit", "api_role_ids": []any{float64(role.ID)}})

	_, err := h.Update(ctx, req)
	requireAPIStatus(t, err, 404)
	var got models.Token
	require.NoError(t, db.First(&got, token.ID).Error)
	require.Equal(t, models.APIRoleModeInherit, got.APIRoleMode)
	require.Empty(t, tokenBindingRoleIDs(t, db, token.ID))
}

func TestTokenUpdateWithoutAPIRoleFieldsPreservesExplicitBindingsAndPublishes(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	token := seedToken(t, db, nil)
	role := seedTokenAPIRoles(t, db, models.Role{Key: "preserved", Name: "Preserved", Status: consts.StatusEnabled})[0]
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Update("api_role_mode", models.APIRoleModeExplicit).Error)
	require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error)
	setScope(ctx, true, 99)
	var published []models.Token
	_, err := events.Subscribe(ctx.GetBus(), events.TokenUpdateTopic, func(_ context.Context, token models.Token) error {
		published = append(published, token)
		return nil
	})
	require.NoError(t, err)
	req := UpdateRequest{ID: strconv.FormatUint(uint64(token.ID), 10)}
	req.SetBodyMap(map[string]any{"name": "renamed", "status": float64(consts.StatusDisabled)})

	got, err := h.Update(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
	require.Equal(t, consts.StatusDisabled, got.Status)
	require.Equal(t, models.APIRoleModeExplicit, got.APIRoleMode)
	require.Equal(t, []uint{role.ID}, tokenBindingRoleIDs(t, db, token.ID))
	require.Len(t, published, 1)
	require.Equal(t, got, published[0])
}

func registerTokenUpdateInterruption(t *testing.T, db *gorm.DB, interrupt func(*gorm.DB) error) {
	t.Helper()
	callbackName := "test:interrupt_token_role_update"
	fired := false
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if fired || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "tokens" {
			return
		}
		fired = true
		if err := interrupt(tx.Session(&gorm.Session{NewDB: true, SkipHooks: true})); err != nil {
			tx.AddError(err)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })
}
