package database

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var requiredFilePragmas = []string{"journal_mode(WAL)", "busy_timeout(30000)", "foreign_keys(1)"}
var requiredMemoryPragmas = []string{"busy_timeout(30000)", "foreign_keys(1)"}

var memorySequence atomic.Uint64

var ErrSameSQLiteFile = errors.New("SQLite paths resolve to same SQLite file")

type Connector struct{}

type ParsedSQLiteDSN struct {
	Base           string
	Query          url.Values
	FilesystemPath string
	Memory         bool
}

func NewConnector() *Connector {
	return &Connector{}
}

func (c *Connector) OpenCore(path string) (*gorm.DB, error) {
	return c.open("core", path)
}

func (c *Connector) OpenLog(path string) (*gorm.DB, error) {
	return c.open("log", path)
}

func (c *Connector) OpenCorePath(path string) (*gorm.DB, error) {
	return c.openPath("core", path, false)
}

func (c *Connector) OpenLogPath(path string) (*gorm.DB, error) {
	return c.openPath("log", path, false)
}

func (c *Connector) OpenExistingCorePath(path string) (*gorm.DB, error) {
	return c.openPath("core", path, true)
}

func (c *Connector) OpenExistingLogPath(path string) (*gorm.DB, error) {
	return c.openPath("log", path, true)
}

func (c *Connector) OpenExistingCore(path string) (*gorm.DB, error) {
	return c.openExisting("core", path)
}

func (c *Connector) OpenExistingLog(path string) (*gorm.DB, error) {
	return c.openExisting("log", path)
}

func (c *Connector) OpenLegacyReadOnly(layout LegacyLayoutInfo) (*gorm.DB, error) {
	if layout.Kind != LegacyLayoutMonolith && layout.Kind != LegacyLayoutV5Core {
		return nil, fmt.Errorf("legacy database layout %q is not readable", layout.Kind)
	}
	parsed, err := ParseSQLiteDSN(layout.Path)
	if err != nil {
		return nil, fmt.Errorf("parse legacy database DSN: %w", err)
	}
	if parsed.Memory {
		return nil, fmt.Errorf("legacy database must be file-backed")
	}
	return openLegacyReadOnly(parsed)
}

func (c *Connector) ReadHistoryMigrationMarker(path string) (models.HistoryMigration, bool, error) {
	parsed, err := ParseSQLiteDSN(path)
	if err != nil {
		return models.HistoryMigration{}, false, fmt.Errorf("parse core database marker DSN: %w", err)
	}
	if parsed.Memory {
		return models.HistoryMigration{}, false, nil
	}
	info, err := os.Stat(parsed.FilesystemPath)
	if errors.Is(err, os.ErrNotExist) {
		return models.HistoryMigration{}, false, nil
	}
	if err != nil {
		return models.HistoryMigration{}, false, fmt.Errorf("stat core database marker source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return models.HistoryMigration{}, false, fmt.Errorf("core database marker source is not a regular file")
	}
	db, err := openLegacyReadOnly(parsed)
	if err != nil {
		return models.HistoryMigration{}, false, fmt.Errorf("open core database marker source: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return models.HistoryMigration{}, false, fmt.Errorf("get core database marker connection: %w", err)
	}
	defer sqlDB.Close()
	return readHistoryMigrationMarker(db)
}

func readHistoryMigrationMarker(db *gorm.DB) (models.HistoryMigration, bool, error) {
	var tables int64
	if err := db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, (models.HistoryMigration{}).TableName()).Scan(&tables).Error; err != nil {
		return models.HistoryMigration{}, false, fmt.Errorf("inspect history migration marker table: %w", err)
	}
	if tables == 0 {
		return models.HistoryMigration{}, false, nil
	}
	var migration models.HistoryMigration
	err := db.First(&migration, models.HistoryMigrationSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.HistoryMigration{}, false, nil
	}
	if err != nil {
		return models.HistoryMigration{}, false, fmt.Errorf("read history migration marker: %w", err)
	}
	return migration, true, nil
}

func (c *Connector) openExisting(kind, path string) (*gorm.DB, error) {
	parsed, err := ParseSQLiteDSN(path)
	if err != nil {
		return nil, fmt.Errorf("parse existing %s database DSN: %w", kind, err)
	}
	if parsed.Memory {
		return nil, fmt.Errorf("existing %s database must be file-backed", kind)
	}
	info, err := os.Stat(parsed.FilesystemPath)
	if err != nil {
		return nil, fmt.Errorf("stat existing %s database: %w", kind, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("existing %s database is not a regular file", kind)
	}
	parsed.Query = cloneQuery(parsed.Query)
	parsed.Query.Set("mode", "rw")
	db, err := gorm.Open(sqlite.Open(parsed.connectionDSN(kind)), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open existing %s database: %w", kind, err)
	}
	return db, nil
}

func (c *Connector) openPath(kind, path string, existing bool) (*gorm.DB, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s database filesystem path must be absolute", kind)
	}
	if existing {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat existing %s database: %w", kind, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("existing %s database is not a regular file", kind)
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s database directory: %w", kind, err)
	}
	u := url.URL{Scheme: "file", Path: path}
	parsed := ParsedSQLiteDSN{Base: u.String(), Query: make(url.Values), FilesystemPath: path}
	if existing {
		parsed.Query.Set("mode", "rw")
	} else {
		parsed.Query.Set("mode", "rwc")
	}
	db, err := gorm.Open(sqlite.Open(parsed.connectionDSN(kind)), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open %s database filesystem path: %w", kind, err)
	}
	return db, nil
}

func (c *Connector) open(kind, path string) (*gorm.DB, error) {
	parsed, err := ParseSQLiteDSN(path)
	if err != nil {
		return nil, fmt.Errorf("parse %s database DSN: %w", kind, err)
	}
	dsn := parsed.connectionDSN(kind)
	if !parsed.Memory {
		if err := os.MkdirAll(filepath.Dir(parsed.FilesystemPath), 0o755); err != nil {
			return nil, fmt.Errorf("create %s database directory: %w", kind, err)
		}
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", kind, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get %s sql database: %w", kind, err)
	}
	if parsed.Memory {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}
	return db, nil
}

func ValidatePaths(corePath, logPath string) error {
	coreDSN, err := ParseSQLiteDSN(corePath)
	if err != nil {
		return fmt.Errorf("parse core SQLite DSN: %w", err)
	}
	logDSN, err := ParseSQLiteDSN(logPath)
	if err != nil {
		return fmt.Errorf("parse log SQLite DSN: %w", err)
	}
	if coreDSN.Memory || logDSN.Memory {
		return nil
	}
	core, err := normalizedPath(coreDSN.FilesystemPath)
	if err != nil {
		return fmt.Errorf("normalize core SQLite path: %w", err)
	}
	log, err := normalizedPath(logDSN.FilesystemPath)
	if err != nil {
		return fmt.Errorf("normalize log SQLite path: %w", err)
	}
	coreTarget, err := canonicalTargetPath(core)
	if err != nil {
		return fmt.Errorf("resolve core SQLite path: %w", err)
	}
	logTarget, err := canonicalTargetPath(log)
	if err != nil {
		return fmt.Errorf("resolve log SQLite path: %w", err)
	}
	if coreTarget == logTarget {
		return fmt.Errorf("%w: %s", ErrSameSQLiteFile, core)
	}
	coreInfo, coreErr := os.Stat(core)
	logInfo, logErr := os.Stat(log)
	if coreErr == nil && logErr == nil && os.SameFile(coreInfo, logInfo) {
		return ErrSameSQLiteFile
	}
	return nil
}

func CheckIntegrity(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("SQLite database is nil")
	}
	var result string
	if err := db.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil {
		return fmt.Errorf("check SQLite integrity: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	return nil
}

func QuickCheck(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("SQLite database is nil")
	}
	var result string
	if err := db.Raw("PRAGMA quick_check").Scan(&result).Error; err != nil {
		return fmt.Errorf("quick check SQLite: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("SQLite quick check failed: %s", result)
	}
	return nil
}

func ParseSQLiteDSN(raw string) (ParsedSQLiteDSN, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ParsedSQLiteDSN{}, fmt.Errorf("SQLite DSN file path is empty")
	}
	base, rawQuery, _ := strings.Cut(trimmed, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ParsedSQLiteDSN{}, fmt.Errorf("parse query: %w", err)
	}

	parsed := ParsedSQLiteDSN{Base: base, Query: query}
	if base == ":memory:" {
		parsed.Memory = true
		return parsed, nil
	}

	if strings.HasPrefix(strings.ToLower(base), "file:") {
		uri, err := url.Parse(base)
		if err != nil {
			return ParsedSQLiteDSN{}, fmt.Errorf("parse file URI: %w", err)
		}
		if uri.Opaque != "" {
			parsed.FilesystemPath, err = url.PathUnescape(uri.Opaque)
		} else {
			parsed.FilesystemPath = uri.Path
		}
		if err != nil {
			return ParsedSQLiteDSN{}, fmt.Errorf("decode file URI path: %w", err)
		}
	} else {
		parsed.FilesystemPath = base
	}
	if strings.TrimSpace(parsed.FilesystemPath) == "" {
		return ParsedSQLiteDSN{}, fmt.Errorf("SQLite DSN file path is empty")
	}

	parsed.Memory = parsed.FilesystemPath == ":memory:" || query.Get("mode") == "memory"
	if parsed.Memory {
		parsed.FilesystemPath = ""
	}
	return parsed, nil
}

func (d ParsedSQLiteDSN) connectionDSN(kind string) string {
	base := d.Base
	query := cloneQuery(d.Query)
	if d.Memory {
		base = fmt.Sprintf("file:ai-gateway-%s-%d", kind, memorySequence.Add(1))
		query.Set("mode", "memory")
		query.Set("cache", "shared")
	}
	query = withoutConflictingPragmas(query)
	pragmas := requiredFilePragmas
	if d.Memory {
		pragmas = requiredMemoryPragmas
	}
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	if encoded := query.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

func cloneQuery(query url.Values) url.Values {
	cloned := make(url.Values, len(query))
	for key, values := range query {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func withoutConflictingPragmas(query url.Values) url.Values {
	cleaned := make(url.Values, len(query))
	for key, values := range query {
		if !strings.EqualFold(key, "_pragma") {
			cleaned[key] = append([]string(nil), values...)
			continue
		}
		for _, value := range values {
			if !isRequiredPragma(value) {
				cleaned[key] = append(cleaned[key], value)
			}
		}
	}
	return cleaned
}

func isRequiredPragma(value string) bool {
	name := strings.ToLower(strings.TrimSpace(value))
	if i := strings.IndexAny(name, "=("); i >= 0 {
		name = name[:i]
	}
	switch name {
	case "journal_mode", "busy_timeout", "foreign_keys":
		return true
	default:
		return false
	}
}

func normalizedPath(path string) (string, error) {
	return filepath.Abs(filepath.Clean(path))
}

func canonicalTargetPath(path string) (string, error) {
	current := path
	var missing []string
	for {
		if _, err := os.Stat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
