package database

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var retiredCoreBillingTables = []any{
	&models.BillingProjectionReceipt{},
	&models.BillingProjectionBaseline{},
	&models.BillingHourlyBucket{},
	&models.TokenDailyBilling{},
	&models.ChannelDailyBilling{},
}

func openRetirementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func seedLegacyCoreBillingTables(t *testing.T, core *gorm.DB) {
	t.Helper()
	require.NoError(t, core.AutoMigrate(retiredCoreBillingTables...))
}

func assertRetiredCoreTablesExist(t *testing.T, core *gorm.DB, want bool) {
	t.Helper()
	for _, table := range retiredCoreBillingTables {
		require.Equalf(t, want, core.Migrator().HasTable(table), "unexpected ownership for %T", table)
	}
}

func TestRetireCoreBillingProjectionTablesRequiresCompletedDailyBackfill(t *testing.T) {
	for _, test := range []struct {
		name   string
		marker *models.DailyBillingBackfill
	}{
		{name: "no marker"},
		{name: "pending", marker: &models.DailyBillingBackfill{Version: 1, State: models.DailyBillingBackfillPending}},
		{name: "running", marker: &models.DailyBillingBackfill{Version: 1, State: models.DailyBillingBackfillRunning}},
		{name: "failed", marker: &models.DailyBillingBackfill{Version: 1, State: models.DailyBillingBackfillFailed}},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, logDB := openRetirementTestDB(t), openRetirementTestDB(t)
			seedLegacyCoreBillingTables(t, core)
			require.NoError(t, models.MigrateLogDB(logDB))
			if test.marker != nil {
				test.marker.StartedAtUnix = 1
				test.marker.UpdatedAtUnix = 1
				require.NoError(t, logDB.Create(test.marker).Error)
			}

			dropped, err := RetireCoreBillingProjectionTables(t.Context(), core, logDB)
			require.NoError(t, err)
			require.False(t, dropped)
			assertRetiredCoreTablesExist(t, core, true)
		})
	}
}

func TestRetireCoreBillingProjectionTablesDropsInDependencyOrderAndIsIdempotent(t *testing.T) {
	core, logDB := openRetirementTestDB(t), openRetirementTestDB(t)
	seedLegacyCoreBillingTables(t, core)
	require.NoError(t, core.AutoMigrate(&models.BillingLog{}))
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "keep-billing-fact", CreatedAt: 1}).Error)
	require.NoError(t, models.MigrateLogDB(logDB))
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: 1, State: models.DailyBillingBackfillCompleted,
		StartedAtUnix: 1, CompletedAtUnix: 2, UpdatedAtUnix: 2,
	}).Error)

	var droppedTables []string
	dropped, err := retireCoreBillingProjectionTablesWithDropTable(t.Context(), core, logDB, func(tx *gorm.DB, model any) error {
		statement := &gorm.Statement{DB: tx}
		require.NoError(t, statement.Parse(model))
		droppedTables = append(droppedTables, statement.Schema.Table)
		return tx.Migrator().DropTable(model)
	})
	require.NoError(t, err)
	require.True(t, dropped)
	require.Equal(t, []string{
		"billing_projection_receipts",
		"billing_projection_baselines",
		"billing_hourly_buckets",
		"token_daily_billings",
		"channel_daily_billings",
	}, droppedTables)
	assertRetiredCoreTablesExist(t, core, false)
	var billingFacts int64
	require.NoError(t, core.Model(&models.BillingLog{}).Count(&billingFacts).Error)
	require.Equal(t, int64(1), billingFacts)

	dropped, err = RetireCoreBillingProjectionTables(t.Context(), core, logDB)
	require.NoError(t, err)
	require.False(t, dropped)
	assertRetiredCoreTablesExist(t, core, false)
}

func TestRetireCoreBillingProjectionTablesKeepsCoreWhenLogUnavailable(t *testing.T) {
	core := openRetirementTestDB(t)
	seedLegacyCoreBillingTables(t, core)

	dropped, err := RetireCoreBillingProjectionTables(t.Context(), core, nil)
	require.ErrorIs(t, err, errCoreBillingRetirementLogUnavailable)
	require.False(t, dropped)
	assertRetiredCoreTablesExist(t, core, true)

	closedLog := openRetirementTestDB(t)
	require.NoError(t, models.MigrateLogDB(closedLog))
	sqlDB, err := closedLog.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	dropped, err = RetireCoreBillingProjectionTables(t.Context(), core, closedLog)
	require.Error(t, err)
	require.False(t, dropped)
	assertRetiredCoreTablesExist(t, core, true)
}

func TestRetireCoreBillingProjectionTablesRollsBackWhenDropFails(t *testing.T) {
	core, logDB := openRetirementTestDB(t), openRetirementTestDB(t)
	seedLegacyCoreBillingTables(t, core)
	require.NoError(t, core.AutoMigrate(&models.BillingLog{}))
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "keep-billing-fact", CreatedAt: 1}).Error)
	require.NoError(t, models.MigrateLogDB(logDB))
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: models.DailyBillingBackfillVersion, State: models.DailyBillingBackfillCompleted,
		StartedAtUnix: 1, CompletedAtUnix: 2, UpdatedAtUnix: 2,
	}).Error)

	dropFailure := errors.New("injected second drop failure")
	dropCalls := 0
	dropped, err := retireCoreBillingProjectionTablesWithDropTable(context.Background(), core, logDB, func(tx *gorm.DB, model any) error {
		dropCalls++
		if dropCalls == 2 {
			return dropFailure
		}
		return tx.Migrator().DropTable(model)
	})
	require.ErrorIs(t, err, dropFailure)
	require.False(t, dropped)
	require.Equal(t, 2, dropCalls)
	assertRetiredCoreTablesExist(t, core, true)
	require.True(t, core.Migrator().HasTable(&models.BillingLog{}))
	var billingFacts int64
	require.NoError(t, core.Model(&models.BillingLog{}).Count(&billingFacts).Error)
	require.Equal(t, int64(1), billingFacts)
}
