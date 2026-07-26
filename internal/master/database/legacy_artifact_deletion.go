package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/gorm"
)

type LegacyArtifactDeletionValidator struct {
	CoreDB              *gorm.DB
	LogDB               *gorm.DB
	LogDatabaseReady    bool
	CoreDatabaseDSN     string
	LogDatabaseDSN      string
	ActiveLegacySources []string
}

func (v LegacyArtifactDeletionValidator) Check(artifact LegacyArtifact) LegacyArtifact {
	artifact.InUse = false
	artifact.CanDelete = false
	artifact.DeleteError = ""
	if artifact.Path == "" {
		return artifact
	}

	artifactPath, err := exactAbsolutePath(artifact.Path)
	if err != nil {
		artifact.DeleteError = fmt.Sprintf("validate legacy artifact path: %v", err)
		return artifact
	}
	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		artifact.DeleteError = fmt.Sprintf("inspect legacy artifact: %v", err)
		return artifact
	}

	targets := []legacyArtifactActiveTarget{
		{label: "active core database", dsn: v.CoreDatabaseDSN},
		{label: "active log database", dsn: v.LogDatabaseDSN},
	}
	for _, source := range v.ActiveLegacySources {
		targets = append(targets, legacyArtifactActiveTarget{label: "active legacy database", dsn: source})
	}
	for _, target := range targets {
		targetPath, pathErr := sqliteFileAbsolutePath(target.dsn)
		if pathErr != nil {
			artifact.DeleteError = fmt.Sprintf("resolve %s: %v", target.label, pathErr)
			return artifact
		}
		if artifactPath == targetPath {
			artifact.InUse = true
			artifact.DeleteError = fmt.Sprintf("legacy artifact aliases %s", target.label)
			return artifact
		}
		targetInfo, statErr := os.Stat(targetPath)
		if statErr != nil {
			artifact.DeleteError = fmt.Sprintf("inspect %s: %v", target.label, statErr)
			return artifact
		}
		if os.SameFile(artifactInfo, targetInfo) {
			artifact.InUse = true
			artifact.DeleteError = fmt.Sprintf("legacy artifact aliases %s", target.label)
			return artifact
		}
	}

	if !artifact.Available || !artifact.Exists {
		if artifact.LastError != "" {
			artifact.DeleteError = artifact.LastError
		}
		return artifact
	}
	if v.CoreDB == nil {
		artifact.DeleteError = "core database is unavailable"
		return artifact
	}
	if v.LogDB == nil || !v.LogDatabaseReady {
		artifact.DeleteError = "log database is unavailable"
		return artifact
	}
	if err := ValidateLegacyCleanerTargets(v.CoreDB, v.LogDB); err != nil {
		artifact.DeleteError = err.Error()
		return artifact
	}
	artifact.CanDelete = true
	return artifact
}

type legacyArtifactActiveTarget struct {
	label string
	dsn   string
}

func sqliteFileAbsolutePath(raw string) (string, error) {
	parsed, err := ParseSQLiteDSN(raw)
	if err != nil {
		return "", err
	}
	if parsed.Memory {
		return "", fmt.Errorf("database must be file-backed")
	}
	abs, err := filepath.Abs(parsed.FilesystemPath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

type LegacyArtifactDeletionCommand struct {
	FindArtifact   func() (LegacyArtifact, error)
	BuildValidator func() LegacyArtifactDeletionValidator
}

func (c LegacyArtifactDeletionCommand) Delete(confirmation string) error {
	if c.FindArtifact == nil {
		return fmt.Errorf("legacy artifact finder is unavailable")
	}
	if c.BuildValidator == nil {
		return fmt.Errorf("legacy artifact deletion validator is unavailable")
	}
	artifact, err := c.FindArtifact()
	if err != nil {
		return err
	}
	if err := requireDeletableLegacyArtifact(c.BuildValidator().Check(artifact)); err != nil {
		return err
	}
	expectedPath := artifact.Path
	cleaner := LegacyFileCleaner{
		ExpectedPath: expectedPath,
		ValidateArtifactDeletion: func() error {
			freshArtifact, findErr := c.FindArtifact()
			if findErr != nil {
				return findErr
			}
			if freshArtifact.Path != expectedPath {
				return fmt.Errorf("legacy artifact path changed from %q to %q", expectedPath, freshArtifact.Path)
			}
			return requireDeletableLegacyArtifact(c.BuildValidator().Check(freshArtifact))
		},
	}
	return cleaner.DeleteArtifact(expectedPath, confirmation)
}

func requireDeletableLegacyArtifact(artifact LegacyArtifact) error {
	if artifact.CanDelete {
		return nil
	}
	if artifact.DeleteError != "" {
		return errors.New(artifact.DeleteError)
	}
	if artifact.LastError != "" {
		return errors.New(artifact.LastError)
	}
	return fmt.Errorf("legacy artifact is unavailable")
}
