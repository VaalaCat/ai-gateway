package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openLegacyFixture(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := NewConnector().OpenCore(path)
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	return db
}

func closeGORM(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

func TestBootstrapCoreCopiesOnlyConfigurationFromMonolith(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Create(&models.User{ID: 11, Username: "legacy-user"}).Error)
	require.NoError(t, source.Create(&models.Setting{Key: "legacy-key", Value: "legacy-value"}).Error)

	usage := make([]models.UsageLog, 10_000)
	for i := range usage {
		usage[i] = models.UsageLog{ID: uint(i + 1), RequestID: fmt.Sprintf("legacy-%d", i+1)}
	}
	require.NoError(t, source.CreateInBatches(usage, 500).Error)

	target := openBootstrapTarget(t)
	var tables []string
	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
		AfterTable: func(table string) error {
			tables = append(tables, table)
			require.NotEqual(t, "usage_logs", table)
			return nil
		},
	})
	resultTables := tables
	require.NoError(t, err)
	require.True(t, result.Created)
	require.Equal(t, LegacyLayoutMonolith, result.SourceKind)
	require.EqualValues(t, 2, result.CopiedRows)
	require.NotContains(t, resultTables, "billing_logs")
	require.NotContains(t, resultTables, "token_daily_billings")

	var users, settings, billings int64
	require.NoError(t, target.Model(&models.User{}).Count(&users).Error)
	require.NoError(t, target.Model(&models.Setting{}).Count(&settings).Error)
	require.NoError(t, target.Model(&models.BillingLog{}).Count(&billings).Error)
	require.EqualValues(t, 1, users)
	require.EqualValues(t, 1, settings)
	require.Zero(t, billings)

	var marker models.HistoryMigration
	require.NoError(t, target.First(&marker, models.HistoryMigrationSingletonID).Error)
	require.Equal(t, string(LegacyLayoutMonolith), marker.SourceKind)
	require.Equal(t, sourcePath, marker.SourcePath)
	require.Equal(t, "pending", marker.State)
	require.EqualValues(t, 1_800_000_000, marker.StartedAtUnix)
}

func TestBootstrapCoreCopiesConfigurationFromV5Core(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source, err := NewConnector().OpenCorePath(sourcePath)
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, models.MigrateCoreDB(source))
	require.NoError(t, source.Create(&models.User{ID: 21, Username: "v5-user"}).Error)
	require.NoError(t, source.Create(&models.BillingLog{ID: 22, RequestID: "historical-billing"}).Error)

	target := openBootstrapTarget(t)
	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutV5Core, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	require.True(t, result.Created)
	require.Equal(t, LegacyLayoutV5Core, result.SourceKind)

	var users, billings int64
	require.NoError(t, target.Model(&models.User{}).Count(&users).Error)
	require.NoError(t, target.Model(&models.BillingLog{}).Count(&billings).Error)
	require.EqualValues(t, 1, users)
	require.Zero(t, billings)
}

func TestBootstrapCoreEmptyConfigurationCreatesPendingMarker(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	target := openBootstrapTarget(t)

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	require.True(t, result.Created)
	require.Zero(t, result.CopiedRows)
	require.NoError(t, target.First(&models.HistoryMigration{}, models.HistoryMigrationSingletonID).Error)
}

func TestBootstrapCoreRollsBackAllTablesAndMarkerOnInterrupt(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Create(&models.User{ID: 31, Username: "rollback-user"}).Error)
	require.NoError(t, source.Create(&models.Channel{ChannelCore: models.ChannelCore{ID: 32, Name: "rollback-channel"}}).Error)
	target := openBootstrapTarget(t)
	interrupt := errors.New("interrupt after channels")

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{
		AfterTable: func(table string) error {
			if table == "channels" {
				return interrupt
			}
			return nil
		},
	})
	require.ErrorIs(t, err, interrupt)
	require.False(t, result.Created)

	var users, markers int64
	require.NoError(t, target.Model(&models.User{}).Count(&users).Error)
	require.NoError(t, target.Model(&models.HistoryMigration{}).Count(&markers).Error)
	require.Zero(t, users)
	require.Zero(t, markers)
}

func TestBootstrapCoreExistingMarkerDoesNotOverwriteTarget(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Create(&models.Setting{Key: "site_name", Value: "legacy"}).Error)
	target := openBootstrapTarget(t)

	first, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	require.True(t, first.Created)
	require.NoError(t, target.Model(&models.Setting{}).Where("key = ?", "site_name").Update("value", "runtime-update").Error)
	require.NoError(t, source.Model(&models.Setting{}).Where("key = ?", "site_name").Update("value", "later-legacy").Error)

	second, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{
		AfterTable: func(string) error { return errors.New("must not copy") },
	})
	require.NoError(t, err)
	require.False(t, second.Created)
	require.Zero(t, second.CopiedRows)
	var setting models.Setting
	require.NoError(t, target.First(&setting, "key = ?", "site_name").Error)
	require.Equal(t, "runtime-update", setting.Value)
}

func TestBootstrapCoreDoesNotOverwritePreexistingTargetRows(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Create(&models.Setting{Key: "site_name", Value: "legacy"}).Error)
	target := openBootstrapTarget(t)
	require.NoError(t, target.Create(&models.Setting{Key: "site_name", Value: "target"}).Error)

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.ErrorContains(t, err, "refuse to overwrite")
	require.False(t, result.Created)
	require.Zero(t, result.CopiedRows)
	var markers int64
	require.NoError(t, target.Model(&models.HistoryMigration{}).Count(&markers).Error)
	require.Zero(t, markers)
	var setting models.Setting
	require.NoError(t, target.First(&setting, "key = ?", "site_name").Error)
	require.Equal(t, "target", setting.Value)
}

func TestBootstrapCoreRejectsInvalidInputs(t *testing.T) {
	target := openBootstrapTarget(t)
	_, err := BootstrapCore(t.Context(), nil, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: "/legacy.db"}, BootstrapOptions{})
	require.ErrorContains(t, err, "source")
	_, err = BootstrapCore(t.Context(), target, nil, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: "/legacy.db"}, BootstrapOptions{})
	require.ErrorContains(t, err, "target")
	_, err = BootstrapCore(t.Context(), target, target, LegacyLayoutInfo{Kind: LegacyLayoutNone}, BootstrapOptions{})
	require.ErrorContains(t, err, "layout")
}

func TestBootstrapCoreReportsSourceInspectionFailure(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	closeGORM(t, source)
	target := openBootstrapTarget(t)

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.ErrorContains(t, err, "inspect bootstrap source SQLite file")
	require.False(t, result.Created)
	var markers int64
	require.NoError(t, target.Model(&models.HistoryMigration{}).Count(&markers).Error)
	require.Zero(t, markers)
}

func TestBootstrapCoreRejectsSamePhysicalSQLiteFile(t *testing.T) {
	tests := []struct {
		name        string
		openHandles func(t *testing.T, path string) (*gorm.DB, *gorm.DB)
	}{
		{
			name: "same handle",
			openHandles: func(t *testing.T, path string) (*gorm.DB, *gorm.DB) {
				db := openV5CoreWithoutHistoryMarker(t, path)
				return db, db
			},
		},
		{
			name: "separate handles to same path",
			openHandles: func(t *testing.T, path string) (*gorm.DB, *gorm.DB) {
				source := openV5CoreWithoutHistoryMarker(t, path)
				target, err := NewConnector().OpenExistingCorePath(path)
				require.NoError(t, err)
				t.Cleanup(func() { closeGORM(t, target) })
				return source, target
			},
		},
		{
			name: "hardlink alias",
			openHandles: func(t *testing.T, path string) (*gorm.DB, *gorm.DB) {
				prepareV5CoreWithoutHistoryMarker(t, path)
				alias := filepath.Join(filepath.Dir(path), "hardlink.db")
				require.NoError(t, os.Link(path, alias))
				return openExistingBootstrapPair(t, path, alias)
			},
		},
		{
			name: "symlink alias",
			openHandles: func(t *testing.T, path string) (*gorm.DB, *gorm.DB) {
				prepareV5CoreWithoutHistoryMarker(t, path)
				alias := filepath.Join(filepath.Dir(path), "symlink.db")
				require.NoError(t, os.Symlink(path, alias))
				return openExistingBootstrapPair(t, path, alias)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.db")
			source, target := tt.openHandles(t, path)
			result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutV5Core, Path: path}, BootstrapOptions{})
			require.ErrorContains(t, err, "source and target must use different SQLite files")
			require.False(t, result.Created)
			var markers int64
			require.NoError(t, source.Model(&models.HistoryMigration{}).Count(&markers).Error)
			require.Zero(t, markers)
			var settings int64
			require.NoError(t, source.Model(&models.Setting{}).Count(&settings).Error)
			require.EqualValues(t, 1, settings)
			var setting models.Setting
			require.NoError(t, source.First(&setting, "key = ?", "protected-setting").Error)
			require.Equal(t, "source-value", setting.Value)
		})
	}
}

func TestRequireDifferentSQLiteFilesAllowsIndependentMemoryDatabases(t *testing.T) {
	first, err := NewConnector().OpenCore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, first) })
	second, err := NewConnector().OpenCore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, second) })

	require.NoError(t, requireDifferentSQLiteFiles(first, second))
	require.ErrorContains(t, requireDifferentSQLiteFiles(first, first), "source and target must use different SQLite files")
}

func TestBootstrapTableCopiersMatchModelTableNames(t *testing.T) {
	modelsInOrder := []any{
		&models.User{}, &models.Token{}, &models.Channel{}, &models.ModelConfig{},
		&models.Agent{}, &models.EnrollmentToken{}, &models.Setting{}, &models.AgentRoute{},
		&models.RequestLimiter{}, &models.LimiterBinding{}, &models.TokenTemplate{}, &models.UserGroup{},
		&models.OAuthProvider{}, &models.OAuthIdentity{}, &models.ModelRouting{}, &models.PrivateChannel{},
		&models.PrivateChannelShare{}, &models.AdminScript{}, &models.InviteCode{}, &models.InviteRedemption{},
		&models.MasterSigningKey{},
	}
	copiers := bootstrapTableCopiers()
	require.Len(t, copiers, len(modelsInOrder))
	db := openBootstrapTarget(t)
	for i, model := range modelsInOrder {
		statement := &gorm.Statement{DB: db}
		require.NoError(t, statement.Parse(model))
		require.Equalf(t, statement.Table, copiers[i].name, "copier %d uses a stale table name", i)
	}
}

func TestBootstrapTableCopiersCrossReadBatchBoundary(t *testing.T) {
	t.Run("numeric primary key", func(t *testing.T) {
		sourcePath := filepath.Join(t.TempDir(), "master.db")
		source := openLegacyFixture(t, sourcePath)
		t.Cleanup(func() { closeGORM(t, source) })
		users := make([]models.User, defaultMigrationReadBatch+1)
		for i := range users {
			users[i] = models.User{ID: uint(i + 1), Username: fmt.Sprintf("user-%04d", i+1)}
		}
		require.NoError(t, source.CreateInBatches(users, 500).Error)
		require.NoError(t, source.Exec(`UPDATE users SET status = 0, group_id = 0, created_at = 0, updated_at = 0 WHERE id = 2000`).Error)
		require.NoError(t, source.Exec(`UPDATE users SET status = 9, group_id = 8, created_at = 7, updated_at = 6 WHERE id = 2001`).Error)
		target := openBootstrapTarget(t)

		result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
		require.NoError(t, err)
		require.EqualValues(t, len(users), result.CopiedRows)
		var count, distinct int64
		require.NoError(t, target.Model(&models.User{}).Count(&count).Error)
		require.NoError(t, target.Model(&models.User{}).Distinct("id").Count(&distinct).Error)
		require.EqualValues(t, len(users), count)
		require.Equal(t, count, distinct)
		var boundary []struct {
			ID        int
			Status    int
			GroupID   int `gorm:"column:group_id"`
			CreatedAt int `gorm:"column:created_at"`
			UpdatedAt int `gorm:"column:updated_at"`
		}
		require.NoError(t, target.Raw(`SELECT id, status, group_id, created_at, updated_at FROM users WHERE id IN (2000, 2001) ORDER BY id`).Scan(&boundary).Error)
		require.Equal(t, []struct {
			ID        int
			Status    int
			GroupID   int `gorm:"column:group_id"`
			CreatedAt int `gorm:"column:created_at"`
			UpdatedAt int `gorm:"column:updated_at"`
		}{
			{ID: 2000},
			{ID: 2001, Status: 9, GroupID: 8, CreatedAt: 7, UpdatedAt: 6},
		}, boundary)
	})

	t.Run("string primary key", func(t *testing.T) {
		sourcePath := filepath.Join(t.TempDir(), "master.db")
		source := openLegacyFixture(t, sourcePath)
		t.Cleanup(func() { closeGORM(t, source) })
		settings := make([]models.Setting, defaultMigrationReadBatch+1)
		for i := range settings {
			settings[i] = models.Setting{Key: fmt.Sprintf("setting-%04d", i+1), Value: "value"}
		}
		require.NoError(t, source.CreateInBatches(settings, 500).Error)
		require.NoError(t, source.Exec(`UPDATE settings SET value = '' WHERE key = 'setting-2000'`).Error)
		require.NoError(t, source.Exec(`UPDATE settings SET value = 'after-boundary' WHERE key = 'setting-2001'`).Error)
		target := openBootstrapTarget(t)

		result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
		require.NoError(t, err)
		require.EqualValues(t, len(settings), result.CopiedRows)
		var count, distinct int64
		require.NoError(t, target.Model(&models.Setting{}).Count(&count).Error)
		require.NoError(t, target.Model(&models.Setting{}).Distinct("key").Count(&distinct).Error)
		require.EqualValues(t, len(settings), count)
		require.Equal(t, count, distinct)
		var boundary []models.Setting
		require.NoError(t, target.Where("key IN ?", []string{"setting-2000", "setting-2001"}).Order("key").Find(&boundary).Error)
		require.Equal(t, []models.Setting{{Key: "setting-2000", Value: ""}, {Key: "setting-2001", Value: "after-boundary"}}, boundary)
	})
}

func openV5CoreWithoutHistoryMarker(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := NewConnector().OpenCorePath(path)
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, db) })
	require.NoError(t, models.MigrateCoreDB(db))
	require.NoError(t, db.Create(&models.Setting{Key: "protected-setting", Value: "source-value"}).Error)
	return db
}

func prepareV5CoreWithoutHistoryMarker(t *testing.T, path string) {
	t.Helper()
	db, err := NewConnector().OpenCorePath(path)
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	require.NoError(t, db.Create(&models.Setting{Key: "protected-setting", Value: "source-value"}).Error)
	closeGORM(t, db)
}

func openExistingBootstrapPair(t *testing.T, sourcePath, targetPath string) (*gorm.DB, *gorm.DB) {
	t.Helper()
	source, err := NewConnector().OpenExistingCorePath(sourcePath)
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, source) })
	target, err := NewConnector().OpenExistingCorePath(targetPath)
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, target) })
	return source, target
}

func openBootstrapTarget(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := NewConnector().OpenCorePath(filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, db) })
	require.NoError(t, models.MigrateCoreDB(db))
	return db
}
