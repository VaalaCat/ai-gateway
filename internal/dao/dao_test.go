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
	db         *gorm.DB
	logDB      *gorm.DB
	layoutMode app.DatabaseLayoutMode
}

func (a *testApp) GetCoreDB() *gorm.DB                           { return a.db }
func (a *testApp) GetLogDB() *gorm.DB                            { return a.logDB }
func (a *testApp) GetDatabaseLayoutMode() app.DatabaseLayoutMode { return a.layoutMode }

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
	if err := db.AutoMigrate(&models.BillingHourlyBucket{}); err != nil {
		t.Fatalf("migrate billing hourly fixture: %v", err)
	}
	return db
}

func setupStrictSplitDBs(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	open := func(role string, migrate func(*gorm.DB) error) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, migrate(db), role)
		return db
	}
	return open("core", models.MigrateCoreDB), open("log", models.MigrateLogDB)
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
