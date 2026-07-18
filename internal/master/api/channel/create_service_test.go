package channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestPrepareAdminChannels(t *testing.T) {
	t.Run("success applies defaults and structured fields", func(t *testing.T) {
		rows, err := prepareAdminChannels([]AdminChannelCreateInput{{
			Name: "primary", Status: 1, Models: []string{"a", "b"},
			ModelMapping: map[string]string{"a": "upstream-a"}, PriceRatio: 1,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Weight != 1 || rows[0].Models != "a,b" || rows[0].ModelMapping != `{"a":"upstream-a"}` {
			t.Fatalf("unexpected row: %#v", rows)
		}
	})

	t.Run("failure rejects invalid member before any write", func(t *testing.T) {
		_, err := prepareAdminChannels([]AdminChannelCreateInput{
			{Name: "valid", Status: 1, PriceRatio: 1},
			{Name: "invalid", Status: 2, PriceRatio: 1},
		})
		if err == nil {
			t.Fatal("invalid status accepted")
		}
	})

	t.Run("boundary validates nested zero values", func(t *testing.T) {
		badRetry := -1
		_, err := prepareAdminChannels([]AdminChannelCreateInput{{
			Name: "bad", Status: 0, PriceRatio: 1,
			Resilience: models.ChannelResilience{MaxRetries: &badRetry},
		}})
		if err == nil {
			t.Fatal("invalid resilience accepted")
		}
	})
}
