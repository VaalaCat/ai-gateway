package database

import (
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLegacyCoordinatorProcessLock(t *testing.T) {
	if path := os.Getenv("AIGW_TEST_COORDINATOR_PATH"); path != "" {
		coordinator, err := acquireLegacyDeleteCoordinator(path)
		if coordinator != nil {
			_ = coordinator.close()
		}
		require.ErrorIs(t, err, ErrLegacyDeleteInProgress)
		return
	}
	path := filepath.Join(t.TempDir(), "master.db")
	coordinator, err := acquireLegacyDeleteCoordinator(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = coordinator.close() })
	command := exec.Command(os.Args[0], "-test.run=^TestLegacyCoordinatorProcessLock$")
	command.Env = append(os.Environ(), "AIGW_TEST_COORDINATOR_PATH="+path)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.NoError(t, coordinator.close())
	coordinator.file = nil
	reopened, err := acquireLegacyDeleteCoordinator(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.close()) }()
	metadata, err := reopened.read(path)
	require.NoError(t, err)
	require.Equal(t, "idle", metadata.Operation)
}

func TestLegacyCoordinatorRejectsMarkerSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.db")
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o600))
	if err := os.Symlink(target, LegacyDeleteMarkerPath(path)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	coordinator, err := acquireLegacyDeleteCoordinator(path)
	if coordinator != nil {
		_ = coordinator.close()
	}
	require.Error(t, err)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "unchanged", string(data))
}
