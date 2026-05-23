package user_group

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/gorm"
)

// invalidateRecorder subscribes to the private_channel.invalidate topic on a
// real MemoryBus and records every affected user ID it observes. Tests assert
// per-user delivery via invalidated(uid).
type invalidateRecorder struct {
	mu   sync.Mutex
	seen map[uint]bool
	done chan struct{}
}

func newInvalidateRecorder(t *testing.T, bus app.EventBus) *invalidateRecorder {
	t.Helper()
	r := &invalidateRecorder{seen: map[uint]bool{}, done: make(chan struct{}, 16)}
	_, err := bus.Subscribe("private_channel.invalidate", func(ctx context.Context, ev eventbus.Event) error {
		var p protocol.PrivateChannelInvalidatePayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		r.mu.Lock()
		for _, id := range p.AffectedUserIDs {
			r.seen[id] = true
		}
		r.mu.Unlock()
		select {
		case r.done <- struct{}{}:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return r
}

func (r *invalidateRecorder) invalidated(uid uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[uid]
}

// waitForEvent waits up to 200ms for at least one delivery; tests that expect
// "no event" can skip calling this since MemoryBus dispatches in goroutines.
func (r *invalidateRecorder) waitForEvent(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for invalidate event")
	}
}

// drain gives any in-flight async publishes a brief window to settle so the
// "no event delivered to user X" assertions are reliable.
func (r *invalidateRecorder) drain() {
	time.Sleep(50 * time.Millisecond)
}

func seedUser(t *testing.T, db *gorm.DB, id uint, name string, groupID uint) {
	t.Helper()
	u := models.User{ID: id, Username: name, Password: "x", GroupID: groupID}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
}

func seedUserGroup(t *testing.T, db *gorm.DB, id uint, name string) {
	t.Helper()
	g := models.UserGroup{ID: id, Name: name}
	if err := db.Create(&g).Error; err != nil {
		t.Fatalf("seed group %s: %v", name, err)
	}
}

func TestFanoutBYOKInvalidateForGroup_FiresPerUser(t *testing.T) {
	_, ctx, db := setupBYOKTest(t)
	seedUserGroup(t, db, 100, "g1")
	seedUser(t, db, 10, "u10", 100)
	seedUser(t, db, 11, "u11", 100)

	bus := ctx.App.GetEventBus()
	rec := newInvalidateRecorder(t, bus)

	q := dao.NewAdminQuery(dao.NewContext(ctx.App))
	if err := fanoutBYOKInvalidateForGroup(context.Background(), q, bus, 100); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	rec.waitForEvent(t)

	if !rec.invalidated(10) {
		t.Fatal("u10 not invalidated")
	}
	if !rec.invalidated(11) {
		t.Fatal("u11 not invalidated")
	}
}

func TestFanoutBYOKInvalidateForGroup_EmptyGroupOK(t *testing.T) {
	_, ctx, db := setupBYOKTest(t)
	seedUserGroup(t, db, 9, "empty")

	bus := ctx.App.GetEventBus()
	rec := newInvalidateRecorder(t, bus)

	q := dao.NewAdminQuery(dao.NewContext(ctx.App))
	if err := fanoutBYOKInvalidateForGroup(context.Background(), q, bus, 9); err != nil {
		t.Fatalf("empty group must not error, got %v", err)
	}
	rec.drain()
	// No users → must not publish anything.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.seen) != 0 {
		t.Fatalf("expected no invalidate events for empty group, got %v", rec.seen)
	}
}

func TestFanoutBYOKInvalidateForGroup_DoesNotTouchOtherGroups(t *testing.T) {
	_, ctx, db := setupBYOKTest(t)
	seedUserGroup(t, db, 100, "g1")
	seedUserGroup(t, db, 200, "g2")
	seedUser(t, db, 10, "u10", 100)
	seedUser(t, db, 20, "u20", 200)

	bus := ctx.App.GetEventBus()
	rec := newInvalidateRecorder(t, bus)

	q := dao.NewAdminQuery(dao.NewContext(ctx.App))
	if err := fanoutBYOKInvalidateForGroup(context.Background(), q, bus, 100); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	rec.waitForEvent(t)

	if !rec.invalidated(10) {
		t.Fatal("u10 should be invalidated")
	}
	if rec.invalidated(20) {
		t.Fatal("u20 (other group) wrongly invalidated")
	}
}

func TestUpdate_BYOKEnabledFlipTriggersFanout(t *testing.T) {
	// Real handler-level wiring: PATCH {byok_enabled: false} on a group with
	// members must invalidate each member's private-channel cache.
	h, ctx, db := setupBYOKTest(t)
	seedUserGroup(t, db, 100, "flip-target")
	seedUser(t, db, 10, "u10", 100)
	seedUser(t, db, 11, "u11", 100)
	// Cross-group control user — must not see the event.
	seedUserGroup(t, db, 200, "untouched")
	seedUser(t, db, 20, "u20", 200)

	bus := ctx.App.GetEventBus()
	rec := newInvalidateRecorder(t, bus)

	req := UpdateRequest{ID: "100"}
	req.SetBodyMap(map[string]any{"byok_enabled": false})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rec.waitForEvent(t)

	if !rec.invalidated(10) || !rec.invalidated(11) {
		t.Fatalf("expected u10+u11 invalidated, got %v", rec.seen)
	}
	if rec.invalidated(20) {
		t.Fatal("u20 from other group must not be invalidated")
	}
}

func TestUpdate_WithoutBYOKEnabledKeyDoesNotFanout(t *testing.T) {
	// PATCH that touches unrelated fields must NOT fan out invalidate events.
	h, ctx, db := setupBYOKTest(t)
	seedUserGroup(t, db, 100, "rename-only")
	seedUser(t, db, 10, "u10", 100)

	bus := ctx.App.GetEventBus()
	rec := newInvalidateRecorder(t, bus)

	req := UpdateRequest{ID: "100"}
	req.SetBodyMap(map[string]any{"description": "renamed"})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rec.drain()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.seen) != 0 {
		t.Fatalf("unrelated PATCH must not fan out invalidate, got %v", rec.seen)
	}
}

func TestFanoutBYOKInvalidateForGroup_NilBusIsNoOp(t *testing.T) {
	// Handlers wired without an event bus must not panic — group-level fanout
	// should silently skip publishing when bus is nil even if users exist.
	_, ctx, db := setupBYOKTest(t)
	seedUserGroup(t, db, 100, "g1")
	seedUser(t, db, 10, "u10", 100)

	q := dao.NewAdminQuery(dao.NewContext(ctx.App))
	if err := fanoutBYOKInvalidateForGroup(context.Background(), q, nil, 100); err != nil {
		t.Fatalf("nil bus must not error, got %v", err)
	}
}
