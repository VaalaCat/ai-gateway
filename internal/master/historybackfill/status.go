package historybackfill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type State string

const (
	StatePending       State = "pending"
	StateCopying       State = "copying"
	StateCaughtUp      State = "caught_up"
	StateDegraded      State = "degraded"
	StateCompleted     State = "completed"
	StateSourceDeleted State = "source_deleted"
)

type CursorStatus struct {
	LastSourceID  uint  `json:"last_source_id"`
	ProcessedRows int64 `json:"processed_rows"`
	Skipped       bool  `json:"skipped"`
}

type Status struct {
	State                State        `json:"state"`
	SourceKind           string       `json:"source_kind"`
	SourcePath           string       `json:"source_path"`
	SourceSizeBytes      int64        `json:"source_size_bytes"`
	Billing              CursorStatus `json:"billing"`
	Requests             CursorStatus `json:"requests"`
	Traces               CursorStatus `json:"traces"`
	RowsPerSecond        float64      `json:"rows_per_second"`
	LastError            string       `json:"last_error"`
	LastSuccessfulAtUnix int64        `json:"last_successful_at_unix"`
	CanComplete          bool         `json:"can_complete"`
	CanDeleteSource      bool         `json:"can_delete_source"`
}

func (w *Worker) Status() Status {
	if w == nil || w.backfiller == nil {
		return unavailableStatus("history backfill worker is unavailable")
	}
	core, err := findHistoryDB(w.backfiller.options.CoreDBFinder, "core")
	if err != nil {
		return unavailableStatus(err.Error())
	}
	var migration models.HistoryMigration
	if err := core.First(&migration, models.HistoryMigrationSingletonID).Error; err != nil {
		return unavailableStatus(fmt.Sprintf("read history migration: %v", err))
	}
	status := Status{
		State: State(migration.State), SourceKind: migration.SourceKind, SourcePath: migration.SourcePath,
		LastError: migration.LastError, LastSuccessfulAtUnix: migration.LastSuccessfulAtUnix,
	}
	if info, statErr := os.Stat(migration.SourcePath); statErr == nil {
		status.SourceSizeBytes = info.Size()
	}
	status.Billing, err = readCursorStatus(core, billingCursorKey)
	if err != nil {
		return degradedStatus(status, err)
	}
	if sourceHasLogHistory(migration.SourceKind) {
		logDB, logErr := findHistoryDB(w.backfiller.options.LogDBFinder, "log")
		if logErr != nil {
			return degradedStatus(status, logErr)
		}
		status.Requests, err = readCursorStatus(logDB, requestCursorKey)
		if err != nil {
			return degradedStatus(status, err)
		}
		status.Traces, err = readCursorStatus(logDB, traceCursorKey)
		if err != nil {
			return degradedStatus(status, err)
		}
	}
	w.mu.Lock()
	status.RowsPerSecond = finiteRate(w.rowsPerSecond)
	batchRunning := w.batchRunning
	if w.runtimeDegraded {
		status.State = StateDegraded
		status.LastError = w.runtimeLastError
	}
	w.mu.Unlock()
	status.CanComplete = status.State == StateCaughtUp
	status.CanDeleteSource = status.State == StateCompleted && status.SourceSizeBytes > 0 && !batchRunning
	return status
}

func (w *Worker) BatchRunning() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.batchRunning
}

func unavailableStatus(message string) Status {
	return Status{State: StateDegraded, LastError: message}
}

func degradedStatus(status Status, err error) Status {
	status.State = StateDegraded
	status.LastError = err.Error()
	status.CanComplete = false
	status.CanDeleteSource = false
	return status
}

func finiteRate(rate float64) float64 {
	if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	return rate
}

func sourceHasLogHistory(kind string) bool {
	return kind == string(masterdatabase.LegacyLayoutMonolith)
}

func findHistoryDB(finder func() *gorm.DB, name string) (*gorm.DB, error) {
	if finder == nil {
		return nil, fmt.Errorf("history backfill %s database is unavailable", name)
	}
	db := finder()
	if db == nil {
		return nil, fmt.Errorf("history backfill %s database is unavailable", name)
	}
	return db, nil
}

func readCursorStatus(db *gorm.DB, key string) (CursorStatus, error) {
	var cursor models.HistoryCursor
	err := db.Where("key = ?", key).First(&cursor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CursorStatus{}, nil
	}
	if err != nil {
		return CursorStatus{}, fmt.Errorf("read history cursor %q: %w", key, err)
	}
	return cursorStatusFrom(cursor), nil
}

func cursorStatusFrom(cursor models.HistoryCursor) CursorStatus {
	return CursorStatus{
		LastSourceID: cursor.LastSourceID, ProcessedRows: cursor.ProcessedRows, Skipped: cursor.Skipped,
	}
}

func (w *Worker) updateMigration(ctx context.Context, values map[string]any) error {
	core, err := findHistoryDB(w.backfiller.options.CoreDBFinder, "core")
	if err != nil {
		return err
	}
	result := core.WithContext(ctx).Model(&models.HistoryMigration{}).
		Where("id = ?", models.HistoryMigrationSingletonID).Updates(values)
	if result.Error != nil {
		return fmt.Errorf("update history migration: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update history migration: singleton row is unavailable")
	}
	return nil
}

func (w *Worker) now() time.Time {
	if w.nowTime != nil {
		return w.nowTime()
	}
	return time.Now()
}
