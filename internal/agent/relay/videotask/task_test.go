package videotask

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"strings"
	"testing"
)

func TestHandleBinding(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{BaseURL: "https://provider.invalid"}, Key: "secret"}
	task := Task{UserID: 1, TokenID: 2, Source: "admin", ChannelID: 3, Model: "video", RequestModel: "alias", ID: "upstream-id"}
	handle, err := task.Seal(ch)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(handle)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Verify(ch, 1, 2) || parsed.Verify(ch, 2, 2) || parsed.Verify(ch, 1, 3) {
		t.Fatal("owner binding failed")
	}
	ch.BaseURL = "https://other.invalid"
	if parsed.Verify(ch, 1, 2) {
		t.Fatal("host change accepted")
	}
	ch.BaseURL = "https://provider.invalid"
	ch.Key = "rotated"
	if parsed.Verify(ch, 1, 2) {
		t.Fatal("rotated credential accepted")
	}
	if strings.Contains(handle, "secret") {
		t.Fatal("credential in handle")
	}
}
func TestRejectUnsafeIDs(t *testing.T) {
	ch := &models.Channel{Key: "secret"}
	for _, id := range []string{"../task", "..", "a%2fb", "a?x=1", "a#b", "a\\b", "a\nb"} {
		if _, err := (&Task{TokenID: 1, Source: "admin", ChannelID: 1, Model: "video", RequestModel: "video", ID: id}).Seal(ch); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
}

func TestOwnerlessTokenHandle(t *testing.T) {
	ch := &models.Channel{Key: "secret"}
	handle, err := (&Task{TokenID: 1, Source: "admin", ChannelID: 2, Model: "video", RequestModel: "video", ID: "job"}).Seal(ch)
	if err != nil {
		t.Fatal(err)
	}
	task, err := Parse(handle)
	if err != nil {
		t.Fatal(err)
	}
	if !task.Verify(ch, 0, 1) || task.Verify(ch, 0, 2) {
		t.Fatal("ownerless tokens must remain isolated")
	}
}

func TestLongNativeIDStillProducesUsableHandle(t *testing.T) {
	ch := &models.Channel{Key: "secret"}
	handle, err := (&Task{TokenID: 1, Source: "admin", ChannelID: 2, Model: "video", RequestModel: "video", ID: strings.Repeat("x", 1024)}).Seal(ch)
	if err != nil {
		t.Fatal(err)
	}
	got, _, ok := Path("/v1/videos/" + handle)
	if !ok || got != handle {
		t.Fatal("gateway rejected its own handle")
	}
	if _, err := Parse(handle); err != nil {
		t.Fatal(err)
	}
}
