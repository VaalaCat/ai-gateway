package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type LegacyArtifact struct {
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	Exists      bool   `json:"exists"`
	InUse       bool   `json:"in_use"`
	Available   bool   `json:"available"`
	LastError   string `json:"last_error"`
	CanDelete   bool   `json:"can_delete"`
	DeleteError string `json:"delete_error"`
}

func SplitManifestPath(legacyPath string) string {
	return legacyPath + ".split-manifest.json"
}

func FindLegacyArtifact(legacyDBPath string) (LegacyArtifact, error) {
	manifestPath := SplitManifestPath(legacyDBPath)
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return LegacyArtifact{Available: true}, nil
	}
	if err != nil {
		return unavailableLegacyArtifact(fmt.Errorf("read legacy split manifest: %w", err))
	}
	var manifest struct {
		Paths struct {
			BackupCore string `json:"backup_core"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return unavailableLegacyArtifact(fmt.Errorf("decode legacy split manifest: %w", err))
	}
	path, err := validatedLegacyArtifactPath(manifest.Paths.BackupCore)
	if err != nil {
		return unavailableLegacyArtifact(err)
	}
	artifact := LegacyArtifact{Path: path, Available: true}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return artifact, nil
	}
	if err != nil {
		return unavailableLegacyArtifactAt(artifact, fmt.Errorf("stat legacy artifact: %w", err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		artifact.Exists = true
		artifact.SizeBytes = info.Size()
		return unavailableLegacyArtifactAt(artifact, fmt.Errorf("legacy artifact must be a regular file"))
	}
	artifact.Exists = true
	artifact.SizeBytes = info.Size()
	return artifact, nil
}

func unavailableLegacyArtifact(err error) (LegacyArtifact, error) {
	return unavailableLegacyArtifactAt(LegacyArtifact{}, err)
}

func unavailableLegacyArtifactAt(artifact LegacyArtifact, err error) (LegacyArtifact, error) {
	artifact.Available = false
	artifact.LastError = err.Error()
	return artifact, err
}

func validatedLegacyArtifactPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("legacy split manifest backup path is empty")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("legacy artifact path must be absolute and clean")
	}
	if !strings.HasSuffix(filepath.Base(path), ".pre-split.bak") {
		return "", fmt.Errorf("legacy artifact path must end with .pre-split.bak")
	}
	return path, nil
}
