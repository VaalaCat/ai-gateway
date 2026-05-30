package channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
)

func ptrBool(b bool) *bool { return &b }

func TestCreate_FreeChannel(t *testing.T) {
	db := setupTestDB(t)
	ctx := newTestContext(t, db, "")
	ctx.App.SetEventBus(eventbus.NewMemoryBus())
	h := &Handler{}

	t.Run("success: free=true persists", func(t *testing.T) {
		res, err := h.Create(ctx, CreateRequest{Name: "free-ch", Free: ptrBool(true)})
		if err != nil {
			t.Fatalf("Create err=%v", err)
		}
		if !res.Value.Free {
			t.Fatalf("created channel Free=false, want true")
		}
	})

	t.Run("default: false when omitted", func(t *testing.T) {
		res, err := h.Create(ctx, CreateRequest{Name: "normal-ch"})
		if err != nil {
			t.Fatalf("Create err=%v", err)
		}
		if res.Value.Free {
			t.Fatalf("created channel Free=true, want false (default)")
		}
	})
}

func TestUpdate_FreeChannel_Toggle(t *testing.T) {
	db := setupTestDB(t)
	ctx := newTestContext(t, db, "")
	ctx.App.SetEventBus(eventbus.NewMemoryBus())
	h := &Handler{}

	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "ch", Status: 1}})

	req := UpdateRequest{ID: "1"}
	req.SetBodyMap(map[string]any{"free": true})
	ch, err := h.Update(ctx, req)
	if err != nil {
		t.Fatalf("Update err=%v", err)
	}
	if !ch.Free {
		t.Fatalf("updated channel Free=false, want true")
	}
}
