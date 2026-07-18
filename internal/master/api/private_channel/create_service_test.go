package private_channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestBuildPrivateChannel(t *testing.T) {
	t.Run("success keeps owner status and typed fields", func(t *testing.T) {
		row := buildPrivateChannel(7, PrivateChannelCreateInput{
			Name: "mine", Status: 0, Type: 1, Key: "secret", Models: []string{"gpt-4o"},
			ModelMapping: map[string]string{"gpt-4o": "upstream"},
		}, []byte("ciphertext"))
		if row.OwnerID != 7 || row.Status != 0 || row.Weight != 1 || row.Name != "mine" {
			t.Fatalf("unexpected row: %#v", row)
		}
		if got := row.ModelMapping.Data()["gpt-4o"]; got != "upstream" {
			t.Fatalf("mapping = %q", got)
		}
	})

	t.Run("boundary keeps affinity zero override", func(t *testing.T) {
		zero := 0
		row := buildPrivateChannel(1, PrivateChannelCreateInput{
			Name: "mine", Status: 1, Type: 1, Affinity: models.ChannelAffinity{TTLSec: &zero},
		}, []byte("ciphertext"))
		if row.Affinity.Data().TTLSec == nil || *row.Affinity.Data().TTLSec != 0 {
			t.Fatalf("affinity = %#v", row.Affinity.Data())
		}
	})

	t.Run("failure does not alias caller maps", func(t *testing.T) {
		mapping := map[string]string{"a": "b"}
		row := buildPrivateChannel(1, PrivateChannelCreateInput{
			Name: "mine", Status: 1, Type: 1, ModelMapping: mapping,
		}, []byte("ciphertext"))
		mapping["a"] = "changed"
		if row.ModelMapping.Data()["a"] != "b" {
			t.Fatal("stored mapping aliases caller")
		}
	})
}
