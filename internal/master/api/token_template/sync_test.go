package token_template

import (
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestDiffToken(t *testing.T) {
	tpl := &models.TokenTemplate{
		ID:     12,
		Name:   "default",
		Models: `["gpt-4","gpt-5"]`,
	}
	tpl.AllowedChannelIDs = []uint{1, 2}

	t.Run("equal -> not changed", func(t *testing.T) {
		tok := &models.Token{ID: 100, Name: "tok", Models: `["gpt-5","gpt-4"]`}
		tok.AllowedChannelIDs = []uint{2, 1}
		changed, item := diffToken(tpl, tok)
		if changed {
			t.Fatalf("expected not changed, got changed=true item=%+v", item)
		}
	})

	t.Run("models changed -> PreviewItem populated", func(t *testing.T) {
		tok := &models.Token{ID: 100, Name: "tok", Models: `["gpt-4"]`}
		tok.AllowedChannelIDs = []uint{1, 2}
		changed, item := diffToken(tpl, tok)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if item.TokenID != 100 || item.TokenName != "tok" {
			t.Fatalf("item id/name wrong: %+v", item)
		}
		if item.ModelsBefore != `["gpt-4"]` || item.ModelsAfter != `["gpt-4","gpt-5"]` {
			t.Fatalf("models before/after wrong: %+v", item)
		}
		if !reflect.DeepEqual(item.ChannelsBefore, []uint{1, 2}) {
			t.Fatalf("ChannelsBefore = %v", item.ChannelsBefore)
		}
		if !reflect.DeepEqual(item.ChannelsAfter, []uint{1, 2}) {
			t.Fatalf("ChannelsAfter = %v", item.ChannelsAfter)
		}
	})

	t.Run("channels changed -> PreviewItem populated", func(t *testing.T) {
		tok := &models.Token{ID: 101, Name: "tok2", Models: `["gpt-4","gpt-5"]`}
		tok.AllowedChannelIDs = []uint{1}
		changed, item := diffToken(tpl, tok)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if !reflect.DeepEqual(item.ChannelsBefore, []uint{1}) {
			t.Fatalf("ChannelsBefore = %v", item.ChannelsBefore)
		}
		if !reflect.DeepEqual(item.ChannelsAfter, []uint{1, 2}) {
			t.Fatalf("ChannelsAfter = %v", item.ChannelsAfter)
		}
	})
}
