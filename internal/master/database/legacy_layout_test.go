package database

import (
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestFindLegacyLayout(t *testing.T) {
	t.Run("missing file is a fresh install", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.db")
		info, err := FindLegacyLayout(path)
		require.NoError(t, err)
		require.Equal(t, LegacyLayoutInfo{Kind: LegacyLayoutNone, Path: path}, info)
		require.NoFileExists(t, path)
	})

	t.Run("unmarked usage schema is a monolith", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "master.db")
		db := openLegacyFixture(t, path)
		closeGORM(t, db)

		info, err := FindLegacyLayout(path)
		require.NoError(t, err)
		require.Equal(t, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: path}, info)
	})

	t.Run("marked core with billing is v5 core", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "master.db")
		db, err := NewConnector().OpenCorePath(path)
		require.NoError(t, err)
		require.NoError(t, models.MigrateCoreDB(db))
		closeGORM(t, db)

		info, err := FindLegacyLayout(path)
		require.NoError(t, err)
		require.Equal(t, LegacyLayoutInfo{Kind: LegacyLayoutV5Core, Path: path}, info)
	})
}

func TestFindLegacyLayoutRejectsUnknownSchema(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{
			name: "unknown unmarked schema without usage logs",
			setup: func(t *testing.T, path string) {
				db, err := NewConnector().OpenCorePath(path)
				require.NoError(t, err)
				require.NoError(t, db.Exec("CREATE TABLE unrelated (id INTEGER PRIMARY KEY)").Error)
				closeGORM(t, db)
			},
		},
		{
			name: "core marker missing billing logs",
			setup: func(t *testing.T, path string) {
				db, err := NewConnector().OpenCorePath(path)
				require.NoError(t, err)
				require.NoError(t, db.AutoMigrate(&models.DatabaseLayout{}))
				require.NoError(t, db.Create(&models.DatabaseLayout{ID: 1, Role: models.DatabaseRoleCore, Version: models.DatabaseLayoutVersion}).Error)
				closeGORM(t, db)
			},
		},
		{
			name: "log marker is not a legacy core",
			setup: func(t *testing.T, path string) {
				db, err := NewConnector().OpenLogPath(path)
				require.NoError(t, err)
				require.NoError(t, models.MigrateLogDB(db))
				closeGORM(t, db)
			},
		},
		{
			name: "unknown core marker version",
			setup: func(t *testing.T, path string) {
				db, err := NewConnector().OpenCorePath(path)
				require.NoError(t, err)
				require.NoError(t, models.MigrateCoreDB(db))
				require.NoError(t, db.Model(&models.DatabaseLayout{}).Where("id = ?", models.DatabaseLayoutID).Update("version", 99).Error)
				closeGORM(t, db)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			tt.setup(t, path)
			_, err := FindLegacyLayout(path)
			require.ErrorContains(t, err, "legacy database layout")
		})
	}
}

func TestFindLegacyLayoutRejectsNonFileDSN(t *testing.T) {
	for _, dsn := range []string{"", ":memory:", "file:temporary?mode=memory&cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			_, err := FindLegacyLayout(dsn)
			require.Error(t, err)
		})
	}
}
