package models

const (
	DatabaseLayoutVersion = 1
	DatabaseLayoutID      = 1
	DatabaseRoleCore      = "core"
	DatabaseRoleLog       = "log"
)

type DatabaseLayout struct {
	ID      uint   `gorm:"primaryKey;autoIncrement:false;not null;check:chk_database_layout_singleton,id = 1" json:"id"`
	Role    string `gorm:"size:8;not null;uniqueIndex;check:chk_database_layout_role,role IN ('core','log')" json:"role"`
	Version int    `gorm:"not null" json:"version"`
}
