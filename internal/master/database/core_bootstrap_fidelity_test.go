package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBootstrapCorePreservesUserGroupNullsAndZeroValues(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Exec(`
		INSERT INTO user_groups (id, name, status, byok_enabled, byok_max_channels, created_at, updated_at)
		VALUES
			(1, 'enabled-null', 0, TRUE, NULL, 0, 0),
			(2, 'null-three', 0, NULL, 3, 0, 0),
			(3, 'disabled-zero', 0, FALSE, 0, 0, 0)
	`).Error)
	target := openBootstrapTarget(t)

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	require.True(t, result.Created)
	require.EqualValues(t, 3, result.CopiedRows)

	type userGroupRow struct {
		ID              int
		Status          int
		BYOKEnabled     sql.NullBool  `gorm:"column:byok_enabled"`
		BYOKMaxChannels sql.NullInt64 `gorm:"column:byok_max_channels"`
		CreatedAt       int64         `gorm:"column:created_at"`
		UpdatedAt       int64         `gorm:"column:updated_at"`
	}
	var rows []userGroupRow
	require.NoError(t, target.Raw(`SELECT id, status, byok_enabled, byok_max_channels, created_at, updated_at FROM user_groups ORDER BY id`).Scan(&rows).Error)
	require.Len(t, rows, 3)
	require.Equal(t, userGroupRow{ID: 1, Status: 0, BYOKEnabled: sql.NullBool{Bool: true, Valid: true}}, rows[0])
	require.Equal(t, userGroupRow{ID: 2, Status: 0, BYOKMaxChannels: sql.NullInt64{Int64: 3, Valid: true}}, rows[1])
	require.Equal(t, userGroupRow{ID: 3, Status: 0, BYOKEnabled: sql.NullBool{Valid: true}, BYOKMaxChannels: sql.NullInt64{Valid: true}}, rows[2])
	var markers int64
	require.NoError(t, target.Model(&models.HistoryMigration{}).Count(&markers).Error)
	require.EqualValues(t, 1, markers)
}

func TestBootstrapCorePreservesFalseAndZeroDefaults(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Exec(`INSERT INTO agents (id, agent_id, status, relay_mode, direct_inbound_enabled, direct_outbound_enabled, relay_inbound_enabled, relay_outbound_enabled) VALUES (1, 'agent', 0, '', FALSE, FALSE, FALSE, FALSE)`).Error)
	require.NoError(t, source.Exec(`INSERT INTO request_limiters (id, name, enabled) VALUES (1, 'limiter', FALSE)`).Error)
	require.NoError(t, source.Exec(`INSERT INTO o_auth_providers (id, name, enabled, protocol) VALUES (1, 'provider', FALSE, '')`).Error)
	require.NoError(t, source.Exec(`INSERT INTO channels (id, name, status, weight, price_ratio) VALUES (1, 'channel', 0, 0, 0)`).Error)
	target := openBootstrapTarget(t)

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.NoError(t, err)
	require.True(t, result.Created)
	require.EqualValues(t, 4, result.CopiedRows)

	var agent struct {
		Status                int
		RelayMode             string
		DirectInboundEnabled  bool
		DirectOutboundEnabled bool
		RelayInboundEnabled   bool
		RelayOutboundEnabled  bool
	}
	agentResult := target.Raw(`SELECT status, relay_mode, direct_inbound_enabled, direct_outbound_enabled, relay_inbound_enabled, relay_outbound_enabled FROM agents WHERE id = 1`).Scan(&agent)
	require.NoError(t, agentResult.Error)
	require.EqualValues(t, 1, agentResult.RowsAffected)
	require.Equal(t, 0, agent.Status)
	require.Empty(t, agent.RelayMode)
	require.False(t, agent.DirectInboundEnabled)
	require.False(t, agent.DirectOutboundEnabled)
	require.False(t, agent.RelayInboundEnabled)
	require.False(t, agent.RelayOutboundEnabled)

	var limiter struct{ Enabled bool }
	limiterResult := target.Raw(`SELECT enabled FROM request_limiters WHERE id = 1`).Scan(&limiter)
	require.NoError(t, limiterResult.Error)
	require.EqualValues(t, 1, limiterResult.RowsAffected)
	require.False(t, limiter.Enabled)
	var provider struct {
		Enabled  bool
		Protocol string
	}
	providerResult := target.Raw(`SELECT enabled, protocol FROM o_auth_providers WHERE id = 1`).Scan(&provider)
	require.NoError(t, providerResult.Error)
	require.EqualValues(t, 1, providerResult.RowsAffected)
	require.False(t, provider.Enabled)
	require.Empty(t, provider.Protocol)
	var channel struct {
		Status     int
		Weight     int
		PriceRatio float64
	}
	channelResult := target.Raw(`SELECT status, weight, price_ratio FROM channels WHERE id = 1`).Scan(&channel)
	require.NoError(t, channelResult.Error)
	require.EqualValues(t, 1, channelResult.RowsAffected)
	require.Equal(t, 0, channel.Status)
	require.Zero(t, channel.Weight)
	require.Zero(t, channel.PriceRatio)
}

func TestBootstrapCopierSchemaEvolution(t *testing.T) {
	t.Run("ignores source columns and uses target defaults", func(t *testing.T) {
		source, target := openBootstrapRawPair(t)
		require.NoError(t, source.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL, source_only TEXT)`).Error)
		require.NoError(t, target.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL, defaulted TEXT NOT NULL DEFAULT 'target-default', nullable TEXT)`).Error)
		require.NoError(t, source.Exec(`INSERT INTO bootstrap_rows (id, value, source_only) VALUES (1, 'source-value', 'ignored')`).Error)

		copied, err := bootstrapCopier[struct{}]("bootstrap_rows", "id").copyAll(t.Context(), source, target)
		require.NoError(t, err)
		require.EqualValues(t, 1, copied)
		var row struct {
			Value     string
			Defaulted string
			Nullable  sql.NullString
		}
		result := target.Raw(`SELECT value, defaulted, nullable FROM bootstrap_rows WHERE id = 1`).Scan(&row)
		require.NoError(t, result.Error)
		require.EqualValues(t, 1, result.RowsAffected)
		require.Equal(t, "source-value", row.Value)
		require.Equal(t, "target-default", row.Defaulted)
		require.False(t, row.Nullable.Valid)
	})

	t.Run("rejects target-only primary key before insert", func(t *testing.T) {
		source, target := openBootstrapRawPair(t)
		require.NoError(t, source.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`).Error)
		require.NoError(t, target.Exec(`CREATE TABLE bootstrap_rows (id INTEGER NOT NULL, value TEXT NOT NULL, new_id TEXT PRIMARY KEY)`).Error)
		require.NoError(t, source.Exec(`INSERT INTO bootstrap_rows (id, value) VALUES (1, 'source-value')`).Error)

		copied, err := bootstrapCopier[struct{}]("bootstrap_rows", "id").copyAll(t.Context(), source, target)
		require.ErrorContains(t, err, "target column new_id is a primary key and is not shared")
		require.Zero(t, copied)
		var rows int64
		require.NoError(t, target.Table("bootstrap_rows").Count(&rows).Error)
		require.Zero(t, rows)
	})

	t.Run("rejects missing required target column before insert", func(t *testing.T) {
		source, target := openBootstrapRawPair(t)
		require.NoError(t, source.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`).Error)
		require.NoError(t, target.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL, required_value TEXT NOT NULL)`).Error)
		require.NoError(t, source.Exec(`INSERT INTO bootstrap_rows (id, value) VALUES (1, 'source-value')`).Error)

		copied, err := bootstrapCopier[struct{}]("bootstrap_rows", "id").copyAll(t.Context(), source, target)
		require.ErrorContains(t, err, "target column required_value is not nullable and has no default")
		require.Zero(t, copied)
		var rows int64
		require.NoError(t, target.Table("bootstrap_rows").Count(&rows).Error)
		require.Zero(t, rows)
	})

	t.Run("rejects pagination key that is not shared", func(t *testing.T) {
		source, target := openBootstrapRawPair(t)
		require.NoError(t, source.Exec(`CREATE TABLE bootstrap_rows (value TEXT NOT NULL)`).Error)
		require.NoError(t, target.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`).Error)

		copied, err := bootstrapCopier[struct{}]("bootstrap_rows", "id").copyAll(t.Context(), source, target)
		require.ErrorContains(t, err, "pagination key id is not shared")
		require.Zero(t, copied)
	})

	t.Run("counts conflicting source rows without overwriting target", func(t *testing.T) {
		source, target := openBootstrapRawPair(t)
		require.NoError(t, source.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`).Error)
		require.NoError(t, target.Exec(`CREATE TABLE bootstrap_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`).Error)
		require.NoError(t, source.Exec(`INSERT INTO bootstrap_rows (id, value) VALUES (1, 'source-value')`).Error)
		require.NoError(t, target.Exec(`INSERT INTO bootstrap_rows (id, value) VALUES (1, 'target-value')`).Error)

		copied, err := bootstrapCopier[struct{}]("bootstrap_rows", "id").copyAll(t.Context(), source, target)
		require.NoError(t, err)
		require.EqualValues(t, 1, copied)
		var value string
		require.NoError(t, target.Raw(`SELECT value FROM bootstrap_rows WHERE id = 1`).Scan(&value).Error)
		require.Equal(t, "target-value", value)
	})
}

func TestBootstrapCoreRejectsTargetRequiredColumnBeforeCopying(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source := openLegacyFixture(t, sourcePath)
	t.Cleanup(func() { closeGORM(t, source) })
	require.NoError(t, source.Create(&models.User{ID: 1, Username: "source-user"}).Error)
	target := openBootstrapTarget(t)
	require.NoError(t, target.Exec(`DROP TABLE users`).Error)
	require.NoError(t, target.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, required_value TEXT NOT NULL)`).Error)

	result, err := BootstrapCore(t.Context(), source, target, LegacyLayoutInfo{Kind: LegacyLayoutMonolith, Path: sourcePath}, BootstrapOptions{})
	require.ErrorContains(t, err, "target column required_value is not nullable and has no default")
	require.False(t, result.Created)
	require.Zero(t, result.CopiedRows)
	var users, markers int64
	require.NoError(t, target.Table("users").Count(&users).Error)
	require.NoError(t, target.Model(&models.HistoryMigration{}).Count(&markers).Error)
	require.Zero(t, users)
	require.Zero(t, markers)
}

func openBootstrapRawPair(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	source, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, source) })
	target, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { closeGORM(t, target) })
	return source, target
}
