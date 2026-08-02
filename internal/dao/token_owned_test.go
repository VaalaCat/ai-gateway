package dao

import (
	"reflect"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func TestTokenOwnedFindsWithOneIDAndUserIDQuery(t *testing.T) {
	ctx, db := setupAdminContext(t)
	owner := &models.User{Username: "marketplace-owner"}
	other := &models.User{Username: "marketplace-other"}
	if err := db.Create(owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	token := &models.Token{UserID: owner.ID, Key: "sk-marketplace-owned", Name: "marketplace", Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}

	queryCount := 0
	var querySQL string
	var queryVars []any
	callbackName := "test:marketplace_owned_query"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		queryCount++
		querySQL = tx.Statement.SQL.String()
		queryVars = append([]any(nil), tx.Statement.Vars...)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	got, err := NewAdminQuery(ctx).Token().FindOwned(token.ID, owner.ID)
	if err != nil {
		t.Fatalf("FindOwned(owner) error = %v", err)
	}
	if got.ID != token.ID || got.UserID != owner.ID {
		t.Fatalf("FindOwned(owner) = %+v, want token %d owner %d", got, token.ID, owner.ID)
	}
	if queryCount != 1 {
		t.Fatalf("FindOwned executed %d queries, want exactly 1", queryCount)
	}
	normalizedSQL := strings.Join(strings.Fields(querySQL), " ")
	if !strings.Contains(normalizedSQL, "id = ? AND user_id = ?") {
		t.Fatalf("FindOwned SQL = %q, want one WHERE containing id AND user_id", normalizedSQL)
	}
	if !reflect.DeepEqual(queryVars, []any{token.ID, owner.ID}) {
		t.Fatalf("FindOwned SQL vars = %#v, want token ID and owner ID", queryVars)
	}

	queryCount = 0
	querySQL = ""
	queryVars = nil
	_, err = NewAdminQuery(ctx).Token().FindOwned(token.ID, other.ID)
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("FindOwned(other) error = %v, want record not found", err)
	}
	if queryCount != 1 {
		t.Fatalf("cross-owner FindOwned executed %d queries, want exactly 1", queryCount)
	}
}

func TestMarketplaceUserGroupFindsEffectiveGroupWithOneQuery(t *testing.T) {
	ctx, db := setupAdminContext(t)
	defaultGroup := &models.UserGroup{ID: models.DefaultUserGroupID, Name: "default", Status: 1, Models: `["default"]`}
	group := &models.UserGroup{Name: "marketplace-group", Status: 1, Models: `["gpt-4o"]`}
	if err := db.Create(defaultGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	ordinaryUser := &models.User{Username: "marketplace-group-user", GroupID: group.ID}
	legacyUser := &models.User{Username: "marketplace-legacy-user", GroupID: 0}
	danglingUser := &models.User{Username: "marketplace-dangling-user", GroupID: 999999}
	for _, user := range []*models.User{ordinaryUser, legacyUser, danglingUser} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&models.User{}).
		Where("id = ?", legacyUser.ID).
		UpdateColumn("group_id", 0).Error; err != nil {
		t.Fatal(err)
	}
	var persistedLegacy models.User
	if err := db.Select("group_id").First(&persistedLegacy, legacyUser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedLegacy.GroupID != 0 {
		t.Fatalf("zero-group fixture persisted GroupID = %d, want 0", persistedLegacy.GroupID)
	}

	queryCount := 0
	callbackName := "test:marketplace_user_group_query"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	tests := []struct {
		name              string
		userID            uint
		wantIdentityID    uint
		wantAuthorization uint
		wantSQL           int
	}{
		{name: "ordinary group", userID: ordinaryUser.ID, wantIdentityID: group.ID, wantAuthorization: group.ID, wantSQL: 1},
		{name: "legacy zero group", userID: legacyUser.ID, wantIdentityID: defaultGroup.ID, wantAuthorization: defaultGroup.ID, wantSQL: 1},
		{name: "system user zero", userID: 0, wantIdentityID: defaultGroup.ID, wantAuthorization: defaultGroup.ID, wantSQL: 1},
		{name: "missing user", userID: 999998, wantIdentityID: defaultGroup.ID, wantAuthorization: defaultGroup.ID, wantSQL: 1},
		{name: "dangling group", userID: danglingUser.ID, wantIdentityID: 999999, wantAuthorization: defaultGroup.ID, wantSQL: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queryCount = 0
			got, err := NewAdminQuery(ctx).UserGroup().FindIdentityAndAuthorizationForUser(test.userID)
			if err != nil {
				t.Fatalf("FindIdentityAndAuthorizationForUser(%d) error = %v", test.userID, err)
			}
			if got.IdentityGroupID != test.wantIdentityID {
				t.Fatalf("identity GroupID = %d, want %d", got.IdentityGroupID, test.wantIdentityID)
			}
			if got.AuthorizationGroup.ID != test.wantAuthorization {
				t.Fatalf("authorization GroupID = %d, want %d", got.AuthorizationGroup.ID, test.wantAuthorization)
			}
			if queryCount != test.wantSQL {
				t.Fatalf("FindIdentityAndAuthorizationForUser(%d) executed %d queries, want %d", test.userID, queryCount, test.wantSQL)
			}
		})
	}
}
