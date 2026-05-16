package models

import (
	"testing"
)

func TestOAuthProviderMigrate(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	p := &OAuthProvider{
		Name:                  "github",
		DisplayName:           "GitHub",
		AuthorizationEndpoint: "https://example.com/authorize",
		TokenEndpoint:         "https://example.com/token",
		UserinfoEndpoint:      "https://example.com/userinfo",
		ClientID:              "cid",
		ClientSecret:          "csec",
		Scopes:                "read:user",
		Enabled:               true,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected ID")
	}
	dup := *p
	dup.ID = 0
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("expected duplicate name to fail")
	}
}

func TestOAuthIdentityCompositeUnique(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	u := &User{Username: "u1", Password: "h"}
	db.Create(u)
	p := &OAuthProvider{Name: "github", DisplayName: "GH"}
	db.Create(p)

	a := &OAuthIdentity{UserID: u.ID, ProviderID: p.ID, Subject: "sub-1"}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("create a: %v", err)
	}
	b := &OAuthIdentity{UserID: u.ID, ProviderID: p.ID, Subject: "sub-1"}
	if err := db.Create(b).Error; err == nil {
		t.Fatal("expected (provider_id, subject) unique violation")
	}
	c := &OAuthIdentity{UserID: u.ID, ProviderID: p.ID, Subject: "sub-2"}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("different subject should succeed: %v", err)
	}
}
