package channel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// fetchModelsHub records which agent received the fetch-models RPC.
type fetchModelsHub struct {
	online map[string]bool
	calls  []string
	params map[string]any
}

func (h *fetchModelsHub) Call(agentID string, _ string, params any, _ time.Duration) (json.RawMessage, error) {
	h.calls = append(h.calls, agentID)
	h.params, _ = params.(map[string]any)
	return json.RawMessage(`{"models":["model-via-agent"]}`), nil
}

func (h *fetchModelsHub) IsOnline(agentID string) bool { return h.online[agentID] }

func newFetchModelsContext(t *testing.T, db *gorm.DB) *app.Context {
	t.Helper()
	// FetchModels resolves the proxy against the master runtime config.
	c := newTestContext(t, db, "")
	c.Settings = &config.MasterRuntimeConfig{}
	return c
}

func TestFetchModels_TagRoutesToOnlineMember(t *testing.T) {
	db := setupTestDB(t)
	for _, agent := range []models.Agent{
		{AgentID: "edge-1", Name: "edge-1", Status: consts.StatusEnabled, Tags: "edge"},
		{AgentID: "edge-2", Name: "edge-2", Status: consts.StatusEnabled, Tags: "us-west, edge"},
		{AgentID: "edge-3", Name: "edge-3", Status: consts.StatusEnabled, Tags: "edge"},
		// Carries "edge" only inside another tag.
		{AgentID: "core-1", Name: "core-1", Status: consts.StatusEnabled, Tags: "edge-1"},
	} {
		db.Create(&agent)
	}
	// edge-1 is offline. edge-2 sorts before edge-3, and core-1 is online but
	// must not be picked.
	hub := &fetchModelsHub{online: map[string]bool{"edge-2": true, "edge-3": true, "core-1": true}}

	h := &Handler{Hub: hub}
	resp, err := h.FetchModels(newFetchModelsContext(t, db), FetchModelsRequest{
		BaseURL:  "http://upstream.invalid",
		Key:      "sk-upstream",
		AgentTag: "edge",
	})
	if err != nil {
		t.Fatalf("FetchModels returned error: %v", err)
	}
	if len(hub.calls) != 1 || hub.calls[0] != "edge-2" {
		t.Fatalf("expected the fetch to go to edge-2, got calls %v", hub.calls)
	}
	if got := hub.params["key"]; got != "sk-upstream" {
		t.Errorf("key = %#v, want sk-upstream", got)
	}
	if len(resp.Models) != 1 || resp.Models[0] != "model-via-agent" {
		t.Errorf("expected the agent's models, got %+v", resp)
	}
}

func TestFetchModels_TagRunsLocallyWhenEmbeddedAgentIsMember(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-local"}]}`))
	}))
	defer upstream.Close()

	db := setupTestDB(t)
	db.Create(&models.Agent{AgentID: "embedded", Name: "Embedded Agent", Status: consts.StatusEnabled, Tags: "edge"})
	db.Create(&models.Agent{AgentID: "edge-1", Name: "edge-1", Status: consts.StatusEnabled, Tags: "edge"})
	hub := &fetchModelsHub{online: map[string]bool{"edge-1": true}}

	h := &Handler{Hub: hub}
	resp, err := h.FetchModels(newFetchModelsContext(t, db), FetchModelsRequest{
		BaseURL:  upstream.URL,
		Key:      "sk-upstream",
		AgentTag: "edge",
	})
	if err != nil {
		t.Fatalf("FetchModels returned error: %v", err)
	}
	if len(hub.calls) != 0 {
		t.Fatalf("expected no agent RPC, got calls %v", hub.calls)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error %q", resp.Error)
	}
	if len(resp.Models) != 1 || resp.Models[0] != "model-local" {
		t.Errorf("expected the upstream's models, got %+v", resp)
	}
}

func TestFetchModels_UnservableTagIsNotFetchedLocally(t *testing.T) {
	cases := []struct {
		name      string
		agents    []models.Agent
		tag       string
		wantError string
	}{
		{
			name:      "tag matches no agent",
			tag:       "nope",
			wantError: `agent tag "nope" matches no enabled agent`,
		},
		{
			name:      "tag is carried only by a disabled agent",
			agents:    []models.Agent{{AgentID: "edge-1", Name: "edge-1", Tags: "edge"}},
			tag:       "edge",
			wantError: `agent tag "edge" matches no enabled agent`,
		},
		{
			name:      "no member is online",
			agents:    []models.Agent{{AgentID: "edge-1", Name: "edge-1", Status: consts.StatusEnabled, Tags: "edge"}},
			tag:       "edge",
			wantError: `agent tag "edge" has no online agent`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
			}))
			defer upstream.Close()

			db := setupTestDB(t)
			for _, agent := range tc.agents {
				db.Create(&agent)
			}
			if tc.name == "tag is carried only by a disabled agent" {
				db.Model(&models.Agent{}).Where("agent_id = ?", "edge-1").Update("status", consts.StatusDisabled)
			}
			hub := &fetchModelsHub{}

			h := &Handler{Hub: hub}
			resp, err := h.FetchModels(newFetchModelsContext(t, db), FetchModelsRequest{
				BaseURL:  upstream.URL,
				Key:      "sk-upstream",
				AgentTag: tc.tag,
			})
			if err != nil {
				t.Fatalf("FetchModels returned error: %v", err)
			}
			if resp.Error != tc.wantError {
				t.Fatalf("Error = %q, want %q", resp.Error, tc.wantError)
			}
			if hits != 0 || len(hub.calls) != 0 {
				t.Fatalf("an unservable tag must not be fetched anyway: upstream hits %d, agent calls %v", hits, hub.calls)
			}
		})
	}
}

func TestFetchModels_AgentIDNamesTheAgentDirectly(t *testing.T) {
	db := setupTestDB(t)
	hub := &fetchModelsHub{online: map[string]bool{"remote-1": true}}

	h := &Handler{Hub: hub}
	resp, err := h.FetchModels(newFetchModelsContext(t, db), FetchModelsRequest{
		BaseURL: "http://upstream.invalid",
		Key:     "sk-upstream",
		AgentID: "remote-1",
	})
	if err != nil {
		t.Fatalf("FetchModels returned error: %v", err)
	}
	if len(hub.calls) != 1 || hub.calls[0] != "remote-1" {
		t.Fatalf("expected the fetch to go to remote-1, got calls %v", hub.calls)
	}
	if len(resp.Models) != 1 || resp.Models[0] != "model-via-agent" {
		t.Errorf("expected the agent's models, got %+v", resp)
	}
}

func TestFetchModels_RejectsAgentIDAndTagTogether(t *testing.T) {
	h := &Handler{Hub: &fetchModelsHub{}}
	_, err := h.FetchModels(newFetchModelsContext(t, setupTestDB(t)), FetchModelsRequest{
		BaseURL:  "http://upstream.invalid",
		Key:      "sk-upstream",
		AgentID:  "remote-1",
		AgentTag: "edge",
	})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 for agent_id and agent_tag together, got %v", err)
	}
}
