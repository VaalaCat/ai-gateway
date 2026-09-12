package convert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

const (
	ReasoningProtocolClaude     = "claude"
	ReasoningProtocolResponses  = "responses"
	ReasoningProtocolOpenAIChat = "openai_chat"
	reasoningEnvelopePrefix     = "llmkit:v1:"
)

type reasoningEnvelope struct {
	Protocol string          `json:"protocol"`
	Data     json.RawMessage `json:"data"`
}

// EncodeReasoningBlock projects protocol-neutral reasoning into a complete
// Claude thinking block, Responses reasoning item, or OpenAI Chat
// reasoning_content representation. Unknown source fields are preserved, while
// structured fields remain authoritative.
func EncodeReasoningBlock(reasoning *ir.ReasoningContent, targetProtocol string) json.RawMessage {
	if reasoning == nil {
		return nil
	}

	rawProtocol := reasoningProtocol(reasoning.RawJSON)
	envelope, envelopeOK, envelopeInvalid := decodeReasoningEnvelope(reasoning.Encrypted)

	sourceProtocol := rawProtocol
	sourceRaw := append(json.RawMessage(nil), reasoning.RawJSON...)
	if envelopeOK && envelope.Protocol == targetProtocol {
		sourceProtocol = envelope.Protocol
		sourceRaw = envelope.Data
	} else if sourceProtocol == "" && envelopeOK {
		sourceProtocol = envelope.Protocol
		sourceRaw = envelope.Data
	}

	if sourceProtocol == targetProtocol {
		nativeEncrypted := reasoning.Encrypted
		if envelopeOK && envelope.Protocol == targetProtocol {
			nativeEncrypted = reasoningNativeEncrypted(envelope.Data, envelope.Protocol)
		} else if envelopeInvalid {
			nativeEncrypted = ""
		}
		return overlayReasoning(sourceRaw, targetProtocol, reasoning, nativeEncrypted)
	}

	if sourceProtocol != "" {
		sourceEncrypted := reasoning.Encrypted
		if envelopeOK {
			sourceEncrypted = reasoningNativeEncrypted(envelope.Data, envelope.Protocol)
		} else if envelopeInvalid {
			sourceEncrypted = ""
		}
		sourceRaw = overlayReasoning(sourceRaw, sourceProtocol, reasoning, sourceEncrypted)
		targetEncrypted := encodeReasoningEnvelope(sourceProtocol, sourceRaw)
		return overlayReasoning(nil, targetProtocol, reasoning, targetEncrypted)
	}

	nativeEncrypted := reasoning.Encrypted
	if envelopeInvalid {
		nativeEncrypted = ""
	}
	return overlayReasoning(nil, targetProtocol, reasoning, nativeEncrypted)
}

func reasoningProtocol(raw json.RawMessage) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	var typ string
	_ = json.Unmarshal(object["type"], &typ)
	switch typ {
	case "thinking":
		return ReasoningProtocolClaude
	case "reasoning":
		return ReasoningProtocolResponses
	}
	if value, exists := object["reasoning_content"]; exists {
		var text string
		if json.Unmarshal(value, &text) == nil {
			return ReasoningProtocolOpenAIChat
		}
	}
	return ""
}

func encodeReasoningEnvelope(protocol string, raw json.RawMessage) string {
	data, _ := json.Marshal(reasoningEnvelope{Protocol: protocol, Data: raw})
	return reasoningEnvelopePrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeReasoningEnvelope(value string) (reasoningEnvelope, bool, bool) {
	if !strings.HasPrefix(value, "llmkit:") {
		return reasoningEnvelope{}, false, false
	}
	if !strings.HasPrefix(value, reasoningEnvelopePrefix) {
		return reasoningEnvelope{}, false, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, reasoningEnvelopePrefix))
	if err != nil {
		return reasoningEnvelope{}, false, true
	}
	var envelope reasoningEnvelope
	if json.Unmarshal(decoded, &envelope) != nil || (envelope.Protocol != ReasoningProtocolClaude && envelope.Protocol != ReasoningProtocolResponses && envelope.Protocol != ReasoningProtocolOpenAIChat) || reasoningProtocol(envelope.Data) != envelope.Protocol {
		return reasoningEnvelope{}, false, true
	}
	return envelope, true, false
}

func reasoningNativeEncrypted(raw json.RawMessage, protocol string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	field := ""
	switch protocol {
	case ReasoningProtocolClaude:
		field = "signature"
	case ReasoningProtocolResponses:
		field = "encrypted_content"
	default:
		return ""
	}
	var encrypted string
	_ = json.Unmarshal(object[field], &encrypted)
	return encrypted
}

func overlayReasoning(raw json.RawMessage, protocol string, reasoning *ir.ReasoningContent, encrypted string) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		object = make(map[string]json.RawMessage)
	}
	switch protocol {
	case ReasoningProtocolClaude:
		object["type"] = json.RawMessage(`"thinking"`)
		readable := reasoning.Content
		if len(readable) == 0 {
			readable = reasoning.Summary
		}
		object["thinking"], _ = json.Marshal(strings.Join(readable, ""))
		setReasoningString(object, "signature", encrypted)
	case ReasoningProtocolResponses:
		object["type"] = json.RawMessage(`"reasoning"`)
		object["summary"] = overlayReasoningParts(object["summary"], "summary_text", reasoning.Summary)
		object["content"] = overlayReasoningParts(object["content"], "reasoning_text", reasoning.Content)
		setReasoningString(object, "encrypted_content", encrypted)
	case ReasoningProtocolOpenAIChat:
		readable := reasoning.Content
		if len(readable) == 0 {
			readable = reasoning.Summary
		}
		// behavior change: absent readable parts preserve restored Chat text;
		// an explicitly present empty part still overrides it.
		if len(readable) > 0 || object["reasoning_content"] == nil {
			object["reasoning_content"], _ = json.Marshal(strings.Join(readable, ""))
		}
	}
	encoded, _ := json.Marshal(object)
	return encoded
}

func setReasoningString(object map[string]json.RawMessage, field, value string) {
	if value == "" {
		delete(object, field)
		return
	}
	object[field], _ = json.Marshal(value)
}

func overlayReasoningParts(raw json.RawMessage, partType string, texts []string) json.RawMessage {
	if texts == nil && len(raw) > 0 {
		return append(json.RawMessage(nil), raw...)
	}
	var parts []json.RawMessage
	_ = json.Unmarshal(raw, &parts)
	result := make([]json.RawMessage, 0, len(parts)+len(texts))
	textIndex := 0
	for _, part := range parts {
		var value struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(part, &value) != nil || value.Type != partType {
			result = append(result, part)
			continue
		}
		if textIndex < len(texts) {
			result = append(result, reasoningTextPart(part, partType, texts[textIndex]))
			textIndex++
		}
	}
	for ; textIndex < len(texts); textIndex++ {
		result = append(result, reasoningTextPart(nil, partType, texts[textIndex]))
	}
	encoded, _ := json.Marshal(result)
	return encoded
}

func reasoningTextPart(raw json.RawMessage, partType, text string) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		object = make(map[string]json.RawMessage)
	}
	object["type"], _ = json.Marshal(partType)
	object["text"], _ = json.Marshal(text)
	encoded, _ := json.Marshal(object)
	return encoded
}

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
