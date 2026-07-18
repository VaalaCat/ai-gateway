package sync

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestMergeAgentConfigHeartbeatDoesNotOverwriteRelayConfiguration(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, models.AutoMigrate(db))
	agent := models.Agent{
		AgentID:   "heartbeat-relay-agent",
		Name:      "agent",
		Status:    1,
		RelayMode: consts.RelayModeCustom,
		RelayURI:  "wss://relay.example/heartbeat?token=secret",
	}
	require.NoError(t, db.Create(&agent).Error)
	hub := NewHub(&dbApp{db: db}, zap.NewNop(), nil, func() int64 { return 1 }, nil, HubOptions{})

	hub.mergeAgentConfig(context.Background(), agent.AgentID, protocol.HeartbeatParams{
		HTTPAddresses: json.RawMessage(`[{"url":"http://127.0.0.1:8139","tag":"auto-detected"}]`),
		Tags:          "heartbeat-tag",
		ProxyURL:      "http://heartbeat-proxy.example",
	})

	var stored models.Agent
	require.NoError(t, db.First(&stored, agent.ID).Error)
	require.Equal(t, `[{"url":"http://127.0.0.1:8139","tag":"auto-detected"}]`, stored.HTTPAddresses)
	require.Equal(t, "heartbeat-tag", stored.Tags)
	require.Equal(t, "http://heartbeat-proxy.example", stored.ProxyURL)
	require.Equal(t, consts.RelayModeCustom, stored.RelayMode)
	require.Equal(t, "wss://relay.example/heartbeat?token=secret", stored.RelayURI)
}
