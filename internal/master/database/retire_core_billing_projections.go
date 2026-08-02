package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

var errCoreBillingRetirementLogUnavailable = errors.New("log database unavailable for core billing retirement")

var retiredCoreBillingProjectionModels = []any{
	&models.BillingProjectionReceipt{},
	&models.BillingProjectionBaseline{},
	&models.BillingHourlyBucket{},
	&models.TokenDailyBilling{},
	&models.ChannelDailyBilling{},
}

// RetireCoreBillingProjectionTables removes obsolete core rollups only after
// the version 1 log-owned daily backfill has durably completed.
func RetireCoreBillingProjectionTables(ctx context.Context, core, logDB *gorm.DB) (bool, error) {
	return retireCoreBillingProjectionTablesWithDropTable(ctx, core, logDB, func(tx *gorm.DB, model any) error {
		return tx.Migrator().DropTable(model)
	})
}

func retireCoreBillingProjectionTablesWithDropTable(
	ctx context.Context,
	core, logDB *gorm.DB,
	dropTable func(tx *gorm.DB, model any) error,
) (bool, error) {
	if core == nil {
		return false, nil
	}
	if logDB == nil {
		return false, errCoreBillingRetirementLogUnavailable
	}
	var marker models.DailyBillingBackfill
	err := logDB.WithContext(ctx).
		Where("version = ?", models.DailyBillingBackfillVersion).
		First(&marker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read daily billing backfill marker: %w", err)
	}
	if marker.State != models.DailyBillingBackfillCompleted {
		return false, nil
	}

	dropped := false
	err = core.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range retiredCoreBillingProjectionModels {
			if !tx.Migrator().HasTable(model) {
				continue
			}
			if err := dropTable(tx, model); err != nil {
				return err
			}
			dropped = true
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("drop retired core billing tables: %w", err)
	}
	return dropped, nil
}
