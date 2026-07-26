package database

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

func TestLegacyCleanerDeletesExactRegularFileAndSidecars(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "master.db")
	for _, path := range []string{source, source + "-wal", source + "-shm"} {
		require.NoError(t, os.WriteFile(path, []byte("db"), 0o600))
	}
	cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
	require.NoError(t, cleaner.DeletePath(source, "DELETE"))
	require.NoFileExists(t, source)
	require.NoFileExists(t, source+"-wal")
	require.NoFileExists(t, source+"-shm")
	require.NoError(t, cleaner.DeletePath(source, "DELETE"), "repeated deletion must be idempotent")
}

func TestLegacyCleanerDeletesArtifactWithoutTouchingSidecars(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "master.db.pre-split.bak")
	for _, path := range []string{artifact, artifact + "-wal", artifact + "-shm"} {
		require.NoError(t, os.WriteFile(path, []byte("legacy"), 0o600))
	}
	cleaner := LegacyFileCleaner{
		ExpectedPath: artifact, MigrationState: "pending", BatchRunning: func() bool { return true },
		ValidateTargets: func() error { return nil },
	}

	require.NoError(t, cleaner.DeleteArtifact(artifact, "DELETE"))
	require.NoFileExists(t, artifact)
	require.FileExists(t, artifact+"-wal")
	require.FileExists(t, artifact+"-shm")
	requireLegacyMarkerOperation(t, artifact, "idle")
}

func TestLegacyCleanerArtifactDeletionIgnoresHistoryState(t *testing.T) {
	for _, state := range []string{"pending", "copying", "caught_up"} {
		t.Run(state, func(t *testing.T) {
			artifact := writeLegacyCleanerFile(t, "master.db.pre-split.bak")
			cleaner := LegacyFileCleaner{
				ExpectedPath: artifact, MigrationState: state, BatchRunning: func() bool { return true },
				ValidateTargets: func() error { return nil },
			}

			require.NoError(t, cleaner.DeleteArtifact(artifact, "DELETE"))
			require.NoFileExists(t, artifact)
		})
	}
}

func TestLegacyCleanerArtifactDeletionKeepsSafetyChecks(t *testing.T) {
	t.Run("confirmation", func(t *testing.T) {
		artifact := writeLegacyCleanerFile(t, "master.db.pre-split.bak")
		cleaner := LegacyFileCleaner{ExpectedPath: artifact, ValidateTargets: func() error { return nil }}
		require.ErrorContains(t, cleaner.DeleteArtifact(artifact, "delete"), "confirmation")
		require.FileExists(t, artifact)
	})
	t.Run("target layout", func(t *testing.T) {
		artifact := writeLegacyCleanerFile(t, "master.db.pre-split.bak")
		cleaner := LegacyFileCleaner{
			ExpectedPath:    artifact,
			ValidateTargets: func() error { return errors.New("log database layout is unavailable") },
		}
		require.ErrorContains(t, cleaner.DeleteArtifact(artifact, "DELETE"), "layout is unavailable")
		require.FileExists(t, artifact)
	})
	t.Run("exact path", func(t *testing.T) {
		artifact := writeLegacyCleanerFile(t, "master.db.pre-split.bak")
		other := writeLegacyCleanerFile(t, "other.db.pre-split.bak")
		cleaner := LegacyFileCleaner{ExpectedPath: artifact, ValidateTargets: func() error { return nil }}
		require.ErrorContains(t, cleaner.DeleteArtifact(other, "DELETE"), "unexpected path")
		require.FileExists(t, artifact)
		require.FileExists(t, other)
	})
}

func TestLegacyCleanerRejectsUnsafeDeletion(t *testing.T) {
	t.Run("wrong path", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		cleaner := LegacyFileCleaner{ExpectedPath: source + ".other", MigrationState: "completed"}
		require.ErrorContains(t, cleaner.DeletePath(source, "DELETE"), "unexpected path")
		require.FileExists(t, source)
	})
	t.Run("relative expected path", func(t *testing.T) {
		cleaner := LegacyFileCleaner{ExpectedPath: "master.db", MigrationState: "completed"}
		require.ErrorContains(t, cleaner.DeletePath("master.db", "DELETE"), "absolute")
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		realPath, source := filepath.Join(dir, "real.db"), filepath.Join(dir, "master.db")
		require.NoError(t, os.WriteFile(realPath, []byte("db"), 0o600))
		require.NoError(t, os.Symlink(realPath, source))
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
		require.ErrorContains(t, cleaner.DeletePath(source, "DELETE"), "regular file")
		require.FileExists(t, realPath)
	})
	t.Run("hardlink alias", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		alias := filepath.Join(filepath.Dir(source), "alias.db")
		require.NoError(t, os.Link(source, alias))
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed"}
		require.ErrorContains(t, cleaner.DeletePath(alias, "DELETE"), "unexpected path")
		require.FileExists(t, source)
	})
	t.Run("active migration", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "copying"}
		require.ErrorContains(t, cleaner.DeletePath(source, "DELETE"), "migration is not completed")
		require.FileExists(t, source)
	})
	t.Run("batch running", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", BatchRunning: func() bool { return true }}
		require.ErrorContains(t, cleaner.DeletePath(source, "DELETE"), "batch is running")
		require.FileExists(t, source)
	})
	t.Run("wrong confirmation", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
		require.ErrorContains(t, cleaner.DeletePath(source, "delete"), "confirmation")
		require.FileExists(t, source)
	})
	t.Run("target validation", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return errors.New("wrong log role") }}
		require.ErrorContains(t, cleaner.DeletePath(source, "DELETE"), "wrong log role")
		require.FileExists(t, source)
	})
}

func TestLegacyCleanerRejectsDirectorySidecarWithoutRemovingSource(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	require.NoError(t, os.Mkdir(source+"-wal", 0o700))
	cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}

	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "regular file")
	require.FileExists(t, source)
	require.DirExists(t, source+"-wal")
	requireLegacyMarkerOperation(t, source, "idle")
}

func TestLegacyCleanerConcurrentDeleteIsIdempotent(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	validated := make(chan struct{}, 2)
	releaseValidation := make(chan struct{})
	isolated := make(chan struct{}, 1)
	releaseDelete := make(chan struct{})
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed",
		ValidateTargets: func() error { validated <- struct{}{}; <-releaseValidation; return nil },
		afterIsolate:    func(string) { isolated <- struct{}{}; <-releaseDelete },
	}
	errs := make(chan error, 2)
	var workers conc.WaitGroup
	for range 2 {
		workers.Go(func() { errs <- cleaner.DeletePath(source, "DELETE") })
	}
	<-validated
	<-validated
	close(releaseValidation)
	<-isolated
	time.Sleep(20 * time.Millisecond)
	close(releaseDelete)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoFileExists(t, source)
}

func TestLegacyCleanerConcurrentRemoveFailureIsShared(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	validated := make(chan struct{}, 2)
	releaseValidation := make(chan struct{})
	removeStarted := make(chan struct{}, 1)
	releaseRemove := make(chan struct{})
	var removeCalls atomic.Int32
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed",
		ValidateTargets: func() error { validated <- struct{}{}; <-releaseValidation; return nil },
		removeFile: func(string) error {
			removeCalls.Add(1)
			removeStarted <- struct{}{}
			<-releaseRemove
			return errors.New("remove denied")
		},
	}
	errs := make(chan error, 2)
	var workers conc.WaitGroup
	for range 2 {
		workers.Go(func() { errs <- cleaner.DeletePath(source, "DELETE") })
	}
	<-validated
	<-validated
	close(releaseValidation)
	<-removeStarted
	time.Sleep(20 * time.Millisecond)
	close(releaseRemove)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.ErrorContains(t, err, "remove denied")
	}
	require.EqualValues(t, 1, removeCalls.Load(), "overlapping caller must share the leader result")
	require.FileExists(t, source, "failed unlink must restore the original path")
}

func TestLegacyDeleteReconciliationWaitsForFailedRemoveRestore(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	removeStarted := make(chan struct{}, 1)
	releaseRemove := make(chan struct{})
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		removeFile: func(string) error {
			removeStarted <- struct{}{}
			<-releaseRemove
			return errors.New("remove denied")
		},
	}
	deleteErr := make(chan error, 1)
	var workers conc.WaitGroup
	workers.Go(func() { deleteErr <- cleaner.DeletePath(source, "DELETE") })
	<-removeStarted
	reconciled := make(chan bool, 1)
	workers.Go(func() {
		err := AfterLegacyDelete(source, func() error {
			_, statErr := os.Lstat(source)
			reconciled <- errors.Is(statErr, os.ErrNotExist)
			return statErr
		})
		require.NoError(t, err)
	})
	select {
	case <-reconciled:
		t.Fatal("reconciliation ran while the source was quarantined")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseRemove)
	workers.Wait()
	require.ErrorContains(t, <-deleteErr, "remove denied")
	require.False(t, <-reconciled, "restored source must not be observed as missing")
}

func TestLegacyDeleteWaitsForReconciliationThenStillDeletes(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	reconcileStarted := make(chan struct{}, 1)
	releaseReconcile := make(chan struct{})
	reconcileErr := make(chan error, 1)
	var workers conc.WaitGroup
	workers.Go(func() {
		reconcileErr <- AfterLegacyDelete(source, func() error {
			reconcileStarted <- struct{}{}
			<-releaseReconcile
			return nil
		})
	})
	<-reconcileStarted
	deleteErr := make(chan error, 1)
	cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
	workers.Go(func() { deleteErr <- cleaner.DeletePath(source, "DELETE") })
	select {
	case <-deleteErr:
		t.Fatal("delete returned before the active reconciliation completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseReconcile)
	workers.Wait()
	require.NoError(t, <-reconcileErr)
	require.NoError(t, <-deleteErr)
	require.NoFileExists(t, source)
}

func TestLegacyDeleteMarkerCoordinatesIndependentCleaners(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	isolated := make(chan struct{}, 1)
	releaseDelete := make(chan struct{})
	groupA, groupB := &singleflight.Group{}, &singleflight.Group{}
	cleanerA := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		operationGroup: groupA,
		afterIsolate:   func(string) { isolated <- struct{}{}; <-releaseDelete },
	}
	cleanerB := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }, operationGroup: groupB,
	}
	deleteA := make(chan error, 1)
	var workers conc.WaitGroup
	workers.Go(func() { deleteA <- cleanerA.DeletePath(source, "DELETE") })
	<-isolated
	require.FileExists(t, LegacyDeleteMarkerPath(source))

	err := cleanerB.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "delete operation is already in progress")
	marked := false
	err = afterLegacyDeleteWithGroup(source, groupB, func() error {
		marked = true
		return nil
	})
	require.ErrorContains(t, err, "already in progress")
	require.False(t, marked)

	close(releaseDelete)
	workers.Wait()
	require.NoError(t, <-deleteA)
	require.NoError(t, afterLegacyDeleteWithGroup(source, groupB, func() error { return nil }))
	requireLegacyMarkerOperation(t, source, "idle")
	require.NoError(t, cleanerB.DeletePath(source, "DELETE"), "retry after marker removal must be idempotent")
}

func TestLegacyDeleteMarkerRemainsWhenRemoveAndRestoreFail(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	groupA, groupB := &singleflight.Group{}, &singleflight.Group{}
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		operationGroup: groupA,
		afterIsolate:   func(path string) { require.NoError(t, os.WriteFile(path, []byte("occupied"), 0o600)) },
		removeFile:     func(string) error { return errors.New("remove denied") },
	}

	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "remove denied")
	require.ErrorContains(t, err, LegacyDeleteMarkerPath(source))
	require.FileExists(t, LegacyDeleteMarkerPath(source))
	marked := false
	err = afterLegacyDeleteWithGroup(source, groupB, func() error {
		marked = true
		return nil
	})
	require.Error(t, err)
	require.False(t, marked)
}

func TestLegacyDeleteFlockClosesReconcileCheckThenDeleteWindow(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	reconcileStarted := make(chan struct{}, 1)
	releaseReconcile := make(chan struct{})
	reconcileErr := make(chan error, 1)
	groupA, groupB := &singleflight.Group{}, &singleflight.Group{}
	var workers conc.WaitGroup
	workers.Go(func() {
		reconcileErr <- afterLegacyDeleteWithGroup(source, groupB, func() error {
			reconcileStarted <- struct{}{}
			<-releaseReconcile
			return nil
		})
	})
	<-reconcileStarted
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }, operationGroup: groupA,
	}
	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorIs(t, err, ErrLegacyDeleteInProgress)
	require.FileExists(t, source)
	close(releaseReconcile)
	workers.Wait()
	require.NoError(t, <-reconcileErr)
}

func TestLegacyDeleteWritesExactMetadataBeforeFirstRename(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "master.db")
	for _, path := range legacyCandidatePaths(source) {
		require.NoError(t, os.WriteFile(path, []byte("db"), 0o600))
	}
	metadataSeen := make(chan legacyDeleteMetadata, 1)
	var calls atomic.Int32
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		beforeIsolate: func(string) {
			if calls.Add(1) != 1 {
				return
			}
			data, err := os.ReadFile(LegacyDeleteMarkerPath(source))
			require.NoError(t, err)
			var metadata legacyDeleteMetadata
			require.NoError(t, json.Unmarshal(data, &metadata))
			metadataSeen <- metadata
		},
	}

	require.NoError(t, cleaner.DeletePath(source, "DELETE"))
	metadata := <-metadataSeen
	require.Equal(t, "delete", metadata.Operation)
	require.Equal(t, legacyCandidatePaths(source), []string{metadata.Targets[0].Path, metadata.Targets[1].Path, metadata.Targets[2].Path})
	for _, target := range metadata.Targets {
		require.True(t, filepath.IsAbs(target.Quarantine))
		require.Equal(t, dir, filepath.Dir(target.Quarantine))
		require.Contains(t, filepath.Base(target.Quarantine), ".legacy-delete-")
	}
}

func TestLegacyDeleteStaleCompletedReconcilesAndMarkFailureRetries(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
	require.NoError(t, cleaner.DeletePath(source, "DELETE"))
	requireLegacyMarkerOperation(t, source, "delete")
	marks := 0
	err := AfterLegacyDelete(source, func() error { marks++; return errors.New("db unavailable") })
	require.ErrorContains(t, err, "db unavailable")
	require.Equal(t, 1, marks)
	requireLegacyMarkerOperation(t, source, "delete")
	require.NoError(t, AfterLegacyDelete(source, func() error { marks++; return nil }))
	require.Equal(t, 2, marks)
	requireLegacyMarkerOperation(t, source, "idle")
}

func TestLegacyDeleteStaleSourceRetriesAndCorruptStateIsFailSafe(t *testing.T) {
	t.Run("source remains", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		writeLegacyDeleteMetadata(t, source)
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
		require.NoError(t, cleaner.DeletePath(source, "DELETE"))
		require.NoFileExists(t, source)
	})
	t.Run("corrupt missing source", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "master.db")
		require.NoError(t, os.WriteFile(LegacyDeleteMarkerPath(source), []byte("{"), 0o600))
		called := false
		err := AfterLegacyDelete(source, func() error { called = true; return nil })
		require.ErrorContains(t, err, "decode legacy delete coordinator")
		require.False(t, called)
	})
	t.Run("corrupt source present with unknown quarantine", func(t *testing.T) {
		source := writeLegacyCleanerFile(t, "master.db")
		unknownQuarantine := filepath.Join(filepath.Dir(source), ".legacy-delete-unknown")
		require.NoError(t, os.WriteFile(unknownQuarantine, []byte("wal"), 0o600))
		require.NoError(t, os.WriteFile(LegacyDeleteMarkerPath(source), []byte("{"), 0o600))
		cleaner := LegacyFileCleaner{ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil }}
		err := cleaner.DeletePath(source, "DELETE")
		require.ErrorContains(t, err, "decode legacy delete coordinator")
		require.FileExists(t, source)
		require.FileExists(t, unknownQuarantine)
		marked := false
		err = AfterLegacyDelete(source, func() error { marked = true; return nil })
		require.ErrorContains(t, err, "decode legacy delete coordinator")
		require.False(t, marked)
		require.FileExists(t, source)
		require.FileExists(t, unknownQuarantine)
	})
}

func TestLegacyCleanerRejectsPathReplacementAfterPreflight(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "master.db")
	original := filepath.Join(dir, "original.db")
	require.NoError(t, os.WriteFile(source, []byte("original"), 0o600))
	replacement := []byte("replacement")
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		beforeIsolate: func(path string) {
			require.NoError(t, os.Rename(path, original))
			require.NoError(t, os.WriteFile(path, replacement, 0o600))
		},
	}

	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "identity changed")
	require.FileExists(t, original)
	content, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, replacement, content, "replacement must be restored, not deleted")
}

func TestLegacyCleanerPreservesQuarantineWhenReplacementCannotBeRestored(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "master.db")
	original := filepath.Join(dir, "original.db")
	require.NoError(t, os.WriteFile(source, []byte("original"), 0o600))
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		beforeIsolate: func(path string) {
			require.NoError(t, os.Rename(path, original))
			require.NoError(t, os.WriteFile(path, []byte("replacement"), 0o600))
		},
		afterIsolate: func(path string) {
			require.NoError(t, os.WriteFile(path, []byte("occupied"), 0o600))
		},
	}

	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "restore replacement")
	require.FileExists(t, original)
	content, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, []byte("occupied"), content)
	quarantines, globErr := filepath.Glob(filepath.Join(dir, ".legacy-delete-*"))
	require.NoError(t, globErr)
	require.Len(t, quarantines, 1)
	content, readErr = os.ReadFile(quarantines[0])
	require.NoError(t, readErr)
	require.Equal(t, []byte("replacement"), content, "unrestored replacement must remain quarantined")
}

func TestLegacyCleanerRestoresVerifiedFileWhenFinalRemoveFails(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		removeFile: func(string) error { return errors.New("remove denied") },
	}

	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "remove denied")
	require.FileExists(t, source)
	requireLegacyMarkerOperation(t, source, "idle")
	quarantines, globErr := filepath.Glob(filepath.Join(filepath.Dir(source), ".legacy-delete-*"))
	require.NoError(t, globErr)
	require.Empty(t, quarantines)
}

func TestLegacyCleanerKeepsVerifiedQuarantineWhenRemoveAndRestoreFail(t *testing.T) {
	source := writeLegacyCleanerFile(t, "master.db")
	cleaner := LegacyFileCleaner{
		ExpectedPath: source, MigrationState: "completed", ValidateTargets: func() error { return nil },
		afterIsolate: func(path string) { require.NoError(t, os.WriteFile(path, []byte("occupied"), 0o600)) },
		removeFile:   func(string) error { return errors.New("remove denied") },
	}

	err := cleaner.DeletePath(source, "DELETE")
	require.ErrorContains(t, err, "remove denied")
	require.ErrorContains(t, err, "restore")
	require.FileExists(t, LegacyDeleteMarkerPath(source))
	quarantines, globErr := filepath.Glob(filepath.Join(filepath.Dir(source), ".legacy-delete-*"))
	require.NoError(t, globErr)
	require.Len(t, quarantines, 1)
}

func TestCanonicalLegacySourcePathRequiresPersistedMatch(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "master.db")
	cwd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(cwd, abs)
	require.NoError(t, err)

	canonical, err := CanonicalLegacySourcePath(rel, abs)
	require.NoError(t, err)
	require.Equal(t, abs, canonical)
	_, err = CanonicalLegacySourcePath(abs, filepath.Join(dir, "other.db"))
	require.ErrorContains(t, err, "does not match")
}

func TestLegacyCleanerDeleteConfiguredSourceBindsPersistedPath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "master.db")
	other := filepath.Join(dir, "other.db")
	require.NoError(t, os.WriteFile(source, []byte("source"), 0o600))
	require.NoError(t, os.WriteFile(other, []byte("other"), 0o600))
	cleaner := LegacyFileCleaner{MigrationState: "completed", ValidateTargets: func() error { return nil }}

	err := cleaner.DeleteConfiguredSource(source, other, "DELETE")
	require.ErrorContains(t, err, "does not match")
	require.FileExists(t, source)
	require.FileExists(t, other)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	relativeSource, err := filepath.Rel(cwd, source)
	require.NoError(t, err)
	require.NoError(t, cleaner.DeleteConfiguredSource(relativeSource, source, "DELETE"))
	require.NoFileExists(t, source)
}

func TestValidateLegacyCleanerTargetsRequiresCoreAndLogRoles(t *testing.T) {
	core, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	require.NoError(t, err)
	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "log.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(core))
	require.NoError(t, models.MigrateLogDB(logDB))

	require.NoError(t, ValidateLegacyCleanerTargets(core, logDB))
	require.Error(t, ValidateLegacyCleanerTargets(nil, logDB))
	require.ErrorContains(t, ValidateLegacyCleanerTargets(logDB, core), "core")
}

func writeLegacyCleanerFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("db"), 0o600))
	return path
}

func requireLegacyMarkerOperation(t *testing.T, source, operation string) {
	t.Helper()
	coordinator, err := acquireLegacyDeleteCoordinator(source)
	require.NoError(t, err)
	defer func() { require.NoError(t, coordinator.close()) }()
	metadata, err := coordinator.read(source)
	require.NoError(t, err)
	require.Equal(t, operation, metadata.Operation)
}

func writeLegacyDeleteMetadata(t *testing.T, source string) {
	t.Helper()
	coordinator, err := acquireLegacyDeleteCoordinator(source)
	require.NoError(t, err)
	defer func() { require.NoError(t, coordinator.close()) }()
	metadata := legacyDeleteMetadata{Version: legacyDeleteMarkerVersion, Operation: "delete", Expected: source}
	for _, path := range legacyCandidatePaths(source) {
		quarantine, reserveErr := reserveLegacyQuarantinePath(filepath.Dir(source))
		require.NoError(t, reserveErr)
		metadata.Targets = append(metadata.Targets, legacyDeleteTarget{Path: path, Quarantine: quarantine})
	}
	require.NoError(t, coordinator.write(metadata))
}
