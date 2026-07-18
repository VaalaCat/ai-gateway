package dao

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func createAgentRouteForUpdate(t *testing.T, mutation AdminAgentRouteMutation, route models.AgentRoute) models.AgentRoute {
	t.Helper()
	if err := mutation.Create(&route); err != nil {
		t.Fatalf("create agent route: %v", err)
	}
	return route
}

func TestAgentRouteUpdate_SelectorTransitionsPersistExplicitEmptyValues(t *testing.T) {
	tests := []struct {
		name         string
		initialID    string
		initialTag   string
		updatedID    string
		updatedTag   string
		wantAgentID  string
		wantAgentTag string
	}{
		{name: "id to tag", initialID: "agent-a", updatedTag: "green", wantAgentTag: "green"},
		{name: "tag to id", initialTag: "blue", updatedID: "agent-a", wantAgentID: "agent-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			mutation := NewAdminMutation(ctx).AgentRoute()
			route := createAgentRouteForUpdate(t, mutation, models.AgentRoute{
				SourceType: "token",
				SourceID:   10,
				Model:      tt.name,
				AgentID:    tt.initialID,
				AgentTag:   tt.initialTag,
			})

			updated := route
			updated.AgentID = tt.updatedID
			updated.AgentTag = tt.updatedTag
			updated.Priority = updated.CalcPriority()
			if err := mutation.Update(&updated); err != nil {
				t.Fatalf("update agent route: %v", err)
			}

			var reloaded models.AgentRoute
			if err := db.First(&reloaded, route.ID).Error; err != nil {
				t.Fatalf("reload agent route: %v", err)
			}
			if reloaded.AgentID != tt.wantAgentID || reloaded.AgentTag != tt.wantAgentTag {
				t.Fatalf("persisted selectors = (%q, %q), want (%q, %q)", reloaded.AgentID, reloaded.AgentTag, tt.wantAgentID, tt.wantAgentTag)
			}
			if reloaded.ID != route.ID || reloaded.CreatedAt != route.CreatedAt {
				t.Fatalf("immutable fields changed: got id=%d created_at=%d, want id=%d created_at=%d", reloaded.ID, reloaded.CreatedAt, route.ID, route.CreatedAt)
			}
		})
	}
}

func TestAgentRouteUpdate_UniqueConflictLeavesRowUnchanged(t *testing.T) {
	ctx, db := setupAdminContext(t)
	mutation := NewAdminMutation(ctx).AgentRoute()
	createAgentRouteForUpdate(t, mutation, models.AgentRoute{
		SourceType: "token",
		SourceID:   10,
		Model:      "gpt-4o",
		AgentID:    "agent-a",
	})
	target := createAgentRouteForUpdate(t, mutation, models.AgentRoute{
		SourceType: "token",
		SourceID:   10,
		Model:      "claude",
		AgentTag:   "blue",
	})

	conflicting := target
	conflicting.Model = "gpt-4o"
	conflicting.AgentID = "agent-a"
	conflicting.AgentTag = ""
	conflicting.Priority = conflicting.CalcPriority()
	err := mutation.Update(&conflicting)
	if err == nil {
		t.Fatal("expected unique conflict")
	}
	if !IsAgentRouteUniqueConflict(err) {
		t.Fatalf("IsAgentRouteUniqueConflict(%v) = false, want true", err)
	}

	var reloaded models.AgentRoute
	if err := db.First(&reloaded, target.ID).Error; err != nil {
		t.Fatalf("reload target route: %v", err)
	}
	if reloaded.SourceType != target.SourceType || reloaded.SourceID != target.SourceID ||
		reloaded.Model != target.Model || reloaded.AgentID != target.AgentID ||
		reloaded.AgentTag != target.AgentTag || reloaded.Priority != target.Priority ||
		reloaded.CreatedAt != target.CreatedAt || reloaded.UpdatedAt != target.UpdatedAt {
		t.Fatalf("conflicting update changed row: got %#v, want %#v", reloaded, target)
	}
}

func TestAgentRouteUpdate_WritesSelectedUpdatedAtAndPreservesCreatedAt(t *testing.T) {
	ctx, db := setupAdminContext(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	db.Config.NowFunc = func() time.Time { return now }
	mutation := NewAdminMutation(ctx).AgentRoute()
	route := createAgentRouteForUpdate(t, mutation, models.AgentRoute{
		SourceType: "token",
		SourceID:   10,
		AgentID:    "agent-a",
	})
	createdAt := route.CreatedAt
	if route.UpdatedAt != now.Unix() {
		t.Fatalf("initial updated_at = %d, want %d", route.UpdatedAt, now.Unix())
	}

	now = now.Add(2 * time.Minute)
	updated := route
	updated.Model = "gpt-4o"
	updated.Priority = updated.CalcPriority()
	if err := mutation.Update(&updated); err != nil {
		t.Fatalf("update agent route: %v", err)
	}

	var reloaded models.AgentRoute
	if err := db.First(&reloaded, route.ID).Error; err != nil {
		t.Fatalf("reload agent route: %v", err)
	}
	if reloaded.UpdatedAt != now.Unix() {
		t.Fatalf("updated_at = %d, want %d", reloaded.UpdatedAt, now.Unix())
	}
	if reloaded.CreatedAt != createdAt {
		t.Fatalf("created_at = %d, want preserved %d", reloaded.CreatedAt, createdAt)
	}
}

func TestAgentRouteUpdate_ZeroRowsReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	mutation := NewAdminMutation(ctx).AgentRoute()
	route := models.AgentRoute{
		ID:         9999,
		SourceType: "token",
		SourceID:   10,
		AgentID:    "agent-a",
	}

	err := mutation.Update(&route)
	if err == nil {
		t.Fatal("Update() error = nil, want zero-row update error")
	}
	if !errors.Is(err, ErrAgentRouteNotFound) {
		t.Fatalf("Update() error = %v, want errors.Is(_, ErrAgentRouteNotFound)", err)
	}
}

func TestAgentRouteUpdate_UniqueConflictClassifierSupportsWrappedGORMError(t *testing.T) {
	err := fmt.Errorf("wrapped duplicate: %w", gorm.ErrDuplicatedKey)
	if !IsAgentRouteUniqueConflict(err) {
		t.Fatalf("IsAgentRouteUniqueConflict(%v) = false, want true", err)
	}
}
