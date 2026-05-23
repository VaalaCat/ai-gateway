package user_group

import (
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// §5.4: BYOKMaxChannels write-time rejection of negative values. The CREATE path
// should also reject; the UPDATE path PATCH map should reject equally.

func TestCreate_NegativeBYOKMaxChannelsRejected(t *testing.T) {
	h, ctx, _ := setupBYOKTest(t)
	neg := -1
	req := CreateRequest{
		Name:            "bad-group",
		BYOKMaxChannels: &neg,
	}
	_, err := h.Create(ctx, req)
	if err == nil {
		t.Fatal("Create with negative BYOKMaxChannels should be rejected")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("expected 400 BadRequest, got %d (%s)", apiErr.Status, apiErr.Message)
	}
}

func TestUpdate_NegativeBYOKMaxChannelsRejected(t *testing.T) {
	h, ctx, db := setupBYOKTest(t)

	// Seed a group we can patch.
	g := models.UserGroup{Name: "patchable"}
	if err := db.Create(&g).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := UpdateRequest{ID: strconv.FormatUint(uint64(g.ID), 10)}
	req.SetBodyMap(map[string]any{"byok_max_channels": float64(-3)}) // json number → float64

	_, err := h.Update(ctx, req)
	if err == nil {
		t.Fatal("PATCH negative byok_max_channels should be rejected")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("expected 400, got %d (%s)", apiErr.Status, apiErr.Message)
	}
}

func TestUpdate_ZeroBYOKMaxChannelsAllowed(t *testing.T) {
	// 0 means "quota disabled by admin" — must be accepted on write.
	h, ctx, db := setupBYOKTest(t)

	g := models.UserGroup{Name: "zero-quota"}
	if err := db.Create(&g).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := UpdateRequest{ID: strconv.FormatUint(uint64(g.ID), 10)}
	req.SetBodyMap(map[string]any{"byok_max_channels": float64(0)})

	if _, err := h.Update(ctx, req); err != nil {
		t.Fatalf("PATCH byok_max_channels=0 should succeed: %v", err)
	}
}
