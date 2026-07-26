package models

type BillingProjectionState string

const (
	BillingProjectionPending BillingProjectionState = "pending"
	BillingProjectionApplied BillingProjectionState = "applied"
)

const BillingProjectionBaselineID uint = 1

// BillingProjectionReceipt is the durable projection outbox for one billing
// fact. Pending is inserted in the same transaction as BillingLog; applied is
// published in the same transaction as all core projections.
type BillingProjectionReceipt struct {
	RequestID       string                 `gorm:"primaryKey;size:64" json:"request_id"`
	BillingLogID    uint                   `gorm:"not null;default:0;index:idx_billing_projection_pending,priority:2" json:"billing_log_id"`
	State           BillingProjectionState `gorm:"size:16;not null;default:applied;check:chk_billing_projection_state,state IN ('pending','applied');index:idx_billing_projection_pending,priority:1" json:"state"`
	ProjectedAtUnix int64                  `gorm:"not null" json:"projected_at_unix"`
}

// BillingProjectionBaseline records the billing-log high watermark that
// predates durable receipts. Facts at or below it were projected by the legacy
// live path and must not be replayed after upgrade.
type BillingProjectionBaseline struct {
	ID                      uint `gorm:"primaryKey;autoIncrement:false;not null;check:chk_billing_projection_baseline_singleton,id = 1" json:"id"`
	BillingLogHighWatermark uint `gorm:"not null" json:"billing_log_high_watermark"`
}
