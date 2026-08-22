package convert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type toolCallState int

const (
	toolCallStarted toolCallState = iota
	toolCallEnded
	customToolCallEvent = "response.output_item.done"
)

func AssertStreamingToolCallInvariant(events []ir.Event) error {
	states := make(map[string]toolCallState)
	for index, event := range events {
		switch event.Type {
		case ir.EventToolCallStart:
			if event.ToolCall == nil || event.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: Start event missing CallID", index)
			}
			if _, exists := states[event.ToolCall.CallID]; exists {
				return fmt.Errorf("event %d: duplicate Start for call_id %s", index, event.ToolCall.CallID)
			}
			states[event.ToolCall.CallID] = toolCallStarted
		case ir.EventToolCallArgumentsDelta:
			if event.ToolCall == nil || event.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: ArgumentsDelta event missing CallID", index)
			}
			state, exists := states[event.ToolCall.CallID]
			if !exists {
				return fmt.Errorf("event %d: ArgumentsDelta without Start for call_id %s", index, event.ToolCall.CallID)
			}
			if state == toolCallEnded {
				return fmt.Errorf("event %d: ArgumentsDelta after End for call_id %s", index, event.ToolCall.CallID)
			}
		case ir.EventToolCallEnd:
			if event.ToolCall == nil || event.ToolCall.CallID == "" {
				return fmt.Errorf("event %d: End event missing CallID", index)
			}
			state, exists := states[event.ToolCall.CallID]
			if !exists {
				return fmt.Errorf("event %d: End without Start for call_id %s", index, event.ToolCall.CallID)
			}
			if state == toolCallEnded {
				return fmt.Errorf("event %d: duplicate End for call_id %s", index, event.ToolCall.CallID)
			}
			states[event.ToolCall.CallID] = toolCallEnded
		}
	}
	for callID, state := range states {
		if state == toolCallStarted {
			return fmt.Errorf("call_id %s: Start without End (unterminated stream)", callID)
		}
	}
	return nil
}

type functionFallbackCall struct {
	name      string
	arguments strings.Builder
}

func AdaptFunctionFallbackEvents(ctx context.Context, events <-chan ir.Event, tools map[string]FunctionFallbackTool) <-chan ir.Event {
	if len(tools) == 0 || events == nil {
		return events
	}
	out := make(chan ir.Event, 64)
	go func() {
		closeAndDrain := func() {
			close(out)
			for range events {
			}
		}
		calls := make(map[string]*functionFallbackCall)
		for {
			select {
			case <-ctx.Done():
				closeAndDrain()
				return
			case event, ok := <-events:
				if !ok {
					close(out)
					return
				}
				adapted, emit := adaptFunctionFallbackEvent(event, tools, calls)
				if !emit {
					continue
				}
				select {
				case out <- adapted:
				case <-ctx.Done():
					closeAndDrain()
					return
				}
			}
		}
	}()
	return out
}

func adaptFunctionFallbackEvent(event ir.Event, tools map[string]FunctionFallbackTool, calls map[string]*functionFallbackCall) (ir.Event, bool) {
	switch event.Type {
	case ir.EventToolCallDelta:
		if event.Delta == nil || event.Delta.ToolCall == nil {
			return event, true
		}
		call := event.Delta.ToolCall
		tool, ok := tools[call.Name]
		if !ok {
			return event, true
		}
		return customToolCallOutput(call.ID, call.Name, unwrapFunctionFallbackArguments(call.Arguments, tool.ArgumentName)), true
	case ir.EventToolCallStart:
		if event.ToolCall == nil {
			return event, true
		}
		if _, ok := tools[event.ToolCall.Name]; !ok {
			return event, true
		}
		calls[event.ToolCall.CallID] = &functionFallbackCall{name: event.ToolCall.Name}
		return ir.Event{}, false
	case ir.EventToolCallArgumentsDelta:
		if event.ToolCall == nil {
			return event, true
		}
		call, ok := calls[event.ToolCall.CallID]
		if !ok {
			return event, true
		}
		call.arguments.WriteString(event.ToolCall.Arguments)
		return ir.Event{}, false
	case ir.EventToolCallEnd:
		if event.ToolCall == nil {
			return event, true
		}
		call, ok := calls[event.ToolCall.CallID]
		if !ok {
			return event, true
		}
		delete(calls, event.ToolCall.CallID)
		arguments := event.ToolCall.Arguments
		if arguments == "" {
			arguments = call.arguments.String()
		}
		tool := tools[call.name]
		return customToolCallOutput(event.ToolCall.CallID, call.name, unwrapFunctionFallbackArguments(arguments, tool.ArgumentName)), true
	default:
		return event, true
	}
}

func unwrapFunctionFallbackArguments(arguments, argumentName string) string {
	var values map[string]any
	if err := json.Unmarshal([]byte(arguments), &values); err == nil {
		if input, ok := values[argumentName].(string); ok {
			return input
		}
	}
	return arguments
}

func customToolCallOutput(callID, name, input string) ir.Event {
	data, _ := json.Marshal(map[string]any{"type": customToolCallEvent, "item": map[string]any{"type": "custom_tool_call", "call_id": callID, "name": name, "input": input}})
	return ir.Event{Type: ir.EventRawPassthrough, RawPassthrough: &ir.RawSSEEvent{EventName: customToolCallEvent, Data: string(data)}}
}
