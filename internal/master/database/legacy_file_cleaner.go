package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

var legacyDeleteGroup singleflight.Group

type legacyOperationResult uint8

const (
	legacyDeleteOperation legacyOperationResult = iota + 1
	legacyReconcileOperation
)

type LegacyFileCleaner struct {
	ExpectedPath             string
	MigrationState           string
	BatchRunning             func() bool
	ValidateTargets          func() error
	ValidateArtifactDeletion func() error
	beforeIsolate            func(string)
	afterIsolate             func(string)
	removeFile               func(string) error
	operationGroup           *singleflight.Group
}

type legacyDeleteUncertainError struct{ err error }

func (e *legacyDeleteUncertainError) Error() string { return e.err.Error() }
func (e *legacyDeleteUncertainError) Unwrap() error { return e.err }

func (c LegacyFileCleaner) DeleteConfiguredSource(configuredPath, persistedPath, confirmation string) error {
	path, err := CanonicalLegacySourcePath(configuredPath, persistedPath)
	if err != nil {
		return err
	}
	c.ExpectedPath = path
	return c.DeletePath(path, confirmation)
}

func ValidateLegacyCleanerTargets(core, logDB *gorm.DB) error {
	if core == nil || logDB == nil {
		return fmt.Errorf("active core and log databases are required")
	}
	if err := verifyLayout(core, models.DatabaseRoleCore); err != nil {
		return fmt.Errorf("validate active core database: %w", err)
	}
	if err := verifyLayout(logDB, models.DatabaseRoleLog); err != nil {
		return fmt.Errorf("validate active log database: %w", err)
	}
	return nil
}

func verifyLayout(db *gorm.DB, role string) error {
	var layouts []models.DatabaseLayout
	if err := db.Find(&layouts).Error; err != nil {
		return err
	}
	if len(layouts) != 1 || layouts[0].ID != models.DatabaseLayoutID || layouts[0].Role != role || layouts[0].Version != models.DatabaseLayoutVersion {
		return fmt.Errorf("invalid %s database layout marker", role)
	}
	return nil
}

func (c LegacyFileCleaner) DeletePath(path, confirmation string) error {
	if c.MigrationState != "completed" {
		return fmt.Errorf("history migration is not completed")
	}
	if c.BatchRunning != nil && c.BatchRunning() {
		return fmt.Errorf("history migration batch is running")
	}
	return c.deleteValidatedPath(path, confirmation)
}

func (c LegacyFileCleaner) DeleteArtifact(path, confirmation string) error {
	expected, err := exactAbsolutePath(c.ExpectedPath)
	if err != nil {
		return fmt.Errorf("invalid expected path: %w", err)
	}
	if !isLegacyArtifactPath(expected) {
		return fmt.Errorf("legacy artifact path must end with .pre-split.bak")
	}
	return c.deleteValidatedPath(path, confirmation)
}

func (c LegacyFileCleaner) deleteValidatedPath(path, confirmation string) error {
	if c.ValidateTargets != nil {
		if err := c.ValidateTargets(); err != nil {
			return fmt.Errorf("validate active database targets: %w", err)
		}
	}
	if confirmation != "DELETE" {
		return fmt.Errorf("confirmation must be DELETE")
	}
	expected, err := exactAbsolutePath(c.ExpectedPath)
	if err != nil {
		return fmt.Errorf("invalid expected path: %w", err)
	}
	requested, err := exactAbsolutePath(path)
	if err != nil {
		return fmt.Errorf("invalid deletion path: %w", err)
	}
	if requested != expected {
		return fmt.Errorf("unexpected path %q", path)
	}
	for {
		value, deleteErr, _ := c.deleteGroup().Do(expected, func() (any, error) {
			return legacyDeleteOperation, c.deleteCanonicalPath(expected)
		})
		if value == legacyDeleteOperation {
			return deleteErr
		}
	}
}

func AfterLegacyDelete(path string, action func() error) error {
	return afterLegacyDeleteWithGroup(path, &legacyDeleteGroup, action)
}

func afterLegacyDeleteWithGroup(path string, group *singleflight.Group, action func() error) error {
	canonical, err := exactAbsolutePath(path)
	if err != nil {
		return fmt.Errorf("invalid legacy reconciliation path: %w", err)
	}
	if action == nil {
		return fmt.Errorf("legacy reconciliation action is unavailable")
	}
	if group == nil {
		return fmt.Errorf("legacy reconciliation group is unavailable")
	}
	for {
		value, actionErr, _ := group.Do(canonical, func() (any, error) {
			return legacyReconcileOperation, reconcileLegacyDelete(canonical, action)
		})
		if value == legacyReconcileOperation {
			return actionErr
		}
	}
}

func (c LegacyFileCleaner) deleteCanonicalPath(expected string) error {
	coordinator, err := acquireLegacyDeleteCoordinator(expected)
	if err != nil {
		return err
	}
	defer func() { _ = coordinator.close() }()
	if c.ValidateArtifactDeletion != nil {
		if err := c.ValidateArtifactDeletion(); err != nil {
			return fmt.Errorf("validate legacy artifact deletion: %w", err)
		}
	}
	metadata, err := readLegacyDeleteMetadataForRetry(coordinator, expected)
	if err != nil {
		return err
	}
	if metadata.Operation == "delete" {
		sourceExists, quarantineExists, stateErr := legacyDeleteFilesState(metadata)
		if stateErr != nil {
			return stateErr
		}
		if quarantineExists {
			return fmt.Errorf("legacy delete state is uncertain: recorded quarantine exists in coordinator %q", coordinator.path)
		}
		if !sourceExists {
			if isLegacyArtifactPath(expected) {
				return coordinator.write(legacyIdleMetadata(expected))
			}
			return nil
		}
		if err := coordinator.write(legacyIdleMetadata(expected)); err != nil {
			return err
		}
	}
	if err := c.deleteCanonicalFiles(coordinator, expected); err != nil {
		return err
	}
	if isLegacyArtifactPath(expected) {
		return coordinator.write(legacyIdleMetadata(expected))
	}
	return nil
}

func (c LegacyFileCleaner) deleteCanonicalFiles(coordinator *legacyDeleteCoordinator, expected string) error {
	candidates := legacyCandidatePaths(expected)
	identities := make([]os.FileInfo, len(candidates))
	for i, candidate := range candidates {
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect legacy file %q: %w", candidate, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("legacy file %q must be a regular file", candidate)
		}
		identities[i] = info
	}
	metadata := legacyDeleteMetadata{Version: legacyDeleteMarkerVersion, Operation: "delete", Expected: expected}
	metadata.Targets = make([]legacyDeleteTarget, len(candidates))
	for i, candidate := range candidates {
		quarantine, err := reserveLegacyQuarantinePath(filepath.Dir(expected))
		if err != nil {
			return fmt.Errorf("reserve legacy quarantine path: %w", err)
		}
		metadata.Targets[i] = legacyDeleteTarget{Path: candidate, Quarantine: quarantine}
	}
	if err := coordinator.write(metadata); err != nil {
		return err
	}
	for i, candidate := range candidates {
		if identities[i] == nil {
			continue
		}
		if c.beforeIsolate != nil {
			c.beforeIsolate(candidate)
		}
		if err := isolateAndDeleteLegacyFile(candidate, metadata.Targets[i].Quarantine, identities[i], c.afterIsolate, c.removeFile); err != nil {
			var uncertain *legacyDeleteUncertainError
			if errors.As(err, &uncertain) {
				return fmt.Errorf("%w; coordinator retained at %q", err, coordinator.path)
			}
			if resetErr := coordinator.write(legacyIdleMetadata(expected)); resetErr != nil {
				return fmt.Errorf("%v; reset legacy delete coordinator: %w", err, resetErr)
			}
			return err
		}
	}
	return nil
}

func (c LegacyFileCleaner) deleteGroup() *singleflight.Group {
	if c.operationGroup != nil {
		return c.operationGroup
	}
	return &legacyDeleteGroup
}

func LegacyDeleteMarkerPath(path string) string {
	return filepath.Clean(path) + ".delete-pending"
}

func isolateAndDeleteLegacyFile(path, quarantine string, expected os.FileInfo, afterIsolate func(string), removeFile func(string) error) error {
	if err := os.Rename(path, quarantine); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("isolate legacy file %q: %w", path, err)
	}
	if afterIsolate != nil {
		afterIsolate(path)
	}
	isolated, err := os.Lstat(quarantine)
	if err != nil {
		restoreErr := restoreQuarantinedLegacyFile(quarantine, path)
		if restoreErr != nil {
			return &legacyDeleteUncertainError{err: fmt.Errorf("inspect isolated legacy file %q: %v; restore file: %w", path, err, restoreErr)}
		}
		return fmt.Errorf("inspect isolated legacy file %q: %w", path, err)
	}
	if isolated.Mode()&os.ModeSymlink != 0 || !isolated.Mode().IsRegular() || !os.SameFile(expected, isolated) {
		restoreErr := restoreQuarantinedLegacyFile(quarantine, path)
		if restoreErr != nil {
			return &legacyDeleteUncertainError{err: fmt.Errorf("legacy file %q identity changed; restore replacement: %w", path, restoreErr)}
		}
		return fmt.Errorf("legacy file %q identity changed after validation", path)
	}
	if removeFile == nil {
		removeFile = os.Remove
	}
	if err := removeFile(quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
		restoreErr := restoreQuarantinedLegacyFile(quarantine, path)
		if restoreErr != nil {
			return &legacyDeleteUncertainError{err: fmt.Errorf("delete isolated legacy file %q: %v; restore quarantine %q: %w", path, err, quarantine, restoreErr)}
		}
		return fmt.Errorf("delete isolated legacy file %q: %w", path, err)
	}
	return nil
}

func restoreQuarantinedLegacyFile(quarantine, path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("original path is occupied")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(quarantine, path)
}

func exactAbsolutePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	if path != clean || abs != clean {
		return "", fmt.Errorf("path must be clean")
	}
	return clean, nil
}

func CanonicalLegacySourcePath(configured, persisted string) (string, error) {
	configuredPath, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("canonicalize configured legacy source: %w", err)
	}
	persistedPath, err := filepath.Abs(persisted)
	if err != nil {
		return "", fmt.Errorf("canonicalize persisted legacy source: %w", err)
	}
	configuredPath = filepath.Clean(configuredPath)
	persistedPath = filepath.Clean(persistedPath)
	if configuredPath != persistedPath {
		return "", fmt.Errorf("configured legacy source %q does not match persisted source %q", configuredPath, persistedPath)
	}
	return configuredPath, nil
}
