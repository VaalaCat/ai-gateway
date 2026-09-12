package chat

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type chatReasoningAccumulator struct {
	text     strings.Builder
	active   bool
	finished bool
}

func (state *chatReasoningAccumulator) append(ch chan<- ir.Event, fragment string) {
	if fragment == "" || state.finished {
		return
	}
	state.active = true
	state.text.WriteString(fragment)
	ch <- ir.Event{Type: ir.EventReasoningSummaryDelta, Reasoning: &ir.ReasoningContent{Summary: []string{fragment}}}
	ch <- ir.Event{Type: ir.EventReasoningContentDelta, Reasoning: &ir.ReasoningContent{Content: []string{fragment}}}
	ch <- ir.Event{Type: ir.EventThinkingDelta, Delta: &ir.DeltaPayload{ContentType: ir.ContentTypeThinking, Text: fragment}}
}

func (state *chatReasoningAccumulator) finish(ch chan<- ir.Event, status ir.ReasoningStatus) {
	if !state.active || state.finished {
		return
	}
	state.finished = true
	text := state.text.String()
	raw, _ := json.Marshal(map[string]string{"reasoning_content": text})
	ch <- ir.Event{
		Type:            ir.EventReasoningDone,
		ReasoningStatus: status,
		Reasoning: &ir.ReasoningContent{
			Summary: []string{text},
			Content: []string{text},
			RawJSON: raw,
		},
	}
}
