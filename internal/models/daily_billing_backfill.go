package models

type DailyBillingBackfillState string

const (
	DailyBillingBackfillPending   DailyBillingBackfillState = "pending"
	DailyBillingBackfillRunning   DailyBillingBackfillState = "running"
	DailyBillingBackfillFailed    DailyBillingBackfillState = "failed"
	DailyBillingBackfillCompleted DailyBillingBackfillState = "completed"

	DailyBillingBackfillVersion uint = 1
)

type DailyBillingBackfill struct {
	Version           uint                      `gorm:"primaryKey;autoIncrement:false;not null" json:"version"`
	State             DailyBillingBackfillState `gorm:"size:16;not null;check:chk_daily_billing_backfill_state,state IN ('pending','running','failed','completed')" json:"state"`
	StartDate         string                    `gorm:"size:10" json:"start_date"`
	EndDate           string                    `gorm:"size:10" json:"end_date"`
	LastCompletedDate string                    `gorm:"size:10" json:"last_completed_date"`
	LastError         string                    `gorm:"type:text" json:"last_error"`
	StartedAtUnix     int64                     `gorm:"not null" json:"started_at_unix"`
	CompletedAtUnix   int64                     `gorm:"not null" json:"completed_at_unix"`
	UpdatedAtUnix     int64                     `gorm:"not null" json:"updated_at_unix"`
}

func (DailyBillingBackfill) TableName() string { return "daily_billing_backfills" }
