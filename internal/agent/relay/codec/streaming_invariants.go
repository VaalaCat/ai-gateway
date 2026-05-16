package codec

import "fmt"

type toolCallState int

const (
	toolCallStateStarted toolCallState = iota
	toolCallStateEnded
)

// AssertStreamingToolCallInvariant verifies that for each call_id in the event
// stream the order is: exactly one Start, zero or more ArgumentsDelta, exactly one End.
// Parallel tool_calls (multiple call_ids interleaved) are allowed.
func AssertStreamingToolCallInvariant(events []Event) error {
	state := map[string]toolCallState{}
	for i, ev := range events {
		switch ev.Type {
		case EventToolCallStart:
			if ev.ToolCall == nil || ev.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: Start event missing CallID", i)
			}
			if _, ok := state[ev.ToolCall.CallID]; ok {
				return fmt.Errorf("event %d: duplicate Start for call_id %s", i, ev.ToolCall.CallID)
			}
			state[ev.ToolCall.CallID] = toolCallStateStarted
		case EventToolCallArgumentsDelta:
			if ev.ToolCall == nil || ev.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: ArgumentsDelta event missing CallID", i)
			}
			s, ok := state[ev.ToolCall.CallID]
			if !ok {
				return fmt.Errorf("event %d: ArgumentsDelta without Start for call_id %s", i, ev.ToolCall.CallID)
			}
			if s == toolCallStateEnded {
				return fmt.Errorf("event %d: ArgumentsDelta after End for call_id %s", i, ev.ToolCall.CallID)
			}
		case EventToolCallEnd:
			if ev.ToolCall == nil || ev.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: End event missing CallID", i)
			}
			s, ok := state[ev.ToolCall.CallID]
			if !ok {
				return fmt.Errorf("event %d: End without Start for call_id %s", i, ev.ToolCall.CallID)
			}
			if s == toolCallStateEnded {
				return fmt.Errorf("event %d: duplicate End for call_id %s", i, ev.ToolCall.CallID)
			}
			state[ev.ToolCall.CallID] = toolCallStateEnded
		}
	}
	for callID, s := range state {
		if s == toolCallStateStarted {
			return fmt.Errorf("call_id %s: Start without End (unterminated stream)", callID)
		}
	}
	return nil
}
