package convert

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestPrepareNamespacedFunctionsForChat(t *testing.T) {
	t.Run("flattens request and records request-local binding", func(t *testing.T) {
		request := &ir.Request{
			Tools:      []ir.Tool{{Type: "function", Namespace: "multi", Name: "spawn"}},
			Messages:   []ir.Message{{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: "c", Namespace: "multi", Name: "spawn"}}}},
			ToolChoice: &ir.ToolChoice{Type: "function", Namespace: "multi", Name: "spawn"},
		}
		prepared, err := PrepareNamespacedFunctionsForChat(request)
		if err != nil {
			t.Fatal(err)
		}
		if prepared == request {
			t.Fatal("namespaced request must use a conversion view")
		}
		if prepared.Tools[0].Name != "multi__spawn" || prepared.Messages[0].ToolCalls[0].Name != "multi__spawn" || prepared.ToolChoice.Name != "multi__spawn" {
			t.Fatalf("prepared request = %#v", prepared)
		}
		if request.Tools[0].Name != "spawn" || request.Messages[0].ToolCalls[0].Name != "spawn" {
			t.Fatal("source request was mutated")
		}
		binding := NamespacedFunctionBindings(request)["multi__spawn"]
		if binding.Namespace != "multi" || binding.Name != "spawn" {
			t.Fatalf("binding = %#v", binding)
		}
	})
	t.Run("rejects non-function namespace child", func(t *testing.T) {
		_, err := PrepareNamespacedFunctionsForChat(&ir.Request{Tools: []ir.Tool{{Type: "custom", Namespace: "multi", Name: "spawn"}}})
		if !errors.Is(err, ErrNamespacedFunctionNotFunction) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("nil input", func(t *testing.T) {
		prepared, err := PrepareNamespacedFunctionsForChat(nil)
		if err != nil || prepared != nil {
			t.Fatalf("prepared=%#v error=%v", prepared, err)
		}
	})
}

func TestBuildChatFunctionNameReadableHashAndCollisions(t *testing.T) {
	t.Run("uses readable name when legal", func(t *testing.T) {
		if got := BuildChatFunctionName("multi", "spawn"); got != "multi__spawn" {
			t.Fatalf("name = %q", got)
		}
	})

	t.Run("uses stable legal hash when unreadable", func(t *testing.T) {
		first := BuildChatFunctionName("namespace with spaces", "spawn")
		second := BuildChatFunctionName("namespace with spaces", "spawn")
		if first != second || len(first) > chatFunctionNameMaxLength || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(first) || !strings.HasPrefix(first, "ns_") {
			t.Fatalf("hashed names = %q / %q", first, second)
		}
	})

	t.Run("uses secondary hash when primary is occupied", func(t *testing.T) {
		namespace, name := "namespace with spaces", "spawn"
		primary := BuildChatFunctionName(namespace, name)
		request := &ir.Request{Tools: []ir.Tool{{Type: "function", Name: primary}, {Type: "function", Namespace: namespace, Name: name}}}
		prepared, err := PrepareNamespacedFunctionsForChat(request)
		if err != nil {
			t.Fatal(err)
		}
		binding := onlyNamespaceBinding(t, request)
		want := namespaceHashForTest(namespace, name, "nsf_", 59)
		if binding.ChatName != want || prepared.Tools[1].Name != want {
			t.Fatalf("binding = %#v, prepared = %#v, want %q", binding, prepared.Tools, want)
		}
	})

	t.Run("rejects when primary and secondary hashes are occupied", func(t *testing.T) {
		namespace, name := "namespace with spaces", "spawn"
		request := &ir.Request{Tools: []ir.Tool{
			{Type: "function", Name: namespaceHashForTest(namespace, name, "ns_", 56)},
			{Type: "function", Name: namespaceHashForTest(namespace, name, "nsf_", 59)},
			{Type: "function", Namespace: namespace, Name: name},
		}}
		_, err := PrepareNamespacedFunctionsForChat(request)
		if !errors.Is(err, ErrNamespacedFunctionNameCollision) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestAdaptNamespacedFunctionEventsRestoresNamespace(t *testing.T) {
	bindings := map[string]NamespacedFunctionBinding{"multi__spawn": {ChatName: "multi__spawn", Namespace: "multi", Name: "spawn"}}
	events := eventChannel(
		ir.Event{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c", Name: "multi__spawn"}},
		ir.Event{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "c", Arguments: "{}"}},
		ir.Event{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
	)
	got := collectEvents(AdaptNamespacedFunctionEvents(context.Background(), events, bindings))
	if len(got) != 3 || got[0].ToolCall.Name != "spawn" || got[0].ToolCall.Namespace != "multi" || got[2].ToolCall.Name != "spawn" || got[2].ToolCall.Namespace != "multi" {
		t.Fatalf("events = %#v", got)
	}
}

func TestAdaptNamespacedFunctionEventsCompatibility(t *testing.T) {
	bindings := namespaceEventBindings()

	t.Run("restores non-stream delta", func(t *testing.T) {
		wantArguments := `{"task":"inspect"}`
		got := collectEvents(AdaptNamespacedFunctionEvents(context.Background(), eventChannel(ir.Event{
			Type:  ir.EventToolCallDelta,
			Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{ID: "spawn-call", Name: "multi__spawn", Arguments: wantArguments}},
		}), bindings))
		call := got[0].Delta.ToolCall
		if call.ID != "spawn-call" || call.Name != "spawn" || call.Namespace != "multi" || call.Arguments != wantArguments {
			t.Fatalf("call = %#v", call)
		}
	})

	t.Run("restores parallel calls ending out of order", func(t *testing.T) {
		got := collectEvents(AdaptNamespacedFunctionEvents(context.Background(), eventChannel(
			ir.Event{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "wait-call", Index: 7, Name: "multi__wait"}},
			ir.Event{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "spawn-call", Index: 2, Name: "multi__spawn"}},
			ir.Event{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "spawn-call", Arguments: `{"task":"a"}`}},
			ir.Event{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "wait-call", Arguments: `{"id":"b"}`}},
			ir.Event{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "wait-call", Index: 7, Arguments: `{"id":"b"}`}},
			ir.Event{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "spawn-call", Index: 2, Arguments: `{"task":"a"}`}},
		), bindings))
		if len(got) != 6 || got[0].ToolCall.Name != "wait" || got[0].ToolCall.Namespace != "multi" || got[1].ToolCall.Name != "spawn" || got[4].ToolCall.Name != "wait" || got[5].ToolCall.Name != "spawn" {
			t.Fatalf("events = %#v", got)
		}
	})

	t.Run("ordinary function passes through", func(t *testing.T) {
		want := ir.Event{Type: ir.EventToolCallDelta, Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{ID: "exec-call", Name: "exec", Arguments: `{}`}}}
		got := collectEvents(AdaptNamespacedFunctionEvents(context.Background(), eventChannel(want), bindings))
		if !reflect.DeepEqual(got, []ir.Event{want}) {
			t.Fatalf("events = %#v", got)
		}
	})

	t.Run("cancellation closes output and drains source", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan ir.Event)
		out := AdaptNamespacedFunctionEvents(ctx, events, bindings)
		cancel()
		select {
		case _, ok := <-out:
			if ok {
				t.Fatal("output remained open")
			}
		case <-time.After(time.Second):
			t.Fatal("output did not close promptly")
		}
		drained := make(chan struct{})
		go func() {
			events <- ir.Event{Type: ir.EventDone}
			close(events)
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(time.Second):
			t.Fatal("source was not drained")
		}
	})
}

func namespaceEventBindings() map[string]NamespacedFunctionBinding {
	return map[string]NamespacedFunctionBinding{
		"multi__spawn": {ChatName: "multi__spawn", Namespace: "multi", Name: "spawn"},
		"multi__wait":  {ChatName: "multi__wait", Namespace: "multi", Name: "wait"},
	}
}

func onlyNamespaceBinding(t *testing.T, request *ir.Request) NamespacedFunctionBinding {
	t.Helper()
	bindings := NamespacedFunctionBindings(request)
	if len(bindings) != 1 {
		t.Fatalf("bindings = %#v", bindings)
	}
	for _, binding := range bindings {
		return binding
	}
	return NamespacedFunctionBinding{}
}

func namespaceHashForTest(namespace, name, prefix string, size int) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + name))
	return prefix + hex.EncodeToString(sum[:])[:size]
}
