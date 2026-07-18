package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// testApp satisfies dao.AppProvider for testing.
type testApp struct {
	db *gorm.DB
}

func (a *testApp) GetDB() *gorm.DB { return a.db }

func TestSetupTestDBClosesOwnedDatabaseAfterTestCleanup(t *testing.T) {
	var ping func() error
	t.Run("fixture", func(t *testing.T) {
		db := setupTestDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Ping())
		ping = sqlDB.Ping
	})

	require.NotNil(t, ping)
	require.Error(t, ping())
}

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func setupTestApp(t *testing.T) (*testApp, *gorm.DB) {
	t.Helper()
	db := setupTestDB(t)
	a := &testApp{db: db}
	return a, db
}

func setupAdminContext(t *testing.T) (Context, *gorm.DB) {
	t.Helper()
	a, db := setupTestApp(t)
	return NewContext(a), db
}

func setupUserContext(t *testing.T, userID uint) (UserContext, *gorm.DB) {
	t.Helper()
	a, db := setupTestApp(t)
	ui := &app.UserInfo{UserID: userID, Username: "testuser", Role: 1}
	return NewUserContext(a, ui), db
}
