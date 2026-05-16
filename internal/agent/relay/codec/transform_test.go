package codec

import (
	"testing"
)

type fakeTransformer struct {
	name        string
	appliesTo   func(Protocol) bool
	transformFn func(*Request, *ChannelConfig)
}

func (f fakeTransformer) Name() string { return f.name }
func (f fakeTransformer) AppliesTo(p Protocol) bool {
	if f.appliesTo == nil {
		return true
	}
	return f.appliesTo(p)
}
func (f fakeTransformer) Transform(r *Request, c *ChannelConfig) {
	if f.transformFn != nil {
		f.transformFn(r, c)
	}
}

func TestRegisterIRTransformer_DuplicatePanics(t *testing.T) {
	resetIRTransformers()
	defer resetIRTransformers()

	RegisterIRTransformer(fakeTransformer{name: "dup", appliesTo: func(Protocol) bool { return true }})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration, got none")
		}
	}()
	RegisterIRTransformer(fakeTransformer{name: "dup", appliesTo: func(Protocol) bool { return true }})
}

func TestApplyIRTransformers_ProtocolFilter(t *testing.T) {
	resetIRTransformers()
	defer resetIRTransformers()

	called := false
	RegisterIRTransformer(fakeTransformer{
		name:        "openai-only",
		appliesTo:   func(p Protocol) bool { return p == ProtocolOpenAIChat },
		transformFn: func(*Request, *ChannelConfig) { called = true },
	})

	ApplyIRTransformers(ProtocolClaude, &Request{}, &ChannelConfig{})
	if called {
		t.Fatal("transformer should NOT be called for ProtocolClaude")
	}

	ApplyIRTransformers(ProtocolOpenAIChat, &Request{}, &ChannelConfig{})
	if !called {
		t.Fatal("transformer should be called for ProtocolOpenAIChat")
	}
}

func TestApplyIRTransformers_OrderPreserved(t *testing.T) {
	resetIRTransformers()
	defer resetIRTransformers()

	var trace []string
	RegisterIRTransformer(fakeTransformer{
		name:        "first",
		appliesTo:   func(Protocol) bool { return true },
		transformFn: func(*Request, *ChannelConfig) { trace = append(trace, "first") },
	})
	RegisterIRTransformer(fakeTransformer{
		name:        "second",
		appliesTo:   func(Protocol) bool { return true },
		transformFn: func(*Request, *ChannelConfig) { trace = append(trace, "second") },
	})
	RegisterIRTransformer(fakeTransformer{
		name:        "third",
		appliesTo:   func(Protocol) bool { return true },
		transformFn: func(*Request, *ChannelConfig) { trace = append(trace, "third") },
	})

	ApplyIRTransformers(ProtocolOpenAIChat, &Request{}, &ChannelConfig{})

	want := []string{"first", "second", "third"}
	if len(trace) != len(want) {
		t.Fatalf("trace length = %d, want %d", len(trace), len(want))
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("trace[%d] = %q, want %q", i, trace[i], want[i])
		}
	}
}

func TestApplyIRTransformers_Idempotent(t *testing.T) {
	resetIRTransformers()
	defer resetIRTransformers()

	RegisterIRTransformer(fakeTransformer{
		name:      "noop",
		appliesTo: func(Protocol) bool { return true },
		transformFn: func(r *Request, _ *ChannelConfig) {
			// in-place 操作但是是幂等的：保证 messages 长度不变
		},
	})

	req := &Request{Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hi"}}}}}
	cfg := &ChannelConfig{}

	ApplyIRTransformers(ProtocolOpenAIChat, req, cfg)
	first := len(req.Messages)
	ApplyIRTransformers(ProtocolOpenAIChat, req, cfg)
	second := len(req.Messages)

	if first != second {
		t.Fatalf("non-idempotent: first=%d second=%d", first, second)
	}
}
