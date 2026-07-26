package historybackfill

import (
	"context"
	"testing"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLegacyReaderProjectsMonolithUsageIntoBillingAndRequest(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.UsageLog{}, &models.UsageLogTrace{}))
	require.NoError(t, source.Create(&models.UsageLog{
		ID: 41, RequestID: "req-41", UserID: 3, AgentID: "agent-1", ModelName: "model-1",
		PromptTokens: 7, TotalCost: 9, Duration: 1200, FirstResponseMs: 200, CreatedAt: 100,
	}).Error)

	reader := NewLegacyReader(source, masterdatabase.LegacyLayoutMonolith)
	billing, err := reader.ReadBilling(t.Context(), 0, 100)
	require.NoError(t, err)
	require.Equal(t, uint(41), billing.LastSourceID)
	require.Len(t, billing.Rows, 1)
	require.Zero(t, billing.Rows[0].ID)
	require.Equal(t, "req-41", billing.Rows[0].RequestID)
	require.EqualValues(t, 9, billing.Rows[0].TotalCost)

	requests, err := reader.ReadRequests(t.Context(), 0, 100)
	require.NoError(t, err)
	require.Equal(t, uint(41), requests.LastSourceID)
	require.Len(t, requests.Rows, 1)
	require.Zero(t, requests.Rows[0].ID)
	require.Equal(t, "agent-1", requests.Rows[0].AgentID)
	require.Equal(t, 1200, requests.Rows[0].Duration)
}

func TestLegacyReaderReadsV5BillingWithoutLogReplay(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.BillingLog{}))
	require.NoError(t, source.Create(&models.BillingLog{ID: 51, RequestID: "req-51", TotalCost: 11}).Error)

	reader := NewLegacyReader(source, masterdatabase.LegacyLayoutV5Core)
	batch, err := reader.ReadBilling(t.Context(), 0, 100)
	require.NoError(t, err)
	require.Equal(t, uint(51), batch.LastSourceID)
	require.Len(t, batch.Rows, 1)
	require.Zero(t, batch.Rows[0].ID)
	require.False(t, reader.HasLogHistory())

	requests, err := reader.ReadRequests(t.Context(), 0, 100)
	require.NoError(t, err)
	require.Empty(t, requests.Rows)
	require.Equal(t, uint(0), requests.LastSourceID)
}

func TestLegacyReaderHonorsCursorBatchBoundaryAndEmptyCursor(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.UsageLog{}, &models.UsageLogTrace{}))
	require.NoError(t, source.Create(&[]models.UsageLog{
		{ID: 41, RequestID: "req-41"}, {ID: 43, RequestID: "req-43"}, {ID: 47, RequestID: "req-47"},
	}).Error)
	reader := NewLegacyReader(source, masterdatabase.LegacyLayoutMonolith)

	batch, err := reader.ReadRequests(t.Context(), 41, 1)
	require.NoError(t, err)
	require.Len(t, batch.Rows, 1)
	require.Equal(t, "req-43", batch.Rows[0].RequestID)
	require.Equal(t, uint(43), batch.LastSourceID)
	empty, err := reader.ReadRequests(t.Context(), 47, 1)
	require.NoError(t, err)
	require.Empty(t, empty.Rows)
	require.Equal(t, uint(47), empty.LastSourceID)
}

func TestLegacyReaderRejectsInvalidInputsAndClosedDatabase(t *testing.T) {
	source := openLegacyReaderDB(t)
	require.NoError(t, source.AutoMigrate(&models.UsageLog{}))
	reader := NewLegacyReader(source, masterdatabase.LegacyLayoutMonolith)

	_, err := reader.ReadBilling(nil, 0, 1)
	require.ErrorContains(t, err, "context")
	_, err = reader.ReadBilling(t.Context(), 0, 0)
	require.ErrorContains(t, err, "limit")
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = reader.ReadBilling(canceled, 0, 1)
	require.ErrorIs(t, err, context.Canceled)
	_, err = NewLegacyReader(source, masterdatabase.LegacyLayoutNone).ReadBilling(t.Context(), 0, 1)
	require.ErrorContains(t, err, "unsupported layout")
	sqlDB, err := source.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	_, err = reader.ReadBilling(t.Context(), 0, 1)
	require.Error(t, err)
}

func openLegacyReaderDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}
