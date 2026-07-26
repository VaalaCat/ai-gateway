package models

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type agentTransportPolicyRow struct {
	DirectInboundEnabled  bool
	DirectOutboundEnabled bool
	RelayInboundEnabled   bool
	RelayOutboundEnabled  bool
}

func TestAgentTransportPolicyModelSchema(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[Agent]()
	fields := map[string]string{
		"DirectInboundEnabled":  `gorm:"not null;default:true" json:"direct_inbound_enabled"`,
		"DirectOutboundEnabled": `gorm:"not null;default:true" json:"direct_outbound_enabled"`,
		"RelayInboundEnabled":   `gorm:"not null;default:true" json:"relay_inbound_enabled"`,
		"RelayOutboundEnabled":  `gorm:"not null;default:true" json:"relay_outbound_enabled"`,
	}
	for name, wantTag := range fields {
		field, ok := typ.FieldByName(name)
		require.True(t, ok, "missing Agent field %s", name)
		require.Equal(t, reflect.Bool, field.Type.Kind())
		require.Equal(t, wantTag, string(field.Tag))
	}
	_, hasLegacyField := typ.FieldByName(strings.Join([]string{"Peer", "Route", "Mode"}, ""))
	require.False(t, hasLegacyField)
}

func TestAutoMigrateAgentTransportPolicy_DropsLegacyRoutingColumnWithoutConvertingValues(t *testing.T) {
	for _, legacyValue := range []string{"direct_first", "relay_only"} {
		t.Run(legacyValue, func(t *testing.T) {
			db := newAgentTransportPolicyTestDB(t)
			createLegacyAgentTransportSchema(t, db, legacyValue)

			require.NoError(t, AutoMigrate(db))

			policy := loadAgentTransportPolicy(t, db, "legacy-agent")
			require.Equal(t, allAgentTransportDirectionsEnabled(), policy)
			require.False(t, db.Migrator().HasColumn(&Agent{}, legacyAgentRoutingColumn()))

			var fallbackCount int64
			require.NoError(t, db.Model(&Setting{}).Where("key = ?", legacyRelayFallbackSettingKey()).Count(&fallbackCount).Error)
			require.Zero(t, fallbackCount, "legacy setting must be removed without mapping to transport directions")
		})
	}
}

func TestAutoMigrateAgentTransportPolicy_RepeatedMigrationPreservesEnabledDefaults(t *testing.T) {
	db := newAgentTransportPolicyTestDB(t)
	createLegacyAgentTransportSchema(t, db, "direct_first")

	require.NoError(t, AutoMigrate(db))
	require.NoError(t, AutoMigrate(db))

	policy := loadAgentTransportPolicy(t, db, "legacy-agent")
	require.Equal(t, allAgentTransportDirectionsEnabled(), policy)
	require.False(t, db.Migrator().HasColumn(&Agent{}, legacyAgentRoutingColumn()))
}

func TestAutoMigrateAgentTransportPolicy_FreshSchemaDefaultsToEnabled(t *testing.T) {
	db := newAgentTransportPolicyTestDB(t)
	require.NoError(t, AutoMigrate(db))

	require.NoError(t, db.Exec(
		`INSERT INTO agents (agent_id, name) VALUES (?, ?)`,
		"fresh-agent", "fresh",
	).Error)

	policy := loadAgentTransportPolicy(t, db, "fresh-agent")
	require.Equal(t, allAgentTransportDirectionsEnabled(), policy)
	require.False(t, db.Migrator().HasColumn(&Agent{}, legacyAgentRoutingColumn()))
}

func TestAutoMigrateAgentTransportPolicy_RepeatedMigrationPreservesExplicitFalse(t *testing.T) {
	db := newAgentTransportPolicyTestDB(t)
	require.NoError(t, AutoMigrate(db))
	require.NoError(t, db.Create(&Agent{AgentID: "disabled-agent", Name: "disabled"}).Error)

	require.NoError(t, db.Model(&Agent{}).
		Where("agent_id = ?", "disabled-agent").
		Updates(map[string]any{
			"direct_inbound_enabled":  false,
			"direct_outbound_enabled": false,
			"relay_inbound_enabled":   false,
			"relay_outbound_enabled":  false,
		}).Error)

	policy := loadAgentTransportPolicy(t, db, "disabled-agent")
	require.Equal(t, agentTransportPolicyRow{}, policy)

	require.NoError(t, AutoMigrate(db))
	policy = loadAgentTransportPolicy(t, db, "disabled-agent")
	require.Equal(t, agentTransportPolicyRow{}, policy)
}

func newAgentTransportPolicyTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func createLegacyAgentTransportSchema(t *testing.T, db *gorm.DB, legacyValue string) {
	t.Helper()

	column := legacyAgentRoutingColumn()
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TABLE agents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_id TEXT UNIQUE,
		secret TEXT,
		name TEXT,
		status INTEGER DEFAULT 1,
		last_seen INTEGER,
		created_at INTEGER,
		http_addresses TEXT,
		tags TEXT,
		proxy_url TEXT,
		relay_mode TEXT NOT NULL DEFAULT 'inherit',
		relay_uri TEXT,
		%s TEXT NOT NULL DEFAULT 'direct_first'
	)`, column)).Error)
	require.NoError(t, db.Exec(`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT
	)`).Error)
	require.NoError(t, db.Exec(
		fmt.Sprintf(`INSERT INTO agents (agent_id, name, %s) VALUES (?, ?, ?)`, column),
		"legacy-agent", "legacy", legacyValue,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)`,
		legacyRelayFallbackSettingKey(), "0",
	).Error)
}

func legacyAgentRoutingColumn() string {
	return strings.Join([]string{"peer", "route", "mode"}, "_")
}

func loadAgentTransportPolicy(t *testing.T, db *gorm.DB, agentID string) agentTransportPolicyRow {
	t.Helper()

	var policy agentTransportPolicyRow
	require.NoError(t, db.Model(&Agent{}).
		Select(
			"direct_inbound_enabled",
			"direct_outbound_enabled",
			"relay_inbound_enabled",
			"relay_outbound_enabled",
		).
		Where("agent_id = ?", agentID).
		Take(&policy).Error)
	return policy
}

func allAgentTransportDirectionsEnabled() agentTransportPolicyRow {
	return agentTransportPolicyRow{
		DirectInboundEnabled:  true,
		DirectOutboundEnabled: true,
		RelayInboundEnabled:   true,
		RelayOutboundEnabled:  true,
	}
}
