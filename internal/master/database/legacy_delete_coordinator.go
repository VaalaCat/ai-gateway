package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const legacyDeleteMarkerVersion = 1

var ErrLegacyDeleteInProgress = errors.New("legacy delete operation is already in progress")

type legacyDeleteTarget struct {
	Path       string `json:"path"`
	Quarantine string `json:"quarantine"`
}

type legacyDeleteMetadata struct {
	Version   int                  `json:"version"`
	Operation string               `json:"operation"`
	Expected  string               `json:"expected"`
	Targets   []legacyDeleteTarget `json:"targets"`
}

type legacyDeleteCoordinator struct {
	file    *os.File
	path    string
	created bool
}

func acquireLegacyDeleteCoordinator(expected string) (*legacyDeleteCoordinator, error) {
	markerPath := LegacyDeleteMarkerPath(expected)
	fd, err := unix.Open(markerPath, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	created := err == nil
	if errors.Is(err, os.ErrExist) {
		fd, err = unix.Open(markerPath, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("open legacy delete coordinator %q: %w", markerPath, err)
	}
	file := os.NewFile(uintptr(fd), markerPath)
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: coordinator %q is locked", ErrLegacyDeleteInProgress, markerPath)
		}
		return nil, fmt.Errorf("lock legacy delete coordinator %q: %w", markerPath, err)
	}
	coordinator := &legacyDeleteCoordinator{file: file, path: markerPath, created: created}
	if created {
		if err := coordinator.write(legacyIdleMetadata(expected)); err != nil {
			_ = coordinator.close()
			return nil, err
		}
	}
	return coordinator, nil
}

func (c *legacyDeleteCoordinator) close() error {
	if c == nil || c.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(c.file.Fd()), unix.LOCK_UN)
	closeErr := c.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func (c *legacyDeleteCoordinator) read(expected string) (legacyDeleteMetadata, error) {
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return legacyDeleteMetadata{}, fmt.Errorf("seek legacy delete coordinator %q: %w", c.path, err)
	}
	decoder := json.NewDecoder(c.file)
	decoder.DisallowUnknownFields()
	var metadata legacyDeleteMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return legacyDeleteMetadata{}, fmt.Errorf("decode legacy delete coordinator %q: %w", c.path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return legacyDeleteMetadata{}, fmt.Errorf("decode legacy delete coordinator %q: trailing data", c.path)
	}
	if err := validateLegacyDeleteMetadata(expected, metadata); err != nil {
		return legacyDeleteMetadata{}, fmt.Errorf("validate legacy delete coordinator %q: %w", c.path, err)
	}
	return metadata, nil
}

func (c *legacyDeleteCoordinator) write(metadata legacyDeleteMetadata) error {
	if err := c.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate legacy delete coordinator %q: %w", c.path, err)
	}
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek legacy delete coordinator %q: %w", c.path, err)
	}
	if err := json.NewEncoder(c.file).Encode(metadata); err != nil {
		return fmt.Errorf("encode legacy delete coordinator %q: %w", c.path, err)
	}
	if err := c.file.Sync(); err != nil {
		return fmt.Errorf("sync legacy delete coordinator %q: %w", c.path, err)
	}
	return nil
}

func legacyIdleMetadata(expected string) legacyDeleteMetadata {
	return legacyDeleteMetadata{Version: legacyDeleteMarkerVersion, Operation: "idle", Expected: expected}
}

func validateLegacyDeleteMetadata(expected string, metadata legacyDeleteMetadata) error {
	if metadata.Version != legacyDeleteMarkerVersion || metadata.Expected != expected {
		return fmt.Errorf("unexpected marker identity")
	}
	switch metadata.Operation {
	case "idle":
		if len(metadata.Targets) != 0 {
			return fmt.Errorf("idle marker must not contain targets")
		}
		return nil
	case "delete":
	default:
		return fmt.Errorf("unknown operation %q", metadata.Operation)
	}
	want := legacyCandidatePaths(expected)
	if len(metadata.Targets) != len(want) {
		return fmt.Errorf("delete marker target count mismatch")
	}
	for i, target := range metadata.Targets {
		if target.Path != want[i] || !filepath.IsAbs(target.Quarantine) || filepath.Clean(target.Quarantine) != target.Quarantine ||
			filepath.Dir(target.Quarantine) != filepath.Dir(expected) || !strings.HasPrefix(filepath.Base(target.Quarantine), ".legacy-delete-") {
			return fmt.Errorf("invalid delete marker target %d", i)
		}
	}
	return nil
}

func legacyCandidatePaths(expected string) []string {
	if isLegacyArtifactPath(expected) {
		return []string{expected}
	}
	return []string{expected + "-wal", expected + "-shm", expected}
}

func isLegacyArtifactPath(path string) bool {
	return strings.HasSuffix(filepath.Base(path), ".pre-split.bak")
}

func legacyDeleteFilesState(metadata legacyDeleteMetadata) (sourceExists, quarantineExists bool, err error) {
	for _, target := range metadata.Targets {
		exists, statErr := legacyRegularFileExists(target.Path)
		if statErr != nil {
			return false, false, statErr
		}
		sourceExists = sourceExists || exists
		exists, statErr = legacyRegularFileExists(target.Quarantine)
		if statErr != nil {
			return false, false, statErr
		}
		quarantineExists = quarantineExists || exists
	}
	return sourceExists, quarantineExists, nil
}

func legacyRegularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect legacy delete path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("legacy delete path %q must be a regular file", path)
	}
	return true, nil
}

func reserveLegacyQuarantinePath(dir string) (string, error) {
	file, err := os.CreateTemp(dir, ".legacy-delete-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if closeErr != nil || removeErr != nil {
		return "", errors.Join(closeErr, removeErr)
	}
	return path, nil
}

func readLegacyDeleteMetadataForRetry(coordinator *legacyDeleteCoordinator, expected string) (legacyDeleteMetadata, error) {
	return coordinator.read(expected)
}

func reconcileLegacyDelete(expected string, action func() error) error {
	coordinator, err := acquireLegacyDeleteCoordinator(expected)
	if err != nil {
		return err
	}
	defer func() { _ = coordinator.close() }()
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
		if sourceExists {
			if err := coordinator.write(legacyIdleMetadata(expected)); err != nil {
				return err
			}
		}
	}
	if err := action(); err != nil {
		return err
	}
	return coordinator.write(legacyIdleMetadata(expected))
}
