package channel

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
)

func TestUpdateChannel_ResilienceBreakerEnabledFalseAccepted(t *testing.T) {
	db := setupTestDB(t)
	c := newTestContext(t, db, "")
	c.App.SetEventBus(eventbus.NewMemoryBus())
	h := &Handler{}
	ch := models.Channel{ChannelCore: models.ChannelCore{Name: "ch", Type: 1, Status: 1, Weight: 1}}
	require.NoError(t, db.Create(&ch).Error)

	req := UpdateRequest{ID: strconv.Itoa(int(ch.ID))}
	req.SetBodyMap(map[string]any{
		"resilience": map[string]any{
			"breaker_enabled": false,
		},
	})
	got, err := h.Update(c, req)
	require.NoError(t, err)
	res := got.Resilience.Data()
	require.NotNil(t, res.BreakerEnabled)
	require.False(t, *res.BreakerEnabled)
}
