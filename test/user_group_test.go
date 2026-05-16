package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

// adminPostJSON posts to an admin endpoint and returns the parsed response body as a map.
func adminPostJSON(t *testing.T, e *testEnv, path string, body map[string]any) map[string]any {
	t.Helper()
	resp := e.DoAdmin("POST", path, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s: %d %s", path, resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v body=%s", path, err, raw)
	}
	return out
}

// TestE2E_UserGroup_LiveSwitch verifies that changing a user's group_id is picked up
// by the agent on the next sync, restricting or expanding the visible model set.
func TestE2E_UserGroup_LiveSwitch(t *testing.T) {
	env := setupFullEnv(t, "agent-ug-live", 1)
	defer env.Close()

	// Register two model configs and two channels providing different models.
	// ListModels assertions do not require a real upstream — they exercise the
	// full token→user→group→channel filter chain inside the agent cache.
	env.CreateModelConfig("gpt-4o")
	env.CreateModelConfig("claude-3")

	chA := env.CreateChannel("ch-A", 1, "k1", "http://stub-A", "gpt-4o")
	chB := env.CreateChannel("ch-B", 14, "k2", "http://stub-B", "claude-3")
	_ = chB

	// Create a user-group that restricts access to channel A only.
	g := adminPostJSON(t, env, "/api/admin/user-groups", map[string]any{
		"name":                "team-a-only",
		"allowed_channel_ids": []uint{chA},
	})
	gid := uint(g["id"].(float64))

	// Create user alice belonging to that group.
	uResp := env.DoAdmin("POST", "/api/admin/users", map[string]any{
		"username": "alice", "password": "pass", "role": 1, "group_id": gid,
	})
	defer uResp.Body.Close()
	if uResp.StatusCode != http.StatusCreated && uResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(uResp.Body)
		t.Fatalf("create user: %d %s", uResp.StatusCode, raw)
	}
	uRaw, _ := io.ReadAll(uResp.Body)
	var u map[string]any
	json.Unmarshal(uRaw, &u)
	uid := uint(u["id"].(float64))

	// Create an API token for alice.
	tok := env.CreateToken(uid, "alice-tok")

	// Sync agent so it picks up the new group, user, token, channels, and models.
	env.SyncFromMaster()

	// /v1/models should only show gpt-4o (channel A), not claude-3 (channel B).
	mResp := env.ListModels(tok)
	if mResp.Code != http.StatusOK {
		t.Fatalf("ListModels status %d body %s", mResp.Code, mResp.Body.String())
	}
	body := mResp.Body.String()
	if !strings.Contains(body, `"id":"gpt-4o"`) {
		t.Fatalf("expected gpt-4o in models list (allowed via group): %s", body)
	}
	if strings.Contains(body, `"id":"claude-3"`) {
		t.Fatalf("did NOT expect claude-3 in models list (group blocks channel B): %s", body)
	}

	// Admin moves alice to the default group (id=1). Default group has an empty
	// allowed_channel_ids whitelist which means all channels are permitted.
	mvResp := env.DoAdmin("PUT", fmt.Sprintf("/api/admin/users/%d", uid), map[string]any{"group_id": 1})
	mvResp.Body.Close()
	if mvResp.StatusCode != http.StatusOK {
		t.Fatalf("update user group_id: %d", mvResp.StatusCode)
	}

	// Sync agent so it picks up the SyncedUser update（push apply-if-present 需要短暂传播窗口）。
	env.SyncFromMaster()
	time.Sleep(100 * time.Millisecond)

	mResp2 := env.ListModels(tok)
	body2 := mResp2.Body.String()
	if !strings.Contains(body2, `"id":"gpt-4o"`) {
		t.Fatalf("after switch to default: still expect gpt-4o: %s", body2)
	}
	if !strings.Contains(body2, `"id":"claude-3"`) {
		t.Fatalf("after switch to default: claude-3 should now appear: %s", body2)
	}
}

// TestE2E_UserGroup_DeleteFallsBackToDefault verifies that deleting a group causes
// affected users to fall back to the default group (id=1) and regain full channel access.
func TestE2E_UserGroup_DeleteFallsBackToDefault(t *testing.T) {
	env := setupFullEnv(t, "agent-ug-delete", 1)
	defer env.Close()

	env.CreateModelConfig("gpt-4o")
	chA := env.CreateChannel("ch-A", 1, "k1", "http://stub", "gpt-4o")
	_ = chA

	// Create a group whose whitelist references a non-existent channel — this
	// effectively blocks all real channels.
	g := adminPostJSON(t, env, "/api/admin/user-groups", map[string]any{
		"name":                "to-delete",
		"allowed_channel_ids": []uint{99999},
	})
	gid := uint(g["id"].(float64))

	// Create user bob belonging to the soon-to-be-deleted group.
	uResp := env.DoAdmin("POST", "/api/admin/users", map[string]any{
		"username": "bob", "password": "pass", "role": 1, "group_id": gid,
	})
	defer uResp.Body.Close()
	if uResp.StatusCode != http.StatusCreated && uResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(uResp.Body)
		t.Fatalf("create user: %d %s", uResp.StatusCode, raw)
	}
	uRaw, _ := io.ReadAll(uResp.Body)
	var u map[string]any
	json.Unmarshal(uRaw, &u)
	uid := uint(u["id"].(float64))

	tok := env.CreateToken(uid, "bob-tok")

	env.SyncFromMaster()

	// Group only allows channel 99999 (doesn't exist) → no real models should be visible.
	mResp := env.ListModels(tok)
	body := mResp.Body.String()
	if strings.Contains(body, `"id":"gpt-4o"`) {
		t.Fatalf("group should block all real channels, but gpt-4o appeared: %s", body)
	}

	// Delete the group — master should reassign bob to group 1 (default).
	delResp := env.DoAdmin("DELETE", fmt.Sprintf("/api/admin/user-groups/%d", gid), nil)
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete group: %d", delResp.StatusCode)
	}

	// Sync agent — bob should now be on default group and gpt-4o should become visible.
	env.SyncFromMaster()

	mResp2 := env.ListModels(tok)
	body2 := mResp2.Body.String()
	if !strings.Contains(body2, `"id":"gpt-4o"`) {
		t.Fatalf("after group delete, expected default access (gpt-4o visible): %s", body2)
	}

	// Confirm in DB that bob.GroupID was set back to 1 by the delete handler.
	var got models.User
	if err := env.Srv.DB.First(&got, uid).Error; err != nil {
		t.Fatalf("reload user from DB: %v", err)
	}
	if got.GroupID != 1 {
		t.Fatalf("bob.GroupID = %d, want 1 (default)", got.GroupID)
	}
}

// TestE2E_UserList_FilterByGroupID verifies the admin user list endpoint
// supports ?group_id= filtering, used by Group detail's "members" tab.
func TestE2E_UserList_FilterByGroupID(t *testing.T) {
	env := setupFullEnv(t, "user-list-by-group", 0)
	defer env.Close()

	// Create a non-default group "paid".
	g := adminPostJSON(t, env, "/api/admin/user-groups", map[string]any{
		"name": "paid",
	})
	paidGID := uint(g["id"].(float64))

	// alice in default group (id=1), bob in paid group.
	aResp := env.DoAdmin("POST", "/api/admin/users", map[string]any{
		"username": "alice", "password": "pw", "role": 1,
	})
	aResp.Body.Close()
	bResp := env.DoAdmin("POST", "/api/admin/users", map[string]any{
		"username": "bob", "password": "pw", "role": 1, "group_id": paidGID,
	})
	bResp.Body.Close()

	// GET /api/admin/users?group_id=<paidGID> should return only bob.
	listResp := env.DoAdmin("GET",
		fmt.Sprintf("/api/admin/users?group_id=%d", paidGID), nil)
	defer listResp.Body.Close()
	raw, _ := io.ReadAll(listResp.Body)
	var listed struct {
		Data  []map[string]any `json:"data"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode list: %v body=%s", err, raw)
	}
	if listed.Total != 1 {
		t.Fatalf("expected total=1, got %d body=%s", listed.Total, raw)
	}
	if listed.Data[0]["username"] != "bob" {
		t.Fatalf("expected bob, got %v", listed.Data[0])
	}

	// Verify the JOIN-derived group_name is exposed in the row.
	if listed.Data[0]["group_name"] != "paid" {
		t.Fatalf("expected group_name=paid, got %v", listed.Data[0]["group_name"])
	}

	_ = models.User{} // keep import
}

// TestE2E_UserGroup_NameConflict verifies that creating or renaming a group to
// an existing name returns HTTP 409 (Conflict) — used by the frontend toast
// to show the localized "name conflict" message instead of a generic error.
func TestE2E_UserGroup_NameConflict(t *testing.T) {
	env := setupFullEnv(t, "user-group-name-conflict", 0)
	defer env.Close()

	// Seed an initial group "team-a".
	g := adminPostJSON(t, env, "/api/admin/user-groups", map[string]any{"name": "team-a"})
	firstID := uint(g["id"].(float64))

	// Creating another group with the same name → 409.
	dupResp := env.DoAdmin("POST", "/api/admin/user-groups", map[string]any{"name": "team-a"})
	defer dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(dupResp.Body)
		t.Fatalf("create duplicate: status=%d body=%s, want 409", dupResp.StatusCode, raw)
	}

	// Create a second group "team-b" and try to rename it to "team-a" → 409.
	g2 := adminPostJSON(t, env, "/api/admin/user-groups", map[string]any{"name": "team-b"})
	secondID := uint(g2["id"].(float64))

	renameResp := env.DoAdmin("PUT",
		fmt.Sprintf("/api/admin/user-groups/%d", secondID),
		map[string]any{"name": "team-a"})
	defer renameResp.Body.Close()
	if renameResp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(renameResp.Body)
		t.Fatalf("rename to existing: status=%d body=%s, want 409", renameResp.StatusCode, raw)
	}

	// Updating a group with its own current name should NOT conflict.
	noopResp := env.DoAdmin("PUT",
		fmt.Sprintf("/api/admin/user-groups/%d", firstID),
		map[string]any{"name": "team-a"})
	defer noopResp.Body.Close()
	if noopResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(noopResp.Body)
		t.Fatalf("rename to own name: status=%d body=%s, want 200", noopResp.StatusCode, raw)
	}
}
