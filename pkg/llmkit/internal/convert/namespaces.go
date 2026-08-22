package convert

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

const (
	namespacedFunctionBindingsMetadataKey = "namespaced_function_bindings"
	chatFunctionNameMaxLength             = 64
)

var (
	ErrNamespacedFunctionMissingNamespace = errors.New("codec: namespaced function has empty namespace")
	ErrNamespacedFunctionMissingName      = errors.New("codec: namespaced function has empty name")
	ErrNamespacedFunctionNotFunction      = errors.New("codec: namespaced tool is not a function")
	ErrNamespacedFunctionNameCollision    = errors.New("codec: namespaced function Chat name collision")
)

type NamespacedFunctionBinding struct {
	ChatName  string `json:"chat_name"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type namespacedFunctionIdentity struct {
	namespace string
	name      string
}

func BuildChatFunctionName(namespace, name string) string {
	readable := namespace + "__" + name
	if namespace != "" && name != "" && !strings.Contains(namespace, "__") && !strings.Contains(name, "__") && isLegalChatFunctionName(readable) {
		return readable
	}
	return hashedChatFunctionName(namespace, name, "ns_", 56)
}

func isLegalChatFunctionName(name string) bool {
	if name == "" || len(name) > chatFunctionNameMaxLength {
		return false
	}
	for _, character := range name {
		if ('a' <= character && character <= 'z') || ('A' <= character && character <= 'Z') || ('0' <= character && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func hashedChatFunctionName(namespace, name, prefix string, hashLength int) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + name))
	return prefix + hex.EncodeToString(sum[:])[:hashLength]
}

func PrepareNamespacedFunctionsForChat(request *ir.Request) (*ir.Request, error) {
	if request == nil {
		return nil, nil
	}
	identities, occupied, err := collectNamespacedFunctionIdentities(request)
	if err != nil {
		return nil, err
	}
	if len(identities) == 0 {
		return request, nil
	}
	bindings, err := buildNamespacedFunctionBindings(identities, occupied)
	if err != nil {
		return nil, err
	}
	if err := recordNamespacedFunctionBindings(request, bindings); err != nil {
		return nil, err
	}
	prepared := cloneRequestForChatNamespacedFunctions(request)
	applyNamespacedFunctionBindingsToChatRequest(prepared, bindings)
	return prepared, nil
}

func collectNamespacedFunctionIdentities(request *ir.Request) ([]namespacedFunctionIdentity, map[string]struct{}, error) {
	identities := make(map[namespacedFunctionIdentity]struct{})
	occupied := make(map[string]struct{})
	add := func(namespace, name string) error {
		if namespace == "" {
			return ErrNamespacedFunctionMissingNamespace
		}
		if name == "" {
			return ErrNamespacedFunctionMissingName
		}
		identities[namespacedFunctionIdentity{namespace: namespace, name: name}] = struct{}{}
		return nil
	}

	for _, tool := range request.Tools {
		if tool.Namespace == "" {
			if isFunctionTool(tool.Type) && tool.Name != "" {
				occupied[tool.Name] = struct{}{}
			}
			continue
		}
		if !isFunctionTool(tool.Type) {
			return nil, nil, fmt.Errorf("%w: namespace %q tool %q has type %q", ErrNamespacedFunctionNotFunction, tool.Namespace, tool.Name, tool.Type)
		}
		if err := add(tool.Namespace, tool.Name); err != nil {
			return nil, nil, err
		}
	}
	for _, message := range request.Messages {
		for _, call := range message.ToolCalls {
			if call.Namespace == "" {
				if call.Name != "" {
					occupied[call.Name] = struct{}{}
				}
				continue
			}
			if err := add(call.Namespace, call.Name); err != nil {
				return nil, nil, err
			}
		}
	}
	if choice := request.ToolChoice; choice != nil {
		if choice.Namespace == "" {
			if choice.Name != "" {
				occupied[choice.Name] = struct{}{}
			}
		} else {
			if choice.Type != "function" {
				return nil, nil, fmt.Errorf("%w: named tool_choice type %q", ErrNamespacedFunctionNotFunction, choice.Type)
			}
			if err := add(choice.Namespace, choice.Name); err != nil {
				return nil, nil, err
			}
		}
	}
	ordered := make([]namespacedFunctionIdentity, 0, len(identities))
	for identity := range identities {
		ordered = append(ordered, identity)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].namespace == ordered[j].namespace {
			return ordered[i].name < ordered[j].name
		}
		return ordered[i].namespace < ordered[j].namespace
	})
	return ordered, occupied, nil
}

func isFunctionTool(toolType string) bool {
	return toolType == "" || toolType == "function"
}

func buildNamespacedFunctionBindings(identities []namespacedFunctionIdentity, occupied map[string]struct{}) ([]NamespacedFunctionBinding, error) {
	counts := make(map[string]int, len(identities))
	for _, identity := range identities {
		counts[BuildChatFunctionName(identity.namespace, identity.name)]++
	}
	used := make(map[string]struct{}, len(occupied)+len(identities))
	for name := range occupied {
		used[name] = struct{}{}
	}
	bindings := make([]NamespacedFunctionBinding, 0, len(identities))
	for _, identity := range identities {
		candidate := BuildChatFunctionName(identity.namespace, identity.name)
		if counts[candidate] > 1 || nameIsUsed(used, candidate) {
			candidate = hashedChatFunctionName(identity.namespace, identity.name, "ns_", 56)
		}
		if nameIsUsed(used, candidate) {
			candidate = hashedChatFunctionName(identity.namespace, identity.name, "nsf_", 59)
		}
		if nameIsUsed(used, candidate) {
			return nil, fmt.Errorf("%w: namespace %q function %q", ErrNamespacedFunctionNameCollision, identity.namespace, identity.name)
		}
		used[candidate] = struct{}{}
		bindings = append(bindings, NamespacedFunctionBinding{ChatName: candidate, Namespace: identity.namespace, Name: identity.name})
	}
	return bindings, nil
}

func nameIsUsed(names map[string]struct{}, name string) bool {
	_, exists := names[name]
	return exists
}

func recordNamespacedFunctionBindings(request *ir.Request, bindings []NamespacedFunctionBinding) error {
	byName := make(map[string]NamespacedFunctionBinding, len(bindings))
	for _, binding := range bindings {
		if binding.ChatName == "" || binding.Namespace == "" || binding.Name == "" {
			return fmt.Errorf("%w: incomplete binding", ErrNamespacedFunctionNameCollision)
		}
		if previous, exists := byName[binding.ChatName]; exists && previous != binding {
			return fmt.Errorf("%w: %q", ErrNamespacedFunctionNameCollision, binding.ChatName)
		}
		byName[binding.ChatName] = binding
	}
	if len(byName) == 0 {
		return nil
	}
	if request.Metadata == nil {
		request.Metadata = make(map[string]any)
	}
	request.Metadata[namespacedFunctionBindingsMetadataKey] = byName
	return nil
}

func NamespacedFunctionBindings(request *ir.Request) map[string]NamespacedFunctionBinding {
	if request == nil || request.Metadata == nil {
		return nil
	}
	stored, ok := request.Metadata[namespacedFunctionBindingsMetadataKey].(map[string]NamespacedFunctionBinding)
	if !ok || len(stored) == 0 {
		return nil
	}
	out := make(map[string]NamespacedFunctionBinding, len(stored))
	for name, binding := range stored {
		if name != binding.ChatName || binding.Namespace == "" || binding.Name == "" {
			return nil
		}
		out[name] = binding
	}
	return out
}

func cloneRequestForChatNamespacedFunctions(request *ir.Request) *ir.Request {
	prepared := *request
	prepared.Tools = append([]ir.Tool(nil), request.Tools...)
	prepared.Messages = append([]ir.Message(nil), request.Messages...)
	for index := range prepared.Messages {
		prepared.Messages[index].ToolCalls = append([]ir.ToolCall(nil), request.Messages[index].ToolCalls...)
	}
	if request.ToolChoice != nil {
		choice := *request.ToolChoice
		prepared.ToolChoice = &choice
	}
	return &prepared
}

func applyNamespacedFunctionBindingsToChatRequest(request *ir.Request, bindings []NamespacedFunctionBinding) {
	byIdentity := make(map[namespacedFunctionIdentity]NamespacedFunctionBinding, len(bindings))
	for _, binding := range bindings {
		byIdentity[namespacedFunctionIdentity{namespace: binding.Namespace, name: binding.Name}] = binding
	}
	for index := range request.Tools {
		tool := &request.Tools[index]
		if binding, ok := byIdentity[namespacedFunctionIdentity{namespace: tool.Namespace, name: tool.Name}]; ok {
			tool.Name = binding.ChatName
		}
	}
	for messageIndex := range request.Messages {
		for callIndex := range request.Messages[messageIndex].ToolCalls {
			call := &request.Messages[messageIndex].ToolCalls[callIndex]
			if binding, ok := byIdentity[namespacedFunctionIdentity{namespace: call.Namespace, name: call.Name}]; ok {
				call.Name = binding.ChatName
			}
		}
	}
	if request.ToolChoice != nil {
		choice := request.ToolChoice
		if binding, ok := byIdentity[namespacedFunctionIdentity{namespace: choice.Namespace, name: choice.Name}]; ok {
			choice.Name = binding.ChatName
		}
	}
}

type namespacedFunctionCallState struct {
	binding NamespacedFunctionBinding
}

func AdaptNamespacedFunctionEvents(ctx context.Context, events <-chan ir.Event, bindings map[string]NamespacedFunctionBinding) <-chan ir.Event {
	if len(bindings) == 0 || events == nil {
		return events
	}
	out := make(chan ir.Event, 64)
	go func() {
		closeAndDrain := func() {
			close(out)
			for range events {
			}
		}
		calls := make(map[string]namespacedFunctionCallState)
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
				adapted := adaptNamespacedFunctionEvent(event, bindings, calls)
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

func adaptNamespacedFunctionEvent(event ir.Event, bindings map[string]NamespacedFunctionBinding, calls map[string]namespacedFunctionCallState) ir.Event {
	switch event.Type {
	case ir.EventToolCallDelta:
		if event.Delta == nil || event.Delta.ToolCall == nil {
			return event
		}
		binding, ok := bindings[event.Delta.ToolCall.Name]
		if !ok {
			return event
		}
		adapted := event
		delta := *event.Delta
		call := *event.Delta.ToolCall
		call.Name, call.Namespace = binding.Name, binding.Namespace
		delta.ToolCall = &call
		adapted.Delta = &delta
		return adapted
	case ir.EventToolCallStart:
		if event.ToolCall == nil {
			return event
		}
		binding, ok := bindings[event.ToolCall.Name]
		if !ok {
			return event
		}
		calls[event.ToolCall.CallID] = namespacedFunctionCallState{binding: binding}
		adapted := event
		call := *event.ToolCall
		call.Name, call.Namespace = binding.Name, binding.Namespace
		adapted.ToolCall = &call
		return adapted
	case ir.EventToolCallEnd:
		if event.ToolCall == nil {
			return event
		}
		state, ok := calls[event.ToolCall.CallID]
		if !ok {
			return event
		}
		delete(calls, event.ToolCall.CallID)
		adapted := event
		call := *event.ToolCall
		call.Name, call.Namespace = state.binding.Name, state.binding.Namespace
		adapted.ToolCall = &call
		return adapted
	default:
		return event
	}
}
