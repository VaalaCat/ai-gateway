package dao

import (
	"context"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestModelMarketplaceCatalogQueryBatchesEnabledRowsAndVisiblePrivateChannels(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	require.NoError(t, db.Create([]models.ModelConfig{
		{ModelName: "zeta", Status: 1},
		{ModelName: "alpha", Status: 1},
		{ModelName: "disabled-model", Status: 0},
	}).Error)
	require.NoError(t, db.Model(&models.ModelConfig{}).
		Where("model_name = ?", "disabled-model").UpdateColumn("status", 0).Error)
	require.NoError(t, db.Create([]models.Channel{
		{ChannelCore: models.ChannelCore{Name: "platform-a", Status: 1}, Models: "alpha,zeta"},
		{ChannelCore: models.ChannelCore{Name: "platform-off", Status: 0}, Models: "alpha"},
	}).Error)
	require.NoError(t, db.Model(&models.Channel{}).
		Where("name = ?", "platform-off").UpdateColumn("status", 0).Error)

	privateRows := []models.PrivateChannel{
		marketplacePrivateRow("owned", 7, 1, "alpha"),
		marketplacePrivateRow("shared-user", 8, 1, "alpha"),
		marketplacePrivateRow("shared-group", 9, 1, "zeta"),
		marketplacePrivateRow("invisible", 10, 1, "alpha"),
		marketplacePrivateRow("disabled-private", 7, 0, "alpha"),
	}
	require.NoError(t, db.Create(&privateRows).Error)
	require.NoError(t, db.Model(&models.PrivateChannel{}).
		Where("name = ?", "disabled-private").UpdateColumn("status", 0).Error)
	require.NoError(t, db.Create([]models.PrivateChannelShare{
		{ChannelID: privateRows[1].ID, TargetType: models.PrivateShareTargetUser, TargetID: 7},
		{ChannelID: privateRows[2].ID, TargetType: models.PrivateShareTargetGroup, TargetID: 42},
	}).Error)

	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))
	configs, err := query.ListEnabledMarketplaceModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "zeta"}, marketplaceConfigNames(configs))

	channels, err := query.ListMarketplaceChannels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"platform-a", "platform-off"}, marketplaceChannelNames(channels))

	private, err := query.ListMarketplacePrivateChannels(context.Background(), MarketplacePrivateChannelScope{
		UserID: 7, GroupIDs: []uint{42},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"owned", "shared-user", "shared-group", "disabled-private"}, marketplacePrivateNames(private),
		"authorization scopes metadata; the Agent planner, not SQL status, decides runtime availability")
}

func TestModelMarketplaceCatalogQueryAdminGlobalIncludesEveryPrivateChannel(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	rows := []models.PrivateChannel{
		marketplacePrivateRow("first", 7, 1, "alpha"),
		marketplacePrivateRow("second", 8, 1, "alpha"),
		marketplacePrivateRow("off", 9, 0, "alpha"),
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Model(&models.PrivateChannel{}).
		Where("name = ?", "off").UpdateColumn("status", 0).Error)

	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))
	got, err := query.ListMarketplacePrivateChannels(context.Background(), MarketplacePrivateChannelScope{AdminGlobal: true})
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second", "off"}, marketplacePrivateNames(got))
}

func TestModelMarketplaceCatalogQueryUsesThreeQueriesRegardlessOfModelCount(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	configs := make([]models.ModelConfig, 0, 40)
	for i := 1; i <= 40; i++ {
		configs = append(configs, models.ModelConfig{ModelName: fmt.Sprintf("model-%02d", i), Status: 1})
	}
	require.NoError(t, db.Create(&configs).Error)
	require.NoError(t, db.Create(&models.Channel{
		ChannelCore: models.ChannelCore{Name: "platform", Status: 1},
		Models:      "model-01,model-02,model-03",
	}).Error)
	require.NoError(t, db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Status: 1}, OwnerID: 7, Name: "private", Status: 1,
		Models: datatypes.JSONSlice[string]{"model-01", "model-02"},
	}).Error)

	queryCount := 0
	callbackName := "test:model_marketplace_query_count"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))
	_, err := query.ListEnabledMarketplaceModels(context.Background())
	require.NoError(t, err)
	_, err = query.ListMarketplaceChannels(context.Background())
	require.NoError(t, err)
	_, err = query.ListMarketplacePrivateChannels(context.Background(), MarketplacePrivateChannelScope{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, 3, queryCount, "catalog reads must stay fixed-size and never query once per model")
	t.Logf("marketplace catalog DAO queries = %d for %d models", queryCount, len(configs))
}

func TestModelMarketplaceCatalogQueryRejectsMissingViewerScopeWithoutReadingPrivateRows(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	require.NoError(t, db.Create(&marketplacePrivateRowValueForCreate).Error)

	queryCount := 0
	callbackName := "test:model_marketplace_zero_scope"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))
	got, err := query.ListMarketplacePrivateChannels(context.Background(), MarketplacePrivateChannelScope{})
	require.NoError(t, err)
	require.Empty(t, got)
	require.Zero(t, queryCount)
}

func TestModelMarketplaceRoutingQueryBatchesEffectiveTokenUserAndGlobalScopes(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	rows := []models.ModelRouting{
		{Name: "global", Scope: models.RoutingScopeGlobal, Enabled: true, Members: `[]`},
		{Name: "global-off", Scope: models.RoutingScopeGlobal, Enabled: false, Members: `[]`},
		{Name: "user-mine", Scope: models.RoutingScopeUser, UserID: 7, Enabled: true, Members: `[]`},
		{Name: "user-other", Scope: models.RoutingScopeUser, UserID: 8, Enabled: true, Members: `[]`},
		{Name: "token-mine", Scope: models.RoutingScopeToken, TokenID: 70, Enabled: true, Members: `[]`},
		{Name: "token-other", Scope: models.RoutingScopeToken, TokenID: 80, Enabled: true, Members: `[]`},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Model(&models.ModelRouting{}).
		Where("name = ?", "global-off").UpdateColumn("enabled", false).Error)

	queryCount := 0
	callbackName := "test:model_marketplace_routing_query_count"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))
	got, err := query.ListMarketplaceRoutings(context.Background(), MarketplaceRoutingScope{
		UserID: 7, TokenID: 70, GroupIDs: []uint{4, 3},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"global", "global-off", "token-mine", "user-mine"}, marketplaceRoutingNames(got))
	require.Equal(t, 1, queryCount, "routing scope must be loaded in one query regardless of route count")
}

func TestModelMarketplaceRoutingQueryAdminGlobalIncludesAllGlobalDefinitionsOnly(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	rows := []models.ModelRouting{
		{Name: "global", Scope: models.RoutingScopeGlobal, Enabled: true, Members: `[]`},
		{Name: "global-off", Scope: models.RoutingScopeGlobal, Enabled: false, Members: `[]`},
		{Name: "tenant-secret", Scope: models.RoutingScopeUser, UserID: 7, Enabled: true, Members: `[]`},
		{Name: "token-secret", Scope: models.RoutingScopeToken, TokenID: 70, Enabled: true, Members: `[]`},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Model(&models.ModelRouting{}).
		Where("name = ?", "global-off").UpdateColumn("enabled", false).Error)

	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))
	got, err := query.ListMarketplaceRoutings(context.Background(), MarketplaceRoutingScope{AdminGlobal: true})
	require.NoError(t, err)
	require.Equal(t, []string{"global", "global-off"}, marketplaceRoutingNames(got))
}

func TestModelMarketplaceRoutingQueryRejectsEmptyOrMixedAdminScopeWithoutSQL(t *testing.T) {
	db := newMarketplaceDAOTestDB(t)
	queryCount := 0
	callbackName := "test:model_marketplace_invalid_routing_scope"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	query := NewModelMarketplaceQuery(NewContext(marketplaceDAOTestApp(db)))

	for _, scope := range []MarketplaceRoutingScope{
		{},
		{AdminGlobal: true, UserID: 7},
		{AdminGlobal: true, TokenID: 70},
	} {
		got, err := query.ListMarketplaceRoutings(context.Background(), scope)
		require.NoError(t, err)
		require.Empty(t, got)
	}
	require.Zero(t, queryCount)
}

var marketplacePrivateRowValueForCreate = marketplacePrivateRow("hidden", 7, 1, "alpha")

func newMarketplaceDAOTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&models.ModelConfig{},
		&models.Channel{},
		&models.PrivateChannel{},
		&models.PrivateChannelShare{},
		&models.ModelRouting{},
	))
	return db
}

func marketplaceDAOTestApp(db *gorm.DB) app.Application {
	application := app.NewApplication()
	application.SetCoreDB(db)
	return application
}

func marketplacePrivateRow(name string, ownerID uint, status int, modelNames ...string) models.PrivateChannel {
	return models.PrivateChannel{
		ChannelCore: models.ChannelCore{Status: status},
		OwnerID:     ownerID,
		Name:        name,
		Status:      status,
		Models:      datatypes.JSONSlice[string](modelNames),
	}
}

func marketplaceConfigNames(rows []models.ModelConfig) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.ModelName)
	}
	return names
}

func marketplacePrivateNames(rows []models.PrivateChannel) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

func marketplaceChannelNames(rows []models.Channel) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

func marketplaceRoutingNames(rows []models.ModelRouting) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}
