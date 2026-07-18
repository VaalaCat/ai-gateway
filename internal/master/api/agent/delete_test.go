package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeleteRevokesControlSessionAfterPersistenceAndBeforeEvents(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "delete-target", Secret: "secret", Name: "target", Status: 1}
	require.NoError(t, db.Create(&agent).Error)
	c := newTestContext(t, db)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/agents/"+strconv.Itoa(int(agent.ID)), nil)
	bus := eventbus.NewMemoryBus()
	c.App.SetEventBus(bus)
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	order := make([]string, 0, 3)
	_, err := events.Subscribe(bus, events.AgentRevokedTopic, func(context.Context, models.Agent) error {
		order = append(order, events.AgentRevokedTopic.Value())
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.AgentDeleteTopic, func(context.Context, models.Agent) error {
		order = append(order, events.AgentDeleteTopic.Value())
		return nil
	})
	require.NoError(t, err)

	h := &Handler{RevokeControlSession: func(agentID string) bool {
		require.Equal(t, agent.AgentID, agentID)
		var count int64
		require.NoError(t, db.Model(&models.Agent{}).Where("id = ?", agent.ID).Count(&count).Error)
		require.Zero(t, count, "control session must be revoked only after the DB delete commits")
		order = append(order, "control.revoke")
		return true
	}}

	resp, err := h.Delete(c, api.IDPathRequest{ID: strconv.Itoa(int(agent.ID))})
	require.NoError(t, err)
	require.Equal(t, "deleted", resp.Status)
	require.Equal(t, []string{
		"control.revoke",
		events.AgentRevokedTopic.Value(),
		events.AgentDeleteTopic.Value(),
	}, order)
}

func TestDeleteDoesNotRevokeWhenPersistenceFails(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "delete-fails", Secret: "secret", Name: "target", Status: 1}
	require.NoError(t, db.Create(&agent).Error)
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(
		"test:fail_agent_delete_before_revoke",
		func(tx *gorm.DB) { _ = tx.AddError(errors.New("injected delete failure")) },
	))
	c := newTestContext(t, db)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/agents/"+strconv.Itoa(int(agent.ID)), nil)
	bus := newAgentRecordingBus()
	c.App.SetEventBus(bus)
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	revokeCalls := 0
	h := &Handler{RevokeControlSession: func(string) bool {
		revokeCalls++
		return true
	}}

	_, err := h.Delete(c, api.IDPathRequest{ID: strconv.Itoa(int(agent.ID))})
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusInternalServerError, apiErr.Status)
	require.Zero(t, revokeCalls)
	require.Empty(t, bus.snapshotEvents())
	var count int64
	require.NoError(t, db.Model(&models.Agent{}).Where("id = ?", agent.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestDeleteMissingAgentDoesNotRevoke(t *testing.T) {
	c := newTestContext(t, setupTestDB(t))
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/agents/999", nil)
	revokeCalls := 0
	h := &Handler{RevokeControlSession: func(string) bool {
		revokeCalls++
		return true
	}}

	_, err := h.Delete(c, api.IDPathRequest{ID: "999"})
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusNotFound, apiErr.Status)
	require.Zero(t, revokeCalls)
}
