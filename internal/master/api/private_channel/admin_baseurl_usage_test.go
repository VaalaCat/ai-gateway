package private_channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestAdminBaseURLUsage_ReturnsCountAndChannels(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1/"},
		OwnerID:     1, Name: "a", Status: 1,
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v2/"},
		OwnerID:     1, Name: "b", Status: 1,
	})
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.anthropic.com/v1/"},
		OwnerID:     1, Name: "c", Status: 1,
	})

	resp, err := h.AdminBaseURLUsage(ctx, AdminBaseURLUsageRequest{Prefix: "https://api.openai.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("count=%d want 2", resp.Count)
	}
	if len(resp.Channels) != 2 {
		t.Fatalf("channels len=%d want 2: %+v", len(resp.Channels), resp.Channels)
	}
}

func TestAdminBaseURLUsage_NoMatchReturnsEmptyArray(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.PrivateChannel{
		ChannelCore: models.ChannelCore{Type: 1, BaseURL: "https://api.openai.com/v1/"},
		OwnerID:     1, Name: "a", Status: 1,
	})

	resp, err := h.AdminBaseURLUsage(ctx, AdminBaseURLUsageRequest{Prefix: "https://nope.example/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("count=%d want 0", resp.Count)
	}
	// JSON serialisation contract: channels must be [] not null.
	if resp.Channels == nil {
		t.Fatal("channels must be a non-nil empty slice (frontend expects an array)")
	}
	if len(resp.Channels) != 0 {
		t.Fatalf("channels=%+v want empty", resp.Channels)
	}
}
