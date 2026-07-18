package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFindAgentByIDClassifiesDatabaseOutcomes(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		db := setupTestDB(t)
		stored := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
		require.NoError(t, db.Create(&stored).Error)

		found, err := findAgentByID(newTestContext(t, db), strconv.Itoa(int(stored.ID)))
		require.NoError(t, err)
		require.Equal(t, stored.AgentID, found.AgentID)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := findAgentByID(newTestContext(t, setupTestDB(t)), "9999")
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusNotFound, apiErr.Status)
		require.Equal(t, "agent not found", apiErr.Message)
	})

	t.Run("query failure is stable and sanitized", func(t *testing.T) {
		db := setupTestDB(t)
		callbackName := "test:agent_lookup_failure:" + t.Name()
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			tx.AddError(errors.New("SQL connection failed password=database-secret"))
		}))
		t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

		_, err := findAgentByID(newTestContext(t, db), "1")
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusInternalServerError, apiErr.Status)
		require.Equal(t, "get agent failed", apiErr.Message)
		status, body := (api.DefaultErrorMapper{}).Map(err)
		require.Equal(t, http.StatusInternalServerError, status)
		raw, marshalErr := json.Marshal(body)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(raw), "database-secret")
		require.NotContains(t, string(raw), "password")
	})

	t.Run("canceled request takes precedence", func(t *testing.T) {
		ctx := newTestContext(t, setupTestDB(t))
		requestCtx, cancel := context.WithCancel(ctx.Request.Context())
		cancel()
		ctx.Request = ctx.Request.WithContext(requestCtx)

		_, err := findAgentByID(ctx, "1")
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusRequestTimeout, apiErr.Status)
		require.Equal(t, "request_canceled", apiErr.Code)
	})
}
