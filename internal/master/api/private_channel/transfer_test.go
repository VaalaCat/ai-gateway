package private_channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"gorm.io/datatypes"
)

func TestPrivateChannelTransferMapping(t *testing.T) {
	cipher, err := byokcrypto.NewFromConfig("", "transfer-test-secret")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success decrypts plaintext and typed fields", func(t *testing.T) {
		sealed, err := cipher.Seal("plain-key", 7)
		if err != nil {
			t.Fatal(err)
		}
		row := models.PrivateChannel{
			ChannelCore: models.ChannelCore{Type: 1}, OwnerID: 7, Name: "mine", Status: 0,
			KeyCipher: sealed, Models: datatypes.JSONSlice[string]{"gpt-4o"},
			ModelMapping: datatypes.NewJSONType(map[string]string{"gpt-4o": "upstream"}),
		}
		file, err := privateChannelToFile(row, cipher)
		if err != nil {
			t.Fatal(err)
		}
		if file.Key != "plain-key" || file.Status != 0 || file.ModelMapping["gpt-4o"] != "upstream" {
			t.Fatalf("unexpected file: %#v", file)
		}
	})

	t.Run("failure rejects ciphertext for another owner", func(t *testing.T) {
		sealed, _ := cipher.Seal("plain-key", 7)
		_, err := privateChannelToFile(models.PrivateChannel{OwnerID: 8, KeyCipher: sealed}, cipher)
		if err == nil {
			t.Fatal("owner mismatch decrypted")
		}
	})

	t.Run("boundary preserves disabled and zero fields", func(t *testing.T) {
		input := privateFileToCreateInput(channelfile.BYOKChannel{
			Name: "off", Status: 0, Type: 1, Models: []string{"m"}, ModelMapping: map[string]string{},
		}, "off-2")
		if input.Name != "off-2" || input.Status != 0 || input.Weight != 0 {
			t.Fatalf("unexpected input: %#v", input)
		}
	})
}
