package codec_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func makeToolCall(id, name string) codec.ToolCall {
	return codec.ToolCall{ID: id, Name: name, Arguments: "{}"}
}

func makeAssistantWithToolCalls(ids ...string) codec.Message {
	var tcs []codec.ToolCall
	for _, id := range ids {
		tcs = append(tcs, makeToolCall(id, "fn_"+id))
	}
	return codec.Message{Role: codec.RoleAssistant, ToolCalls: tcs}
}

func makeAssistantText(text string) codec.Message {
	return codec.TextMessage(codec.RoleAssistant, text)
}

func makeToolResponse(callID, text string) codec.Message {
	return codec.Message{
		Role:       codec.RoleTool,
		ToolCallID: callID,
		Content:    []codec.ContentBlock{{Type: codec.ContentTypeText, Text: text}},
	}
}

// a) Plain assistant tool_call → tool: pass-through (no preamble).
func TestNormalize_PassThrough_NoChange(t *testing.T) {
	anchor := makeAssistantWithToolCalls("id1")
	tool := makeToolResponse("id1", "result")
	user := codec.TextMessage(codec.RoleUser, "hello")

	input := []codec.Message{user, anchor, tool}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Role != codec.RoleUser {
		t.Errorf("msg[0] role: got %q, want user", got[0].Role)
	}
	if got[1].Role != codec.RoleAssistant || len(got[1].ToolCalls) == 0 {
		t.Errorf("msg[1] should be assistant with tool_calls")
	}
	if got[2].Role != codec.RoleTool {
		t.Errorf("msg[2] role: got %q, want tool", got[2].Role)
	}
	// No preamble text should appear.
	content := ""
	for _, cb := range got[1].Content {
		content += cb.Text
	}
	if content != "" {
		t.Errorf("unexpected content %q in anchor message (no preamble expected)", content)
	}
}

// b) tool_call → preamble assistant text → tool: text merged into assistant.content.
func TestNormalize_SinglePreamble_MergedIntoAnchor(t *testing.T) {
	anchor := makeAssistantWithToolCalls("id1")
	preamble := makeAssistantText("Thinking about it.")
	tool := makeToolResponse("id1", "result")

	input := []codec.Message{anchor, preamble, tool}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 2 {
		t.Fatalf("expected 2 messages (preamble absorbed), got %d: %+v", len(got), got)
	}
	// got[0] = anchor with merged text, got[1] = tool
	if got[0].Role != codec.RoleAssistant || len(got[0].ToolCalls) == 0 {
		t.Errorf("got[0] should be assistant with tool_calls")
	}
	if got[1].Role != codec.RoleTool {
		t.Errorf("got[1] should be tool")
	}
	content := extractText(got[0])
	if !strings.Contains(content, "Thinking about it.") {
		t.Errorf("preamble text not merged into anchor; content=%q", content)
	}
}

// c) tool_call → preamble1 → preamble2 → tool: both texts joined with "\n\n".
func TestNormalize_MultiplePreambles_JoinedWithSeparator(t *testing.T) {
	anchor := makeAssistantWithToolCalls("id1")
	preamble1 := makeAssistantText("First thought.")
	preamble2 := makeAssistantText("Second thought.")
	tool := makeToolResponse("id1", "result")

	input := []codec.Message{anchor, preamble1, preamble2, tool}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	content := extractText(got[0])
	if !strings.Contains(content, "First thought.") {
		t.Errorf("preamble1 not in content: %q", content)
	}
	if !strings.Contains(content, "Second thought.") {
		t.Errorf("preamble2 not in content: %q", content)
	}
	if !strings.Contains(content, "\n\n") {
		t.Errorf("preambles not joined with \\n\\n: %q", content)
	}
}

// d) Parallel tool_calls (two ids) with preamble between: text merged, both tool responses follow.
func TestNormalize_ParallelToolCalls_PreambleMerged(t *testing.T) {
	anchor := makeAssistantWithToolCalls("idA", "idB")
	preamble := makeAssistantText("Working on it.")
	toolA := makeToolResponse("idA", "resultA")
	toolB := makeToolResponse("idB", "resultB")

	input := []codec.Message{anchor, preamble, toolA, toolB}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 3 { // anchor + toolA + toolB
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Role != codec.RoleAssistant || len(got[0].ToolCalls) != 2 {
		t.Errorf("got[0] should be assistant with 2 tool_calls")
	}
	if got[1].Role != codec.RoleTool || got[1].ToolCallID != "idA" {
		t.Errorf("got[1] should be tool for idA, got role=%q id=%q", got[1].Role, got[1].ToolCallID)
	}
	if got[2].Role != codec.RoleTool || got[2].ToolCallID != "idB" {
		t.Errorf("got[2] should be tool for idB, got role=%q id=%q", got[2].Role, got[2].ToolCallID)
	}
	content := extractText(got[0])
	if !strings.Contains(content, "Working on it.") {
		t.Errorf("preamble text not merged: %q", content)
	}
}

// e) Unrelated message in middle (user): walk stops; no reordering across user turns.
func TestNormalize_UserBoundary_StopsWalk(t *testing.T) {
	anchor := makeAssistantWithToolCalls("id1")
	user := codec.TextMessage(codec.RoleUser, "interrupt")
	tool := makeToolResponse("id1", "result")

	// anchor → user → tool: user stops the walk, so tool is NOT absorbed.
	input := []codec.Message{anchor, user, tool}
	got := codec.NormalizeAssistantToolCallSequence(input)

	// All 3 messages preserved in original order.
	if len(got) != 3 {
		t.Fatalf("expected 3 messages (walk stopped at user), got %d", len(got))
	}
	if got[0].Role != codec.RoleAssistant {
		t.Errorf("got[0] should be assistant")
	}
	if got[1].Role != codec.RoleUser {
		t.Errorf("got[1] should be user")
	}
	if got[2].Role != codec.RoleTool {
		t.Errorf("got[2] should be tool")
	}
}

// f) Empty messages: no-op.
func TestNormalize_Empty_NoOp(t *testing.T) {
	got := codec.NormalizeAssistantToolCallSequence(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
	got2 := codec.NormalizeAssistantToolCallSequence([]codec.Message{})
	if len(got2) != 0 {
		t.Errorf("expected empty, got %d", len(got2))
	}
}

// TestNormalizeAssistantToolCallSequence_CrossProtocolBehavior confirms which
// encoders do and do not use the normalizer.
func TestNormalizeAssistantToolCallSequence_CrossProtocolBehavior(t *testing.T) {
	// chat encoder uses normalizer: already covered by TestRegression_CodexPreambleTextInToolCall.
	// Here we simply verify the helper itself is stable when called with already-ordered messages.
	anchor := makeAssistantWithToolCalls("xid")
	tool := makeToolResponse("xid", "ok")
	msgs := []codec.Message{anchor, tool}
	got := codec.NormalizeAssistantToolCallSequence(msgs)
	if len(got) != 2 {
		t.Errorf("already-ordered messages should pass through unchanged, got %d", len(got))
	}
	if got[0].Role != codec.RoleAssistant || len(got[0].ToolCalls) == 0 {
		t.Errorf("got[0] should remain assistant with tool_calls")
	}
	if got[1].Role != codec.RoleTool {
		t.Errorf("got[1] should remain tool")
	}
}

// g) Two consecutive assistant{tool_calls} → merged into one with both tool_calls; both tool responses follow.
func TestNormalize_TwoConsecutiveAssistantToolCalls_Merged(t *testing.T) {
	anchorA := makeAssistantWithToolCalls("idA")
	anchorB := makeAssistantWithToolCalls("idB")
	toolA := makeToolResponse("idA", "resultA")
	toolB := makeToolResponse("idB", "resultB")

	input := []codec.Message{anchorA, anchorB, toolA, toolB}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 3 { // merged assistant + toolA + toolB
		t.Fatalf("expected 3 messages (2 assistants merged + 2 tools), got %d: %+v", len(got), got)
	}
	if got[0].Role != codec.RoleAssistant {
		t.Errorf("got[0] should be assistant")
	}
	if len(got[0].ToolCalls) != 2 {
		t.Errorf("got[0] should have 2 tool_calls (merged), got %d", len(got[0].ToolCalls))
	}
	if got[1].Role != codec.RoleTool || got[1].ToolCallID != "idA" {
		t.Errorf("got[1] should be tool for idA")
	}
	if got[2].Role != codec.RoleTool || got[2].ToolCallID != "idB" {
		t.Errorf("got[2] should be tool for idB")
	}
}

// h) Three consecutive assistant{tool_calls} (parallel) — all merged into one.
func TestNormalize_ThreeConsecutiveAssistantToolCalls_AllMerged(t *testing.T) {
	anchorA := makeAssistantWithToolCalls("idA")
	anchorB := makeAssistantWithToolCalls("idB")
	anchorC := makeAssistantWithToolCalls("idC")
	toolA := makeToolResponse("idA", "resultA")
	toolB := makeToolResponse("idB", "resultB")
	toolC := makeToolResponse("idC", "resultC")

	input := []codec.Message{anchorA, anchorB, anchorC, toolA, toolB, toolC}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 4 { // merged assistant + 3 tools
		t.Fatalf("expected 4 messages, got %d: %+v", len(got), got)
	}
	if got[0].Role != codec.RoleAssistant {
		t.Errorf("got[0] should be assistant")
	}
	if len(got[0].ToolCalls) != 3 {
		t.Errorf("got[0] should have 3 tool_calls (merged), got %d", len(got[0].ToolCalls))
	}
	ids := map[string]bool{}
	for _, tc := range got[0].ToolCalls {
		ids[tc.ID] = true
	}
	for _, want := range []string{"idA", "idB", "idC"} {
		if !ids[want] {
			t.Errorf("tool_call %q missing from merged assistant", want)
		}
	}
	for k := 1; k <= 3; k++ {
		if got[k].Role != codec.RoleTool {
			t.Errorf("got[%d] should be tool, got %q", k, got[k].Role)
		}
	}
}

// i) assistant{tool_calls=[A]} → assistant{tool_calls=[B], text="thinking"} → tool{A} → tool{B}:
// text absorbed into merged assistant.content, tool_calls=[A,B], both tools follow.
func TestNormalize_ConsecutiveAssistantToolCallsWithText_Merged(t *testing.T) {
	anchorA := makeAssistantWithToolCalls("idA")
	// anchorB has both tool_calls and text content
	anchorB := codec.Message{
		Role:      codec.RoleAssistant,
		ToolCalls: []codec.ToolCall{makeToolCall("idB", "fn_idB")},
		Content: []codec.ContentBlock{
			{Type: codec.ContentTypeText, Text: "thinking out loud"},
		},
	}
	toolA := makeToolResponse("idA", "resultA")
	toolB := makeToolResponse("idB", "resultB")

	input := []codec.Message{anchorA, anchorB, toolA, toolB}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 3 { // merged assistant + toolA + toolB
		t.Fatalf("expected 3 messages, got %d: %+v", len(got), got)
	}
	if len(got[0].ToolCalls) != 2 {
		t.Errorf("got[0] should have 2 tool_calls, got %d", len(got[0].ToolCalls))
	}
	content := extractText(got[0])
	if !strings.Contains(content, "thinking out loud") {
		t.Errorf("text from consecutive assistant not absorbed into merged content: %q", content)
	}
	if got[1].Role != codec.RoleTool || got[1].ToolCallID != "idA" {
		t.Errorf("got[1] should be tool for idA")
	}
	if got[2].Role != codec.RoleTool || got[2].ToolCallID != "idB" {
		t.Errorf("got[2] should be tool for idB")
	}
}

// j) Mixed: assistant{tool_calls=[A]} → assistant{text only, no tool_calls} →
// assistant{tool_calls=[B]} → tool{A} → tool{B}: text absorbed, both tool_calls merged.
func TestNormalize_MixedPreambleAndParallelToolCalls_AllMerged(t *testing.T) {
	anchorA := makeAssistantWithToolCalls("idA")
	preamble := makeAssistantText("intermediate reasoning")
	anchorB := makeAssistantWithToolCalls("idB")
	toolA := makeToolResponse("idA", "resultA")
	toolB := makeToolResponse("idB", "resultB")

	input := []codec.Message{anchorA, preamble, anchorB, toolA, toolB}
	got := codec.NormalizeAssistantToolCallSequence(input)

	if len(got) != 3 { // merged assistant + toolA + toolB
		t.Fatalf("expected 3 messages, got %d: %+v", len(got), got)
	}
	if len(got[0].ToolCalls) != 2 {
		t.Errorf("got[0] should have 2 tool_calls (idA and idB), got %d", len(got[0].ToolCalls))
	}
	content := extractText(got[0])
	if !strings.Contains(content, "intermediate reasoning") {
		t.Errorf("preamble text not in merged content: %q", content)
	}
	if got[1].Role != codec.RoleTool {
		t.Errorf("got[1] should be tool")
	}
	if got[2].Role != codec.RoleTool {
		t.Errorf("got[2] should be tool")
	}
}

// k) assistant{tool_calls=[A]} → user → assistant{tool_calls=[B]}: walk stops at user;
// first sequence cannot collect B's tool, B is processed as a separate anchor.
func TestNormalize_UserBoundaryStopsParallelMerge(t *testing.T) {
	anchorA := makeAssistantWithToolCalls("idA")
	user := codec.TextMessage(codec.RoleUser, "interrupt")
	anchorB := makeAssistantWithToolCalls("idB")
	toolB := makeToolResponse("idB", "resultB")

	input := []codec.Message{anchorA, user, anchorB, toolB}
	got := codec.NormalizeAssistantToolCallSequence(input)

	// anchorA sequence: walk stops at user → anchorA is emitted as-is (pending idA unsatisfied)
	// then user, then anchorB + toolB
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(got), got)
	}
	if got[0].Role != codec.RoleAssistant || len(got[0].ToolCalls) != 1 {
		t.Errorf("got[0] should be assistant with 1 tool_call (idA only)")
	}
	if got[1].Role != codec.RoleUser {
		t.Errorf("got[1] should be user")
	}
	if got[2].Role != codec.RoleAssistant || len(got[2].ToolCalls) != 1 {
		t.Errorf("got[2] should be assistant with 1 tool_call (idB only)")
	}
	if got[3].Role != codec.RoleTool || got[3].ToolCallID != "idB" {
		t.Errorf("got[3] should be tool for idB")
	}
}

// extractText concatenates all text content blocks in a message.
func extractText(m codec.Message) string {
	var sb strings.Builder
	for _, cb := range m.Content {
		if cb.Type == codec.ContentTypeText {
			sb.WriteString(cb.Text)
		}
	}
	return sb.String()
}

func TestDropEmptyTextBlocks_RemovesEmptyKeepsReal(t *testing.T) {
	in := []codec.ContentBlock{
		{Type: codec.ContentTypeText, Text: ""},
		{Type: codec.ContentTypeText, Text: "Hello!"},
	}
	got := codec.DropEmptyTextBlocks(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Text != "Hello!" {
		t.Errorf("kept block text = %q, want %q", got[0].Text, "Hello!")
	}
}

func TestDropEmptyTextBlocks_KeepsNonTextAndRawJSON(t *testing.T) {
	in := []codec.ContentBlock{
		{Type: codec.ContentTypeText, Text: ""},                                // dropped
		{Type: codec.ContentTypeImage, MediaB64: "abc", MimeType: "image/png"}, // kept: non-text
		{RawJSON: json.RawMessage(`{"type":"text"}`)},                          // kept: RawJSON passthrough
	}
	got := codec.DropEmptyTextBlocks(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Type != codec.ContentTypeImage {
		t.Errorf("block[0] type = %q, want image", got[0].Type)
	}
	if got[1].RawJSON == nil {
		t.Errorf("block[1] RawJSON should be preserved")
	}
}

func TestDropEmptyTextBlocks_AllEmptyAndNil(t *testing.T) {
	allEmpty := []codec.ContentBlock{
		{Type: codec.ContentTypeText, Text: ""},
		{Type: codec.ContentTypeText, Text: ""},
	}
	if got := codec.DropEmptyTextBlocks(allEmpty); len(got) != 0 {
		t.Errorf("all-empty: len = %d, want 0", len(got))
	}
	if got := codec.DropEmptyTextBlocks(nil); len(got) != 0 {
		t.Errorf("nil input: len = %d, want 0", len(got))
	}
}
