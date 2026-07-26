package master

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/config"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterhistorybackfill "github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type splitDatabases struct {
	core *gorm.DB
	log  *gorm.DB
}

type databasePreflight struct {
	hasMigration bool
}

func prepareDatabases(ctx context.Context, cfg *config.MasterConfig, connector *masterdatabase.Connector, logger *zap.Logger) (splitDatabases, error) {
	preflight, err := inspectDatabaseFilesBeforeWrite(cfg, connector)
	if err != nil {
		return splitDatabases{}, err
	}
	coreDSN, _ := masterdatabase.ParseSQLiteDSN(cfg.DBPath)
	if coreDSN.Memory {
		return createMemorySplitDatabases(ctx, connector, cfg)
	}
	core, err := openAndMigrateCore(connector, cfg.DBPath)
	if err != nil {
		return splitDatabases{}, err
	}
	if err := initializeCoreHistory(ctx, connector, core, cfg.LegacyDBPath, preflight.hasMigration); err != nil {
		closeGormDatabase(core)
		return splitDatabases{}, err
	}
	logDB, err := openAndMigrateLog(connector, cfg.LogDBPath)
	if err != nil && isLayoutRoleConflict(err) {
		closeGormDatabase(core)
		return splitDatabases{}, err
	}
	if err != nil {
		logger.Warn("log_database_startup_degraded", zap.Error(err), zap.String("path", cfg.LogDBPath))
	}
	return splitDatabases{core: core, log: logDB}, nil
}

func inspectDatabaseFilesBeforeWrite(cfg *config.MasterConfig, connector *masterdatabase.Connector) (databasePreflight, error) {
	result := databasePreflight{}
	if err := masterdatabase.ValidatePaths(cfg.DBPath, cfg.LogDBPath); err != nil {
		return result, err
	}
	var legacyMetadataErr error
	for _, activePath := range []string{cfg.DBPath, cfg.LogDBPath} {
		err := masterdatabase.ValidatePaths(activePath, cfg.LegacyDBPath)
		if err == nil {
			continue
		}
		if errors.Is(err, masterdatabase.ErrSameSQLiteFile) {
			return result, err
		}
		legacyMetadataErr = errors.Join(legacyMetadataErr, err)
	}
	migration, found, err := connector.ReadHistoryMigrationMarker(cfg.DBPath)
	if err != nil {
		return result, err
	}
	result.hasMigration = found
	if legacyMetadataErr != nil && !found {
		return result, legacyMetadataErr
	}
	if found && historyMigrationNeedsSource(migration) {
		for _, activePath := range []string{cfg.DBPath, cfg.LogDBPath} {
			if err := masterdatabase.ValidatePaths(activePath, migration.SourcePath); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func historyMigrationNeedsSource(migration models.HistoryMigration) bool {
	return migration.SourceKind != string(masterdatabase.LegacyLayoutNone) &&
		migration.State != string(masterhistorybackfill.StateCompleted) &&
		migration.State != string(masterhistorybackfill.StateSourceDeleted)
}

func initializeCoreHistory(ctx context.Context, connector *masterdatabase.Connector, core *gorm.DB, legacyPath string, hasMigration bool) error {
	if hasMigration {
		return nil
	}
	legacy, err := masterdatabase.FindLegacyLayout(legacyPath)
	if err != nil {
		return err
	}
	if legacy.Kind == masterdatabase.LegacyLayoutNone {
		return masterdatabase.InitializeFreshCore(ctx, core, nil)
	}
	source, err := connector.OpenLegacyReadOnly(legacy)
	if err != nil {
		return err
	}
	defer closeGormDatabase(source)
	_, err = masterdatabase.BootstrapCore(ctx, source, core, legacy, masterdatabase.BootstrapOptions{})
	return err
}

func newHistoryBackfillWorker(cfg *config.MasterConfig, application app.Application, connector *masterdatabase.Connector, logger *zap.Logger) (*masterhistorybackfill.Worker, error) {
	core := application.GetCoreDB()
	var migration models.HistoryMigration
	if err := core.First(&migration, models.HistoryMigrationSingletonID).Error; err != nil {
		return nil, fmt.Errorf("read history migration marker: %w", err)
	}
	var openLegacyReader masterhistorybackfill.LegacyReaderOpener
	if migration.SourceKind != string(masterdatabase.LegacyLayoutNone) && migration.State != string(masterhistorybackfill.StateSourceDeleted) && migration.State != string(masterhistorybackfill.StateCompleted) {
		layout := masterdatabase.LegacyLayoutInfo{Kind: masterdatabase.LegacyLayout(migration.SourceKind), Path: migration.SourcePath}
		openLegacyReader = func() (*masterhistorybackfill.LegacyReader, func() error, error) {
			return openHistoryLegacyReader(connector, layout, cfg.DBPath, cfg.LogDBPath)
		}
	}
	backfiller := masterhistorybackfill.New(masterhistorybackfill.Options{
		CoreDBFinder: application.GetCoreDB, LogDBFinder: application.GetLogDB,
		BillingHistoryWriter: &masterhistorybackfill.BillingBatchWriter{DBFinder: application.GetCoreDB},
		RequestHistoryWriter: &masterlogqueue.HistoricalRequestBatchWriter{DBFinder: application.GetLogDB},
		Logger:               logger,
	})
	return masterhistorybackfill.NewWorker(masterhistorybackfill.WorkerOptions{Backfiller: backfiller, OpenLegacyReader: openLegacyReader}), nil
}

func openHistoryLegacyReader(connector *masterdatabase.Connector, expected masterdatabase.LegacyLayoutInfo, activePaths ...string) (*masterhistorybackfill.LegacyReader, func() error, error) {
	for _, activePath := range activePaths {
		if err := masterdatabase.ValidatePaths(activePath, expected.Path); err != nil {
			return nil, nil, err
		}
	}
	actual, err := masterdatabase.FindLegacyLayout(expected.Path)
	if err != nil {
		return nil, nil, err
	}
	if actual.Kind != expected.Kind {
		return nil, nil, fmt.Errorf("legacy history layout mismatch: marker requires %q, source is %q", expected.Kind, actual.Kind)
	}
	source, err := connector.OpenLegacyReadOnly(actual)
	if err != nil {
		return nil, nil, err
	}
	closeLegacy := func() error {
		sqlDB, dbErr := source.DB()
		if dbErr != nil {
			return dbErr
		}
		return sqlDB.Close()
	}
	return masterhistorybackfill.NewLegacyReader(source, actual.Kind), closeLegacy, nil
}

func createMemorySplitDatabases(ctx context.Context, connector *masterdatabase.Connector, cfg *config.MasterConfig) (splitDatabases, error) {
	core, err := openAndMigrateCore(connector, cfg.DBPath)
	if err != nil {
		return splitDatabases{}, err
	}
	if err := masterdatabase.InitializeFreshCore(ctx, core, nil); err != nil {
		closeGormDatabase(core)
		return splitDatabases{}, err
	}
	logDB, err := openAndMigrateLog(connector, cfg.LogDBPath)
	if err != nil {
		closeGormDatabase(core)
		return splitDatabases{}, err
	}
	return splitDatabases{core: core, log: logDB}, nil
}

func openAndMigrateCore(connector *masterdatabase.Connector, path string) (*gorm.DB, error) {
	db, err := openCoreDatabase(connector, path)
	if err != nil {
		return nil, err
	}
	if err := models.MigrateCoreDB(db); err != nil {
		closeGormDatabase(db)
		return nil, err
	}
	return db, nil
}

func openAndMigrateLog(connector *masterdatabase.Connector, path string) (*gorm.DB, error) {
	db, err := openLogDatabase(connector, path)
	if err != nil {
		return nil, err
	}
	if err := models.MigrateLogDB(db); err != nil {
		closeGormDatabase(db)
		return nil, err
	}
	return db, nil
}

func isLayoutRoleConflict(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "layout role")
}

func closeGormDatabase(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
