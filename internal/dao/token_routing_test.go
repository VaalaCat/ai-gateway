package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestDeleteTokenWithRoutings(t *testing.T) {
	ctx, db := setupAdminContext(t)
	token := models.Token{UserID: 1, Key: "sk-delete-routing", Name: "target"}
	other := models.Token{UserID: 1, Key: "sk-delete-routing-other", Name: "other"}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&other).Error)
	for _, routing := range []models.ModelRouting{
		{Name: "one", Scope: models.RoutingScopeToken, TokenID: token.ID, Members: "[]"},
		{Name: "two", Scope: models.RoutingScopeToken, TokenID: token.ID, Members: "[]"},
		{Name: "kept", Scope: models.RoutingScopeToken, TokenID: other.ID, Members: "[]"},
	} {
		require.NoError(t, db.Create(&routing).Error)
	}

	deleted, err := NewAdminMutation(ctx).Token().DeleteWithRoutings(token.ID)
	require.NoError(t, err)
	require.Len(t, deleted, 2)
	var tokenCount, targetRoutingCount, otherRoutingCount int64
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Count(&tokenCount).Error)
	require.NoError(t, db.Model(&models.ModelRouting{}).Where("token_id = ?", token.ID).Count(&targetRoutingCount).Error)
	require.NoError(t, db.Model(&models.ModelRouting{}).Where("token_id = ?", other.ID).Count(&otherRoutingCount).Error)
	require.Zero(t, tokenCount)
	require.Zero(t, targetRoutingCount)
	require.EqualValues(t, 1, otherRoutingCount)
}

func TestDeleteTokenWithRoutingsRollsBack(t *testing.T) {
	ctx, db := setupAdminContext(t)
	token := models.Token{UserID: 1, Key: "sk-delete-routing-rollback", Name: "target"}
	require.NoError(t, db.Create(&token).Error)
	routing := models.ModelRouting{Name: "one", Scope: models.RoutingScopeToken, TokenID: token.ID, Members: "[]"}
	require.NoError(t, db.Create(&routing).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER block_token_delete BEFORE DELETE ON tokens BEGIN SELECT RAISE(ABORT, 'blocked'); END`).Error)

	_, err := NewAdminMutation(ctx).Token().DeleteWithRoutings(token.ID)
	require.Error(t, err)
	var tokenCount, routingCount int64
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Count(&tokenCount).Error)
	require.NoError(t, db.Model(&models.ModelRouting{}).Where("token_id = ?", token.ID).Count(&routingCount).Error)
	require.EqualValues(t, 1, tokenCount)
	require.EqualValues(t, 1, routingCount)
}

func TestDeleteByTokenHandlesEmptySet(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, NewAdminMutation(ctx).ModelRouting().DeleteByToken(999))
	var count int64
	require.NoError(t, db.Model(&models.ModelRouting{}).Count(&count).Error)
	require.Zero(t, count)
}
