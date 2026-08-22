package convert

import (
	"errors"
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestResolveToolFallbackPolicies(t *testing.T) {
	raw := map[string]any{"type": "web_search"}
	emit := TargetEmitFuncs{Function: func(tool ir.Tool) any {
		return map[string]any{"type": tool.Type, "name": tool.Name, "schema": tool.InputSchema}
	}}
	tests := []struct {
		name    string
		tool    ir.Tool
		target  ir.Protocol
		policy  BuiltinToolFallbackPolicy
		want    any
		reason  string
		wantErr error
	}{
		{name: "drop", tool: ir.Tool{Type: "web_search", RawConfig: raw}, target: ir.ProtocolOpenAIChat, policy: BuiltinToolFallbackDrop, reason: DroppedToolReasonCrossProtocolIncompatible},
		{name: "error", tool: ir.Tool{Type: "web_search", RawConfig: raw}, target: ir.ProtocolOpenAIChat, policy: BuiltinToolFallbackError, wantErr: ErrBuiltinToolUnsupported},
		{name: "passthrough", tool: ir.Tool{Type: "web_search", RawConfig: raw}, target: ir.ProtocolOpenAIChat, policy: BuiltinToolFallbackPassthrough, want: raw},
		{
			name:   "function",
			tool:   ir.Tool{Type: "custom", Name: "apply_patch"},
			target: ir.ProtocolOpenAIResponses,
			policy: BuiltinToolFallbackFunction,
			want: map[string]any{
				"type": "function",
				"name": "apply_patch",
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{
							"type":        "string",
							"description": "Raw input for the original custom tool.",
						},
					},
					"required":             []string{"input"},
					"additionalProperties": false,
				},
			},
		},
		{name: "function missing name", tool: ir.Tool{Type: "function"}, target: ir.ProtocolOpenAIChat, policy: BuiltinToolFallbackDrop, wantErr: ErrFunctionToolMissingName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTool(tt.tool, ir.ProtocolOpenAIResponses, tt.target, tt.policy, emit)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.reason != "" {
				if got.Dropped == nil || got.Dropped.Reason != tt.reason {
					t.Fatalf("resolved = %#v", got)
				}
				return
			}
			if !reflect.DeepEqual(got.Emit, tt.want) {
				t.Fatalf("emit = %#v, want %#v", got.Emit, tt.want)
			}
		})
	}
}

func TestDroppedToolsMetadata(t *testing.T) {
	t.Run("records and reads a copy", func(t *testing.T) {
		req := &ir.Request{}
		want := []DroppedTool{{Type: "web_search", Reason: DroppedToolReasonCrossProtocolIncompatible}}
		RecordDroppedTools(req, want)
		got := DroppedTools(req)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		got[0].Type = "changed"
		if DroppedTools(req)[0].Type != "web_search" {
			t.Fatal("metadata was exposed for mutation")
		}
	})
	t.Run("nil and empty are no-op", func(t *testing.T) {
		RecordDroppedTools(nil, []DroppedTool{{Type: "x"}})
		req := &ir.Request{}
		RecordDroppedTools(req, nil)
		if req.Metadata != nil || DroppedTools(nil) != nil {
			t.Fatalf("unexpected metadata: %#v", req.Metadata)
		}
	})
}

func TestAssertToolsInvariantPreservesLegacyNameTypeHandling(t *testing.T) {
	tests := []struct {
		name    string
		tool    map[string]any
		wantErr bool
	}{
		{
			name: "chat non-string name is left to protocol validation",
			tool: map[string]any{"type": "function", "function": map[string]any{"name": 123}},
		},
		{
			name: "responses non-string name is left to protocol validation",
			tool: map[string]any{"type": "function", "name": 123},
		},
		{
			name:    "chat missing name is rejected",
			tool:    map[string]any{"type": "function", "function": map[string]any{}},
			wantErr: true,
		},
		{
			name:    "responses empty string name is rejected",
			tool:    map[string]any{"type": "function", "name": ""},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertToolsInvariant([]any{tt.tool})
			if tt.wantErr && !errors.Is(err, ErrFunctionToolMissingName) {
				t.Fatalf("error = %v, want ErrFunctionToolMissingName", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestNormalizeBuiltinToolFallback(t *testing.T) {
	tests := []struct {
		input string
		want  BuiltinToolFallbackPolicy
	}{
		{input: "", want: BuiltinToolFallbackDrop},
		{input: "drop", want: BuiltinToolFallbackDrop},
		{input: "error", want: BuiltinToolFallbackError},
		{input: "passthrough", want: BuiltinToolFallbackPassthrough},
		{input: "function", want: BuiltinToolFallbackFunction},
		{input: "bogus", want: BuiltinToolFallbackDrop},
		{input: "DROP", want: BuiltinToolFallbackDrop},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := NormalizeBuiltinToolFallback(tt.input); got != tt.want {
				t.Fatalf("policy = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveToolRawConfigAndFallbackMetadata(t *testing.T) {
	raw := map[string]any{"type": "web_search", "external_web_access": false}
	emit := TargetEmitFuncs{Function: func(tool ir.Tool) any { return tool }}

	t.Run("same protocol retains raw config", func(t *testing.T) {
		got, err := ResolveTool(ir.Tool{Type: "web_search", RawConfig: raw}, ir.ProtocolOpenAIResponses, ir.ProtocolOpenAIResponses, BuiltinToolFallbackDrop, emit)
		if err != nil || !reflect.DeepEqual(got.Emit, raw) || got.Dropped != nil {
			t.Fatalf("resolved=%#v error=%v", got, err)
		}
	})

	t.Run("function fallback drops unsupported server tool", func(t *testing.T) {
		got, err := ResolveTool(ir.Tool{Type: "web_search", RawConfig: raw}, ir.ProtocolOpenAIResponses, ir.ProtocolOpenAIResponses, BuiltinToolFallbackFunction, emit)
		if err != nil || got.Dropped == nil || got.Dropped.Reason != DroppedToolReasonFunctionFallbackUnsupported {
			t.Fatalf("resolved=%#v error=%v", got, err)
		}
	})

	t.Run("records and reads function fallback mappings", func(t *testing.T) {
		request := &ir.Request{}
		RecordFunctionFallbackTools(request, []FunctionFallbackTool{{Name: "apply_patch", ArgumentName: "input"}, {Name: "shell", ArgumentName: "command"}})
		got := FunctionFallbackTools(request)
		if len(got) != 2 || got["apply_patch"].ArgumentName != "input" || got["shell"].ArgumentName != "command" {
			t.Fatalf("mappings = %#v", got)
		}
		got["apply_patch"] = FunctionFallbackTool{Name: "changed"}
		if FunctionFallbackTools(request)["apply_patch"].Name != "apply_patch" {
			t.Fatal("stored fallback metadata was exposed for mutation")
		}
	})

	t.Run("nil and empty fallback mappings are no-op", func(t *testing.T) {
		RecordFunctionFallbackTools(nil, []FunctionFallbackTool{{Name: "x"}})
		request := &ir.Request{}
		RecordFunctionFallbackTools(request, nil)
		if request.Metadata != nil || FunctionFallbackTools(nil) != nil {
			t.Fatalf("metadata = %#v", request.Metadata)
		}
	})
}

func TestAssertToolsInvariantWireShapes(t *testing.T) {
	tests := []struct {
		name    string
		tool    any
		wantErr bool
	}{
		{name: "chat valid", tool: map[string]any{"type": "function", "function": map[string]any{"name": "f"}}},
		{name: "chat empty", tool: map[string]any{"type": "function", "function": map[string]any{"name": ""}}, wantErr: true},
		{name: "responses valid", tool: map[string]any{"type": "function", "name": "f"}},
		{name: "responses missing", tool: map[string]any{"type": "function"}, wantErr: true},
		{name: "claude valid", tool: map[string]any{"name": "f", "input_schema": nil}},
		{name: "claude empty", tool: map[string]any{"name": "", "input_schema": nil}, wantErr: true},
		{name: "builtin passthrough", tool: map[string]any{"type": "web_search"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertToolsInvariant([]any{tt.tool})
			if tt.wantErr && !errors.Is(err, ErrFunctionToolMissingName) {
				t.Fatalf("error = %v, want missing name", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}
