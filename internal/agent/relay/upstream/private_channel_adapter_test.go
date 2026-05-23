package upstream

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestProjectPrivateChannel_Basics(t *testing.T) {
	pc := &protocol.SyncedPrivateChannel{ChannelCore: models.ChannelCore{ID: 7, Type: 1, BaseURL: "https://api.openai.com", Weight: 3, Priority: 5, Status: 1}, KeyPlaintext: "sk-x", Models: []string{"gpt-4o", "gpt-3.5-turbo"}}
	ch := ProjectPrivateChannelToChannel(pc)
	if ch.ID != 7 || ch.Key != "sk-x" || ch.Weight != 3 {
		t.Fatalf("basic fields not copied: %+v", ch)
	}
	if ch.Models != "gpt-4o,gpt-3.5-turbo" {
		t.Fatalf("CSV mismatch: %q", ch.Models)
	}
	// Task 15: priority offset 移除——private channel 投影时不再 +10000，
	// "private 优先" 由 sort.go 的 source rank 二级排序保证。
	if ch.Priority != 5 {
		t.Fatalf("priority must be passed through unchanged, got %d (want 5)", ch.Priority)
	}
}

func TestProjectPrivateChannel_ModelMapping(t *testing.T) {
	pc := &protocol.SyncedPrivateChannel{
		ModelMapping: map[string]string{"gpt-4o-mini": "gpt-4o"},
	}
	ch := ProjectPrivateChannelToChannel(pc)
	if ch.ModelMapping == "" {
		t.Fatal("model_mapping not marshaled")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(ch.ModelMapping), &got); err != nil {
		t.Fatalf("model_mapping not valid JSON: %v", err)
	}
	if got["gpt-4o-mini"] != "gpt-4o" {
		t.Fatalf("mapping content wrong: %+v", got)
	}
}

func TestProjectPrivateChannel_EmptyMappingProducesEmptyString(t *testing.T) {
	pc := &protocol.SyncedPrivateChannel{}
	ch := ProjectPrivateChannelToChannel(pc)
	if ch.ModelMapping != "" {
		t.Fatalf("empty mapping should produce empty string, got %q", ch.ModelMapping)
	}
}

func TestProjectPrivateChannel_NoHeaderOverrideField(t *testing.T) {
	pc := &protocol.SyncedPrivateChannel{ChannelCore: models.ChannelCore{ID: 1, Type: 1, Status: 1}, KeyPlaintext: "x"}
	ch := ProjectPrivateChannelToChannel(pc)
	if ch.HeaderOverride != "" || ch.ProxyURL != "" {
		t.Fatalf("HeaderOverride / ProxyURL must remain empty: HO=%q PU=%q",
			ch.HeaderOverride, ch.ProxyURL)
	}
}
