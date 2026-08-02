package billing

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestLegacySettlerDoesNotCreateOrWriteRetiredDailyBilling(t *testing.T) {
	db, settler := newLegacyDailySettler(t)
	require.NoError(t, db.Migrator().DropTable(&models.TokenDailyBilling{}, &models.ChannelDailyBilling{}))
	entry := legacyDailyEntry("legacy-without-daily-projections", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).Unix())

	require.NoError(t, settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{entry}))

	require.False(t, db.Migrator().HasTable(&models.TokenDailyBilling{}))
	require.False(t, db.Migrator().HasTable(&models.ChannelDailyBilling{}))
	require.Equal(t, int64(1), countRows(t, db, &models.UsageLog{}))
	var user models.User
	require.NoError(t, db.First(&user, 1).Error)
	require.Less(t, user.Quota, int64(10_000))
}

func newLegacyDailySettler(t *testing.T) (*gorm.DB, *Settler) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.AutoMigrate(db))
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "legacy-daily", Password: "x", Quota: 10_000}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "priced", InputPrice: 1, OutputPrice: 1, Status: 1}).Error)
	return db, NewSettler(&testAppProvider{db: db}, eventbus.NewMemoryBus(), zap.NewNop())
}

func legacyDailyEntry(requestID string, timestamp int64) protocol.UsageLogEntry {
	return protocol.UsageLogEntry{
		RequestID: requestID, UserID: 1, TokenID: 2, TokenName: "legacy-token",
		ChannelID: 3, ModelName: "priced", PromptTokens: 11, CompletionTokens: 7,
		Status: 1, Timestamp: timestamp,
	}
}

func TestNewSettlerKeepsLegacyUsageTransactionBeforeSplitActivation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.AutoMigrate(db))
	require.False(t, db.Migrator().HasTable(&models.BillingLog{}))
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "legacy", Password: "x", Quota: 1000}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "m", InputPrice: 1, Status: 1}).Error)

	settler := NewSettler(&testAppProvider{db: db}, eventbus.NewMemoryBus(), zap.NewNop())
	err = settler.SettleBatch(t.Context(), "agent", []protocol.UsageLogEntry{{
		RequestID: "legacy-before-3b", UserID: 1, ModelName: "m", PromptTokens: 1_000, Status: 1,
	}})
	require.NoError(t, err)
	require.Equal(t, int64(1), countRows(t, db, &models.UsageLog{}))
	var user models.User
	require.NoError(t, db.First(&user, 1).Error)
	require.Equal(t, int64(900), user.Quota)
}
