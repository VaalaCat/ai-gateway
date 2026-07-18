package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestAgentResponsesExcludeMasterSigningState(t *testing.T) {
	db := setupTestDB(t)
	privateMarker := []byte("agent-api-private-signing-marker")
	one := uint8(1)
	quietDB := db.Session(&gorm.Session{Logger: db.Logger.LogMode(gormlogger.Silent)})
	require.NoError(t, quietDB.Create(&models.MasterSigningKey{
		KeyID:      strings.Repeat("a", 64),
		PublicKey:  []byte("public-key"),
		PrivateKey: privateMarker,
		ActiveSlot: &one,
	}).Error)
	agent := models.Agent{
		AgentID: "agent-a",
		Secret:  "agent-secret",
		Name:    "Agent A",
		Status:  consts.StatusEnabled,
	}
	require.NoError(t, quietDB.Create(&agent).Error)

	handler := &Handler{Connections: connectivity.NewService(
		"master-instance",
		connectivity.Sources{},
		connectivity.Options{Now: func() time.Time { return time.Unix(1_000, 0) }},
	)}
	c := newTestContext(t, db)
	c.UserInfo = &app.UserInfo{Role: 2}
	listResponse, err := handler.List(c, ListRequest{})
	require.NoError(t, err)
	require.Len(t, listResponse.Data, 1)
	detailResponse, err := handler.Detail(c, DetailRequest{ID: strconv.Itoa(int(agent.ID))})
	require.NoError(t, err)

	for _, response := range []any{listResponse.Data[0], detailResponse} {
		raw, err := json.Marshal(response)
		require.NoError(t, err)
		requireNoAgentSigningState(t, raw, privateMarker)
	}
}

func requireNoAgentSigningState(t *testing.T, raw, privateMarker []byte) {
	t.Helper()
	if bytes.Contains(raw, privateMarker) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(privateMarker))) {
		t.Fatal("agent API response exposed private signing material")
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"privatekey", "private_key", "active_slot", "master_signing"} {
		if strings.Contains(lower, forbidden) {
			t.Fatal("agent API response exposed a master signing field")
		}
	}
}
