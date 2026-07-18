package codec

import (
	"context"
	"encoding/json"
	"strings"
)

const customToolCallEvent = "response.output_item.done"

type functionFallbackCall struct {
	name      string
	arguments strings.Builder
}

// AdaptFunctionFallbackEvents restores custom tool calls that a limited
// upstream emitted as function calls. The returned event is raw Responses wire
// data so the inbound Responses codec can preserve custom-tool semantics.
func AdaptFunctionFallbackEvents(
	ctx context.Context,
	events <-chan Event,
	tools map[string]FunctionFallbackTool,
) <-chan Event {
	if len(tools) == 0 {
		return events
	}
	out := make(chan Event, 64)
	go func() {
		closeOutputAndDrain := func() {
			// Closing out lets Relay return and close the response body, which in
			// turn makes the decoder close events while this goroutine drains it.
			close(out)
			for range events {
			}
		}
		calls := make(map[string]*functionFallbackCall)
		for {
			select {
			case <-ctx.Done():
				closeOutputAndDrain()
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
					closeOutputAndDrain()
					return
				}
			}
		}
	}()
	return out
}

func adaptFunctionFallbackEvent(
	event Event,
	tools map[string]FunctionFallbackTool,
	calls map[string]*functionFallbackCall,
) (Event, bool) {
	switch event.Type {
	case EventToolCallDelta:
		if event.Delta == nil || event.Delta.ToolCall == nil {
			return event, true
		}
		call := event.Delta.ToolCall
		tool, ok := tools[call.Name]
		if !ok {
			return event, true
		}
		return customToolCallOutput(call.ID, call.Name, unwrapFunctionFallbackArguments(call.Arguments, tool.ArgumentName)), true
	case EventToolCallStart:
		if event.ToolCall == nil {
			return event, true
		}
		if _, ok := tools[event.ToolCall.Name]; !ok {
			return event, true
		}
		calls[event.ToolCall.CallID] = &functionFallbackCall{name: event.ToolCall.Name}
		return Event{}, false
	case EventToolCallArgumentsDelta:
		if event.ToolCall == nil {
			return event, true
		}
		call, ok := calls[event.ToolCall.CallID]
		if !ok {
			return event, true
		}
		call.arguments.WriteString(event.ToolCall.Arguments)
		return Event{}, false
	case EventToolCallEnd:
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
		return customToolCallOutput(
			event.ToolCall.CallID,
			call.name,
			unwrapFunctionFallbackArguments(arguments, tool.ArgumentName),
		), true
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

func customToolCallOutput(callID, name, input string) Event {
	data, _ := json.Marshal(map[string]any{
		"type": customToolCallEvent,
		"item": map[string]any{
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    name,
			"input":   input,
		},
	})
	return Event{
		Type: EventRawPassthrough,
		RawPassthrough: &RawSSEEvent{
			EventName: customToolCallEvent,
			Data:      string(data),
		},
	}
}
