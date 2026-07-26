package database

import (
	"fmt"
	"net/url"
	"os"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type LegacyLayout string

const (
	LegacyLayoutNone     LegacyLayout = "none"
	LegacyLayoutMonolith LegacyLayout = "monolith"
	LegacyLayoutV5Core   LegacyLayout = "v5_core"
)

type LegacyLayoutInfo struct {
	Kind LegacyLayout
	Path string
}

func FindLegacyLayout(dsn string) (LegacyLayoutInfo, error) {
	parsed, err := ParseSQLiteDSN(dsn)
	if err != nil {
		return LegacyLayoutInfo{}, fmt.Errorf("parse legacy database DSN: %w", err)
	}
	if parsed.Memory {
		return LegacyLayoutInfo{}, fmt.Errorf("legacy database must be file-backed")
	}
	info := LegacyLayoutInfo{Kind: LegacyLayoutNone, Path: parsed.FilesystemPath}
	stat, err := os.Stat(parsed.FilesystemPath)
	if os.IsNotExist(err) {
		return info, nil
	}
	if err != nil {
		return LegacyLayoutInfo{}, fmt.Errorf("stat legacy database: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return LegacyLayoutInfo{}, fmt.Errorf("legacy database is not a regular file")
	}

	db, err := openLegacyReadOnly(parsed)
	if err != nil {
		return LegacyLayoutInfo{}, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return LegacyLayoutInfo{}, fmt.Errorf("get legacy database connection: %w", err)
	}
	defer sqlDB.Close()

	marked, err := legacyTableExists(db, "database_layouts")
	if err != nil {
		return LegacyLayoutInfo{}, err
	}
	if marked {
		return findMarkedLegacyLayout(db, info.Path)
	}
	monolith, err := legacyTableExists(db, "usage_logs")
	if err != nil {
		return LegacyLayoutInfo{}, err
	}
	if monolith {
		info.Kind = LegacyLayoutMonolith
		return info, nil
	}
	return LegacyLayoutInfo{}, fmt.Errorf("legacy database layout is unknown: missing database_layouts and usage_logs")
}

func legacyTableExists(db *gorm.DB, name string) (bool, error) {
	var count int64
	if err := db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count).Error; err != nil {
		return false, fmt.Errorf("inspect legacy database layout: %w", err)
	}
	return count == 1, nil
}

func openLegacyReadOnly(parsed ParsedSQLiteDSN) (*gorm.DB, error) {
	query := cloneQuery(parsed.Query)
	query.Set("mode", "ro")
	query.Del("_pragma")
	base := parsed.Base
	if base == parsed.FilesystemPath {
		base = (&url.URL{Scheme: "file", Path: parsed.FilesystemPath}).String()
	}
	dsn := base
	if encoded := query.Encode(); encoded != "" {
		dsn += "?" + encoded
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		return nil, fmt.Errorf("open legacy database read-only: %w", err)
	}
	return db, nil
}

func findMarkedLegacyLayout(db *gorm.DB, path string) (LegacyLayoutInfo, error) {
	var markers []models.DatabaseLayout
	if err := db.Find(&markers).Error; err != nil {
		return LegacyLayoutInfo{}, fmt.Errorf("legacy database layout marker: %w", err)
	}
	if len(markers) != 1 || markers[0].ID != models.DatabaseLayoutID || markers[0].Role != models.DatabaseRoleCore || markers[0].Version != models.DatabaseLayoutVersion {
		return LegacyLayoutInfo{}, fmt.Errorf("legacy database layout marker is not a supported core layout")
	}
	hasBilling, err := legacyTableExists(db, "billing_logs")
	if err != nil {
		return LegacyLayoutInfo{}, err
	}
	if !hasBilling {
		return LegacyLayoutInfo{}, fmt.Errorf("legacy database layout is marked core but missing billing_logs")
	}
	return LegacyLayoutInfo{Kind: LegacyLayoutV5Core, Path: path}, nil
}
