package system

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestGetSettingsResponseExcludesMasterSigningState(t *testing.T) {
	c := newSettingsContext(t)
	privateMarker := []byte("settings-private-signing-marker")
	one := uint8(1)
	db := c.App.GetDB()
	quietDB := db.Session(&gorm.Session{Logger: db.Logger.LogMode(gormlogger.Silent)})
	require.NoError(t, quietDB.Create(&models.MasterSigningKey{
		KeyID:      strings.Repeat("a", 64),
		PublicKey:  []byte("public-key"),
		PrivateKey: privateMarker,
		ActiveSlot: &one,
	}).Error)
	require.NoError(t, quietDB.Create(&models.Setting{
		Key:   "master_signing_key",
		Value: string(privateMarker),
	}).Error)

	response, err := (&Handler{}).GetSettings(c, GetSettingsRequest{})
	require.NoError(t, err)
	_, exposedAsSetting := response.Settings["master_signing_key"]
	require.False(t, exposedAsSetting)
	raw, err := json.Marshal(response)
	require.NoError(t, err)
	requireNoSystemSigningState(t, raw, privateMarker)
}

func requireNoSystemSigningState(t *testing.T, raw, privateMarker []byte) {
	t.Helper()
	if bytes.Contains(raw, privateMarker) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(privateMarker))) {
		t.Fatal("system response exposed private signing material")
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"privatekey", "private_key", "active_slot", "master_signing"} {
		if strings.Contains(lower, forbidden) {
			t.Fatal("system response exposed a master signing field")
		}
	}
}
