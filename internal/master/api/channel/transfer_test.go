package channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

func TestAdminChannelTransferMapping(t *testing.T) {
	t.Run("success exports plaintext and typed fields", func(t *testing.T) {
		row := models.Channel{
			ChannelCore: models.ChannelCore{Name: "main", Status: 0, Type: 1, Weight: 2},
			Key:         "secret", Models: "a,b", ModelMapping: `{"a":"upstream"}`,
			Limit: datatypes.NewJSONType(models.ChannelLimit{}),
		}
		file, err := adminChannelToFile(row)
		if err != nil {
			t.Fatal(err)
		}
		if file.Key != "secret" || file.Status != 0 || len(file.Models) != 2 || file.ModelMapping["a"] != "upstream" {
			t.Fatalf("unexpected file: %#v", file)
		}
	})

	t.Run("failure rejects corrupt stored mapping", func(t *testing.T) {
		_, err := adminChannelToFile(models.Channel{ModelMapping: "{"})
		if err == nil {
			t.Fatal("corrupt mapping exported")
		}
	})

	t.Run("boundary preserves disabled status and zero overrides", func(t *testing.T) {
		file := channelfile.AdminChannel{Name: "off", Status: 0, PriceRatio: 0}
		input := adminFileToCreateInput(file, "off-2")
		if input.Name != "off-2" || input.Status != 0 || input.PriceRatio != 0 {
			t.Fatalf("unexpected input: %#v", input)
		}
	})
}
