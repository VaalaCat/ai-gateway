package model_routing_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestExplicitMemberKindsPersistAndRejectCycles(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "same,real")
	id := createRouting(t, srv, jwt, map[string]any{"name": "same", "scope": "global", "enabled": true, "members": []map[string]any{{"kind": "model", "ref": "same", "weight": 1}}})
	var saved models.ModelRouting
	if err := srv.App.GetCoreDB().First(&saved, id).Error; err != nil {
		t.Fatal(err)
	}
	var ms []models.RoutingMember
	if err := json.Unmarshal([]byte(saved.Members), &ms); err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Kind != "model" {
		t.Fatalf("stored members: %s", saved.Members)
	}
	for _, tc := range []struct{ kind, code string }{{"routing", "cycle_detected"}, {"", "invalid_member_kind"}, {"auto", "invalid_member_kind"}} {
		code, body := updateRouting(t, srv, jwt, id, map[string]any{"members": []map[string]any{{"kind": tc.kind, "ref": "same", "weight": 1}}})
		if code != http.StatusBadRequest || body["code"] != tc.code {
			t.Fatalf("kind %q: %d %v", tc.kind, code, body)
		}
	}
}

func TestTypedPreviewUsesProposedGraphAndScope(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "A,real")
	createRouting(t, srv, jwt, map[string]any{"name": "A", "scope": "global", "enabled": true, "members": []map[string]any{{"kind": "model", "ref": "real", "weight": 1}}})
	createRouting(t, srv, jwt, map[string]any{"name": "B", "scope": "global", "enabled": true, "members": []map[string]any{{"kind": "routing", "ref": "A", "weight": 1}}})
	preview := previewRouting(t, srv, jwt, map[string]any{"self_name": "A", "self_scope": "global", "members": []map[string]any{{"kind": "routing", "ref": "B", "weight": 1}}})
	b := preview["root"].(map[string]any)["children"].([]any)[0].(map[string]any)
	a := b["children"].([]any)[0].(map[string]any)
	if a["error"] != "cycle" {
		t.Fatalf("candidate A -> B -> A must be a cycle: %v", preview)
	}
	preview = previewRouting(t, srv, jwt, map[string]any{"self_name": "A", "self_scope": "user", "self_user_id": 1, "members": []map[string]any{{"kind": "routing", "ref": "A", "weight": 1}}})
	a = preview["root"].(map[string]any)["children"].([]any)[0].(map[string]any)
	if a["kind"] != "routing" || a["error"] != nil {
		t.Fatalf("user A and global A are distinct: %v", preview)
	}
}
