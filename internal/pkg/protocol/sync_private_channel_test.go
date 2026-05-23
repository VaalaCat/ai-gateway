package protocol

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestSyncedPrivateChannel_RoundTripJSON(t *testing.T) {
	pc := SyncedPrivateChannel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: 1, BaseURL: "https://api.openai.com",
			Weight: 1, Priority: 0, Status: 1,
		},
		OwnerID:      42,
		KeyPlaintext: "sk-test",
		Models:       []string{"gpt-4o"},
		ModelMapping: map[string]string{"front": "back"},
	}
	b, err := json.Marshal(pc)
	if err != nil { t.Fatal(err) }
	var got SyncedPrivateChannel
	if err := json.Unmarshal(b, &got); err != nil { t.Fatal(err) }
	if got.KeyPlaintext != "sk-test" || got.Models[0] != "gpt-4o" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.ModelMapping["front"] != "back" {
		t.Fatalf("modelmapping mismatch: %+v", got.ModelMapping)
	}
}

func TestPrivateChannelInvalidatePayload_JSONShape(t *testing.T) {
	p := PrivateChannelInvalidatePayload{Action: "invalidate", AffectedUserIDs: []uint{1, 2, 3}}
	b, _ := json.Marshal(p)
	want := `{"action":"invalidate","affected_user_ids":[1,2,3]}`
	if string(b) != want {
		t.Fatalf("payload shape: got %s want %s", b, want)
	}
}

func TestVisiblePrivateChannelSet_EmptyChannels(t *testing.T) {
	set := VisiblePrivateChannelSet{UserID: 7, Channels: nil}
	b, _ := json.Marshal(set)
	var got VisiblePrivateChannelSet
	if err := json.Unmarshal(b, &got); err != nil { t.Fatal(err) }
	if got.UserID != 7 {
		t.Fatalf("user_id roundtrip failed: %+v", got)
	}
	if got.Channels != nil && len(got.Channels) != 0 {
		t.Fatalf("expected empty channels, got %+v", got.Channels)
	}
}
