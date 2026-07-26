package models

// LogHistoryAggregateMerge makes cutover-hour delta merges idempotent across restarts.
type LogHistoryAggregateMerge struct {
	ID          uint   `gorm:"primaryKey"`
	SourceTable string `gorm:"size:64;uniqueIndex:idx_log_history_aggregate_merge,priority:1"`
	SourceKey   string `gorm:"size:255;uniqueIndex:idx_log_history_aggregate_merge,priority:2"`
	CreatedAt   int64  `gorm:"autoCreateTime"`
}

func (LogHistoryAggregateMerge) TableName() string { return "log_history_aggregate_merges" }
