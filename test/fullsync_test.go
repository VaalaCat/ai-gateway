package test

import (
	"encoding/json"
	"io"
	"testing"
)

type fullSyncResult struct {
	AgentID    string `json:"agent_id"`
	Success    bool   `json:"success"`
	Version    int64  `json:"version"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error"`
}

type fullSyncResponse struct {
	Results []fullSyncResult `json:"results"`
}

func TestFullSync_SingleAgent(t *testing.T) {
	env := setupFullEnv(t, "fullsync-single", 1)
	defer env.Close()

	env.CreateChannel("fs-ch", 1, "sk-test", "http://localhost:9999", "gpt-4o")
	env.SyncFromMaster()

	resp := env.DoAdmin("POST", "/api/admin/agents/full-sync", map[string]any{
		"agent_ids": []string{"fullsync-single"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result fullSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	r := result.Results[0]
	if r.AgentID != "fullsync-single" {
		t.Errorf("expected agent_id=fullsync-single, got %q", r.AgentID)
	}
	if !r.Success {
		t.Errorf("expected success=true, got error: %s", r.Error)
	}
	if r.Version <= 0 {
		t.Errorf("expected positive version, got %d", r.Version)
	}
	if r.DurationMs < 0 {
		t.Errorf("expected non-negative duration_ms, got %d", r.DurationMs)
	}
}

func TestFullSync_AllAgents(t *testing.T) {
	env := setupFullEnv(t, "fullsync-all", 1)
	defer env.Close()

	env.SyncFromMaster()

	resp := env.DoAdmin("POST", "/api/admin/agents/full-sync", map[string]any{
		"all": true,
	})
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result fullSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(result.Results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(result.Results))
	}

	for _, r := range result.Results {
		if !r.Success {
			t.Errorf("agent %s: expected success=true, got error: %s", r.AgentID, r.Error)
		}
	}
}

func TestFullSync_OfflineAgent(t *testing.T) {
	env := setupFullEnv(t, "fullsync-offline", 1)
	defer env.Close()

	resp := env.DoAdmin("POST", "/api/admin/agents/full-sync", map[string]any{
		"agent_ids": []string{"non-existent-agent-id"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result fullSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	r := result.Results[0]
	if r.AgentID != "non-existent-agent-id" {
		t.Errorf("expected agent_id=non-existent-agent-id, got %q", r.AgentID)
	}
	if r.Success {
		t.Errorf("expected success=false for offline agent")
	}
	if r.Error == "" {
		t.Errorf("expected non-empty error message for offline agent")
	}
}

func TestFullSync_EmptyRequest(t *testing.T) {
	env := setupFullEnv(t, "fullsync-empty", 1)
	defer env.Close()

	resp := env.DoAdmin("POST", "/api/admin/agents/full-sync", map[string]any{})
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected non-200 for empty request, got 200: %s", body)
	}
}
