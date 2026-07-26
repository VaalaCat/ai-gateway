package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLegacyArtifactFindsExactManifestBackup(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "master.db")
	artifactPath := filepath.Join(dir, "master.db.20260725.pre-split.bak")
	require.NoError(t, os.WriteFile(artifactPath, []byte("legacy"), 0o600))
	require.NoError(t, os.WriteFile(SplitManifestPath(legacyPath), []byte(`{"paths":{"backup_core":"`+artifactPath+`"}}`), 0o600))

	artifact, err := FindLegacyArtifact(legacyPath)
	require.NoError(t, err)
	require.Equal(t, LegacyArtifact{Path: artifactPath, SizeBytes: 6, Exists: true, Available: true}, artifact)
}

func TestLegacyArtifactMissingManifestAndFileAreAvailableAsAbsent(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "master.db")

	artifact, err := FindLegacyArtifact(legacyPath)
	require.NoError(t, err)
	require.Equal(t, LegacyArtifact{Available: true}, artifact)

	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	require.NoError(t, os.WriteFile(SplitManifestPath(legacyPath), []byte(`{"paths":{"backup_core":"`+artifactPath+`"}}`), 0o600))
	artifact, err = FindLegacyArtifact(legacyPath)
	require.NoError(t, err)
	require.Equal(t, LegacyArtifact{Path: artifactPath, Available: true}, artifact)
}

func TestLegacyArtifactRejectsInvalidManifestTargets(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "master.db")
	tests := []struct {
		name    string
		content string
	}{
		{name: "invalid json", content: `{`},
		{name: "relative path", content: `{"paths":{"backup_core":"master.db.pre-split.bak"}}`},
		{name: "wrong suffix", content: `{"paths":{"backup_core":"` + filepath.Join(dir, "backup.db") + `"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(SplitManifestPath(legacyPath), []byte(tt.content), 0o600))
			artifact, err := FindLegacyArtifact(legacyPath)
			require.Error(t, err)
			require.False(t, artifact.Available)
			require.NotEmpty(t, artifact.LastError)
		})
	}
}

func TestLegacyArtifactRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "master.db")
	realPath := filepath.Join(dir, "real.pre-split.bak")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	require.NoError(t, os.WriteFile(realPath, []byte("legacy"), 0o600))
	require.NoError(t, os.Symlink(realPath, artifactPath))
	require.NoError(t, os.WriteFile(SplitManifestPath(legacyPath), []byte(`{"paths":{"backup_core":"`+artifactPath+`"}}`), 0o600))

	artifact, err := FindLegacyArtifact(legacyPath)
	require.ErrorContains(t, err, "regular file")
	require.Equal(t, artifactPath, artifact.Path)
	require.True(t, artifact.Exists)
	require.False(t, artifact.Available)
	require.Contains(t, artifact.LastError, "regular file")
}

func TestLegacyArtifactReadFailureIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "master.db")
	require.NoError(t, os.Mkdir(SplitManifestPath(legacyPath), 0o700))

	artifact, err := FindLegacyArtifact(legacyPath)
	require.Error(t, err)
	require.False(t, artifact.Available)
	require.Contains(t, artifact.LastError, "read legacy split manifest")
}
