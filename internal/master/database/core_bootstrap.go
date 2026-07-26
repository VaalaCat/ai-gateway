package database

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type BootstrapResult struct {
	Created    bool
	SourceKind LegacyLayout
	CopiedRows int64
}

type BootstrapOptions struct {
	AfterTable func(string) error
	Now        func() time.Time
}

func BootstrapCore(ctx context.Context, source, target *gorm.DB, layout LegacyLayoutInfo, options BootstrapOptions) (BootstrapResult, error) {
	result := BootstrapResult{SourceKind: layout.Kind}
	if source == nil {
		return result, fmt.Errorf("bootstrap source database is nil")
	}
	if target == nil {
		return result, fmt.Errorf("bootstrap target database is nil")
	}
	if layout.Kind != LegacyLayoutMonolith && layout.Kind != LegacyLayoutV5Core {
		return result, fmt.Errorf("bootstrap legacy layout %q is not copyable", layout.Kind)
	}
	if err := requireDifferentSQLiteFiles(source, target); err != nil {
		return result, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	err := target.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var markers int64
		if err := tx.Model(&models.HistoryMigration{}).Where("id = ?", models.HistoryMigrationSingletonID).Count(&markers).Error; err != nil {
			return fmt.Errorf("count history migration marker: %w", err)
		}
		if markers != 0 {
			return nil
		}
		if err := requireEmptyBootstrapTarget(ctx, tx); err != nil {
			return err
		}
		for _, copier := range bootstrapTableCopiers() {
			rows, err := copier.copyAll(ctx, source, tx)
			if err != nil {
				return fmt.Errorf("copy bootstrap table %s: %w", copier.name, err)
			}
			result.CopiedRows += rows
			if options.AfterTable != nil {
				if err := options.AfterTable(copier.name); err != nil {
					return fmt.Errorf("after bootstrap table %s: %w", copier.name, err)
				}
			}
		}
		marker := models.HistoryMigration{
			ID:            models.HistoryMigrationSingletonID,
			SourceKind:    string(layout.Kind),
			SourcePath:    layout.Path,
			State:         "pending",
			StartedAtUnix: options.Now().Unix(),
		}
		if err := tx.Create(&marker).Error; err != nil {
			return fmt.Errorf("create history migration marker: %w", err)
		}
		result.Created = true
		return nil
	})
	if err != nil {
		result.Created = false
		result.CopiedRows = 0
		return result, err
	}
	return result, nil
}

func InitializeFreshCore(ctx context.Context, target *gorm.DB, now func() time.Time) error {
	if target == nil {
		return fmt.Errorf("fresh core database is nil")
	}
	if now == nil {
		now = time.Now
	}
	return target.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var markers int64
		if err := tx.Model(&models.HistoryMigration{}).Where("id = ?", models.HistoryMigrationSingletonID).Count(&markers).Error; err != nil {
			return fmt.Errorf("count history migration marker: %w", err)
		}
		if markers != 0 {
			return nil
		}
		if err := requireEmptyBootstrapTarget(ctx, tx); err != nil {
			return err
		}
		startedAt := now().Unix()
		return tx.Create(&models.HistoryMigration{
			ID: models.HistoryMigrationSingletonID, SourceKind: string(LegacyLayoutNone),
			State: "completed", StartedAtUnix: startedAt, CompletedAtUnix: startedAt,
		}).Error
	})
}

func requireEmptyBootstrapTarget(ctx context.Context, target *gorm.DB) error {
	for _, copier := range bootstrapTableCopiers() {
		var rows int64
		if err := target.WithContext(ctx).Table(copier.name).Count(&rows).Error; err != nil {
			return fmt.Errorf("inspect bootstrap target table %s: %w", copier.name, err)
		}
		if rows != 0 {
			return fmt.Errorf("refuse to overwrite bootstrap target: table %s contains %d rows", copier.name, rows)
		}
	}
	return nil
}

func requireDifferentSQLiteFiles(source, target *gorm.DB) error {
	if source == target {
		return fmt.Errorf("bootstrap source and target must use different SQLite files")
	}
	sourcePath, sourceFileBacked, err := mainSQLiteFile(source)
	if err != nil {
		return fmt.Errorf("inspect bootstrap source SQLite file: %w", err)
	}
	targetPath, targetFileBacked, err := mainSQLiteFile(target)
	if err != nil {
		return fmt.Errorf("inspect bootstrap target SQLite file: %w", err)
	}
	if !sourceFileBacked || !targetFileBacked {
		return nil
	}
	sourcePath, err = normalizedPath(sourcePath)
	if err != nil {
		return fmt.Errorf("normalize bootstrap source SQLite file: %w", err)
	}
	targetPath, err = normalizedPath(targetPath)
	if err != nil {
		return fmt.Errorf("normalize bootstrap target SQLite file: %w", err)
	}
	canonicalSource, err := canonicalTargetPath(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve bootstrap source SQLite file: %w", err)
	}
	canonicalTarget, err := canonicalTargetPath(targetPath)
	if err != nil {
		return fmt.Errorf("resolve bootstrap target SQLite file: %w", err)
	}
	sameFile := canonicalSource == canonicalTarget
	if !sameFile {
		sourceInfo, sourceErr := os.Stat(sourcePath)
		targetInfo, targetErr := os.Stat(targetPath)
		sameFile = sourceErr == nil && targetErr == nil && os.SameFile(sourceInfo, targetInfo)
	}
	if sameFile {
		return fmt.Errorf("bootstrap source and target must use different SQLite files")
	}
	return nil
}

func mainSQLiteFile(db *gorm.DB) (string, bool, error) {
	var databases []struct {
		Name string `gorm:"column:name"`
		File string `gorm:"column:file"`
	}
	if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return "", false, err
	}
	for _, database := range databases {
		if database.Name == "main" {
			return database.File, database.File != "", nil
		}
	}
	return "", false, fmt.Errorf("main SQLite database is missing")
}
