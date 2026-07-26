package models

const HistoryMigrationSingletonID uint = 1

type HistoryMigration struct {
	ID                   uint   `gorm:"primaryKey;autoIncrement:false"`
	SourceKind           string `gorm:"not null"`
	SourcePath           string `gorm:"not null"`
	State                string `gorm:"not null;index"`
	SkipLogHistory       bool   `gorm:"not null;default:false"`
	LastError            string
	StartedAtUnix        int64
	LastSuccessfulAtUnix int64
	CompletedAtUnix      int64
	SourceDeletedAtUnix  int64
}

func (HistoryMigration) TableName() string { return "history_migrations" }

type HistoryCursor struct {
	Key           string `gorm:"primaryKey;size:64"`
	LastSourceID  uint   `gorm:"not null"`
	ProcessedRows int64  `gorm:"not null"`
	Skipped       bool   `gorm:"not null;default:false"`
	UpdatedAtUnix int64  `gorm:"not null"`
}

func (HistoryCursor) TableName() string { return "history_cursors" }
