package upstream

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestThinkingRules_SendBack(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: `{"model_thinking_passthrough":[{"model_pattern":"deepseek.*","send_back_thinking":true}]}`}}
	tr := NewThinkingRules(ch)
	if !tr.SendBack("deepseek-v4") {
		t.Fatal("want SendBack true for deepseek-v4")
	}
	if tr.SendBack("gpt-4") {
		t.Fatal("want SendBack false for gpt-4")
	}
}

func TestThinkingRules_EmptyChannel(t *testing.T) {
	if NewThinkingRules(&models.Channel{}).SendBack("anything") {
		t.Fatal("empty channel must yield false")
	}
}
