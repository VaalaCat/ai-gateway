package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLegacyArtifactDeletionValidatorRejectsActiveDatabaseIdentityAliases(t *testing.T) {
	tests := []struct {
		name          string
		artifactPath  func(string, string, string) string
		prepare       func(*testing.T, string, string)
		activeSources func(string) []string
		wantError     string
	}{
		{
			name:         "lexical core alias",
			artifactPath: func(corePath, _, _ string) string { return corePath },
			wantError:    "active core database",
		},
		{
			name:         "symlink log alias",
			artifactPath: func(_, _, aliasPath string) string { return aliasPath },
			prepare: func(t *testing.T, logPath, aliasPath string) {
				require.NoError(t, os.Symlink(logPath, aliasPath))
			},
			wantError: "active log database",
		},
		{
			name:         "hardlink active legacy alias",
			artifactPath: func(_, _, aliasPath string) string { return aliasPath },
			prepare: func(t *testing.T, legacyPath, aliasPath string) {
				require.NoError(t, os.WriteFile(legacyPath, []byte("legacy"), 0o600))
				require.NoError(t, os.Link(legacyPath, aliasPath))
			},
			activeSources: func(legacyPath string) []string { return []string{legacyPath} },
			wantError:     "active legacy database",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			corePath := filepath.Join(dir, "core.pre-split.bak")
			logPath := filepath.Join(dir, "log.db")
			legacyPath := filepath.Join(dir, "legacy.db")
			aliasPath := filepath.Join(dir, "master.db.pre-split.bak")
			core := openArtifactDeletionDB(t, corePath, models.DatabaseRoleCore)
			logDB := openArtifactDeletionDB(t, logPath, models.DatabaseRoleLog)
			if tt.prepare != nil {
				firstArg := logPath
				if tt.activeSources != nil {
					firstArg = legacyPath
				}
				tt.prepare(t, firstArg, aliasPath)
			}
			activeSources := []string(nil)
			if tt.activeSources != nil {
				activeSources = tt.activeSources(legacyPath)
			}
			path := tt.artifactPath(corePath, logPath, aliasPath)
			info, err := os.Stat(path)
			require.NoError(t, err)

			artifact := LegacyArtifact{Path: path, SizeBytes: info.Size(), Exists: true, Available: true}
			checked := (LegacyArtifactDeletionValidator{
				CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
				CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
				ActiveLegacySources: activeSources,
			}).Check(artifact)

			require.True(t, checked.InUse)
			require.False(t, checked.CanDelete)
			require.Contains(t, checked.DeleteError, tt.wantError)
		})
	}
}

func TestLegacyArtifactDeletionValidatorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	core := openArtifactDeletionDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactDeletionDB(t, logPath, models.DatabaseRoleLog)
	require.NoError(t, os.WriteFile(artifactPath, []byte("artifact"), 0o600))
	artifact := LegacyArtifact{Path: artifactPath, Exists: true, Available: true}

	tests := []struct {
		name      string
		validator LegacyArtifactDeletionValidator
		wantError string
	}{
		{
			name: "log database unavailable",
			validator: LegacyArtifactDeletionValidator{
				CoreDB: core, LogDB: logDB, CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
			},
			wantError: "log database is unavailable",
		},
		{
			name: "active target metadata unavailable",
			validator: LegacyArtifactDeletionValidator{
				CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
				CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
				ActiveLegacySources: []string{filepath.Join(dir, "missing.db")},
			},
			wantError: "inspect active legacy database",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checked := tt.validator.Check(artifact)
			require.False(t, checked.InUse)
			require.False(t, checked.CanDelete)
			require.Contains(t, checked.DeleteError, tt.wantError)
		})
	}
}

func TestLegacyArtifactDeletionCommandFreshlyRevalidatesBeforeDelete(t *testing.T) {
	t.Run("validator state changes before cleaner isolation", func(t *testing.T) {
		dir := t.TempDir()
		corePath := filepath.Join(dir, "core.db")
		initialCorePath := filepath.Join(dir, "initial-core.db")
		logPath := filepath.Join(dir, "log.db")
		legacyPath := filepath.Join(dir, "master.db")
		artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
		core := openArtifactDeletionDB(t, corePath, models.DatabaseRoleCore)
		initialCore := openArtifactDeletionDB(t, initialCorePath, models.DatabaseRoleCore)
		logDB := openArtifactDeletionDB(t, logPath, models.DatabaseRoleLog)
		writeArtifactManifest(t, legacyPath, artifactPath)
		require.NoError(t, os.Link(corePath, artifactPath))
		validatorBuilds := 0
		command := LegacyArtifactDeletionCommand{
			FindArtifact: func() (LegacyArtifact, error) { return FindLegacyArtifact(legacyPath) },
			BuildValidator: func() LegacyArtifactDeletionValidator {
				validatorBuilds++
				activeCore, activeCorePath := initialCore, initialCorePath
				if validatorBuilds > 1 {
					activeCore, activeCorePath = core, corePath
				}
				return LegacyArtifactDeletionValidator{
					CoreDB: activeCore, LogDB: logDB, LogDatabaseReady: true,
					CoreDatabaseDSN: activeCorePath, LogDatabaseDSN: logPath,
				}
			},
		}

		err := command.Delete("DELETE")

		require.ErrorContains(t, err, "active core database")
		require.Equal(t, 2, validatorBuilds)
		require.FileExists(t, corePath)
		require.FileExists(t, artifactPath)
	})

	t.Run("replacement becomes hardlink to active core", func(t *testing.T) {
		dir := t.TempDir()
		corePath := filepath.Join(dir, "core.db")
		logPath := filepath.Join(dir, "log.db")
		legacyPath := filepath.Join(dir, "master.db")
		artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
		core := openArtifactDeletionDB(t, corePath, models.DatabaseRoleCore)
		logDB := openArtifactDeletionDB(t, logPath, models.DatabaseRoleLog)
		writeArtifactManifest(t, legacyPath, artifactPath)
		require.NoError(t, os.WriteFile(artifactPath, []byte("artifact"), 0o600))
		buildValidator := func() LegacyArtifactDeletionValidator {
			return LegacyArtifactDeletionValidator{
				CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
				CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
			}
		}
		artifact, err := FindLegacyArtifact(legacyPath)
		require.NoError(t, err)
		require.True(t, buildValidator().Check(artifact).CanDelete)
		require.NoError(t, os.Remove(artifactPath))
		require.NoError(t, os.Link(corePath, artifactPath))

		command := LegacyArtifactDeletionCommand{
			FindArtifact:   func() (LegacyArtifact, error) { return FindLegacyArtifact(legacyPath) },
			BuildValidator: buildValidator,
		}
		err = command.Delete("DELETE")

		require.ErrorContains(t, err, "active core database")
		require.FileExists(t, corePath)
		require.FileExists(t, artifactPath)
	})

	t.Run("log database becomes unavailable", func(t *testing.T) {
		dir := t.TempDir()
		corePath := filepath.Join(dir, "core.db")
		logPath := filepath.Join(dir, "log.db")
		legacyPath := filepath.Join(dir, "master.db")
		artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
		core := openArtifactDeletionDB(t, corePath, models.DatabaseRoleCore)
		logDB := openArtifactDeletionDB(t, logPath, models.DatabaseRoleLog)
		writeArtifactManifest(t, legacyPath, artifactPath)
		require.NoError(t, os.WriteFile(artifactPath, []byte("artifact"), 0o600))
		logReady := true
		command := LegacyArtifactDeletionCommand{
			FindArtifact: func() (LegacyArtifact, error) { return FindLegacyArtifact(legacyPath) },
			BuildValidator: func() LegacyArtifactDeletionValidator {
				return LegacyArtifactDeletionValidator{
					CoreDB: core, LogDB: logDB, LogDatabaseReady: logReady,
					CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
				}
			},
		}
		logReady = false

		err := command.Delete("DELETE")

		require.ErrorContains(t, err, "log database is unavailable")
		require.FileExists(t, artifactPath)
	})

	t.Run("eligible artifact", func(t *testing.T) {
		dir := t.TempDir()
		corePath := filepath.Join(dir, "core.db")
		logPath := filepath.Join(dir, "log.db")
		legacyPath := filepath.Join(dir, "master.db")
		artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
		core := openArtifactDeletionDB(t, corePath, models.DatabaseRoleCore)
		logDB := openArtifactDeletionDB(t, logPath, models.DatabaseRoleLog)
		writeArtifactManifest(t, legacyPath, artifactPath)
		require.NoError(t, os.WriteFile(artifactPath, []byte("artifact"), 0o600))
		command := LegacyArtifactDeletionCommand{
			FindArtifact: func() (LegacyArtifact, error) { return FindLegacyArtifact(legacyPath) },
			BuildValidator: func() LegacyArtifactDeletionValidator {
				return LegacyArtifactDeletionValidator{
					CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
					CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
				}
			},
		}

		require.NoError(t, command.Delete("DELETE"))
		require.NoFileExists(t, artifactPath)
	})
}

func openArtifactDeletionDB(t *testing.T, path, role string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	if role == models.DatabaseRoleCore {
		require.NoError(t, models.MigrateCoreDB(db))
	} else {
		require.NoError(t, models.MigrateLogDB(db))
	}
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func writeArtifactManifest(t *testing.T, legacyPath, artifactPath string) {
	t.Helper()
	content := []byte(`{"paths":{"backup_core":"` + artifactPath + `"}}`)
	require.NoError(t, os.WriteFile(SplitManifestPath(legacyPath), content, 0o600))
}
