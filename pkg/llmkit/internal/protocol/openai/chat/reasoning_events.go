package chat

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type chatReasoningAccumulator struct {
	text      strings.Builder
	active    bool
	finished  bool
	signature string
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
	if (!state.active && state.signature == "") || state.finished {
		return
	}
	state.finished = true
	text := state.text.String()
	ch <- ir.Event{
		Type:            ir.EventReasoningDone,
		ReasoningStatus: status,
		Reasoning:       chatReasoningContent(text, state.signature),
	}
}

func chatReasoningContent(text, signature string) *ir.ReasoningContent {
	fields := map[string]string{"reasoning_content": text}
	if signature != "" {
		fields["signature"] = signature
	}
	raw, _ := json.Marshal(fields)
	return &ir.ReasoningContent{
		Summary: []string{text},
		Content: []string{text},
		RawJSON: raw,
	}
}
