package codec

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeBuiltinToolFallback(t *testing.T) {
	cases := []struct {
		in   string
		want BuiltinToolFallbackPolicy
	}{
		{"", BuiltinToolFallbackDrop},
		{"drop", BuiltinToolFallbackDrop},
		{"error", BuiltinToolFallbackError},
		{"passthrough", BuiltinToolFallbackPassthrough},
		{"bogus", BuiltinToolFallbackDrop},
		{"DROP", BuiltinToolFallbackDrop}, // 大小写敏感，非精确匹配回落到默认
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := NormalizeBuiltinToolFallback(c.in)
			if got != c.want {
				t.Errorf("NormalizeBuiltinToolFallback(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestErrorSentinels(t *testing.T) {
	wrapped := errors.New("wrapped: " + ErrFunctionToolMissingName.Error())
	if errors.Is(wrapped, ErrFunctionToolMissingName) {
		t.Error("wrapping via concatenation should not match errors.Is")
	}
	if !errors.Is(ErrFunctionToolMissingName, ErrFunctionToolMissingName) {
		t.Error("identity errors.Is check failed")
	}
	if !errors.Is(ErrBuiltinToolUnsupported, ErrBuiltinToolUnsupported) {
		t.Error("identity errors.Is check failed")
	}
}

// 测试用的 stub emit：目标协议简单地把输入转成 map，便于断言
func stubEmit() TargetEmitFuncs {
	return TargetEmitFuncs{
		Function: func(t Tool) any {
			return map[string]any{
				"kind":        "function",
				"name":        t.Name,
				"description": t.Description,
			}
		},
	}
}

func TestResolveTool(t *testing.T) {
	rawCfg := map[string]any{"type": "web_search", "external_web_access": false}

	cases := []struct {
		name        string
		tool        Tool
		source      Protocol
		target      Protocol
		policy      BuiltinToolFallbackPolicy
		wantEmit    any
		wantDropped *DroppedTool
		wantErr     error
	}{
		// Case A — 合法 function，每个目标协议一行
		{
			name:     "A_function_to_chat",
			tool:     Tool{Type: "function", Name: "get_weather", Description: "d"},
			source:   ProtocolOpenAIResponses,
			target:   ProtocolOpenAIChat,
			policy:   BuiltinToolFallbackDrop,
			wantEmit: map[string]any{"kind": "function", "name": "get_weather", "description": "d"},
		},
		{
			name:     "A_empty_type_normalizes_to_function",
			tool:     Tool{Type: "", Name: "f", Description: "d"},
			source:   ProtocolOpenAIChat,
			target:   ProtocolClaude,
			policy:   BuiltinToolFallbackDrop,
			wantEmit: map[string]any{"kind": "function", "name": "f", "description": "d"},
		},
		// Case B — 空 name function，每个 policy 各一行证明 policy 无关
		{
			name:    "B_empty_name_drop",
			tool:    Tool{Type: "function", Name: ""},
			source:  ProtocolOpenAIResponses,
			target:  ProtocolOpenAIChat,
			policy:  BuiltinToolFallbackDrop,
			wantErr: ErrFunctionToolMissingName,
		},
		{
			name:    "B_empty_name_error",
			tool:    Tool{Type: "function", Name: ""},
			source:  ProtocolOpenAIResponses,
			target:  ProtocolOpenAIChat,
			policy:  BuiltinToolFallbackError,
			wantErr: ErrFunctionToolMissingName,
		},
		{
			name:    "B_empty_name_passthrough",
			tool:    Tool{Type: "function", Name: ""},
			source:  ProtocolOpenAIResponses,
			target:  ProtocolOpenAIChat,
			policy:  BuiltinToolFallbackPassthrough,
			wantErr: ErrFunctionToolMissingName,
		},
		// Case C — 同协议透传
		{
			name:     "C_builtin_same_protocol_responses",
			tool:     Tool{Type: "web_search", RawConfig: rawCfg},
			source:   ProtocolOpenAIResponses,
			target:   ProtocolOpenAIResponses,
			policy:   BuiltinToolFallbackDrop,
			wantEmit: rawCfg,
		},
		// Case D — drop
		{
			name:   "D_builtin_cross_drop",
			tool:   Tool{Type: "web_search", Name: "", RawConfig: rawCfg},
			source: ProtocolOpenAIResponses,
			target: ProtocolOpenAIChat,
			policy: BuiltinToolFallbackDrop,
			wantDropped: &DroppedTool{
				Type:   "web_search",
				Reason: DroppedToolReasonCrossProtocolIncompatible,
			},
		},
		{
			name:   "D_builtin_cross_drop_to_claude",
			tool:   Tool{Type: "file_search", Name: "", RawConfig: rawCfg},
			source: ProtocolOpenAIResponses,
			target: ProtocolClaude,
			policy: BuiltinToolFallbackDrop,
			wantDropped: &DroppedTool{
				Type:   "file_search",
				Reason: DroppedToolReasonCrossProtocolIncompatible,
			},
		},
		// Case E — error
		{
			name:    "E_builtin_cross_error",
			tool:    Tool{Type: "web_search", RawConfig: rawCfg},
			source:  ProtocolOpenAIResponses,
			target:  ProtocolOpenAIChat,
			policy:  BuiltinToolFallbackError,
			wantErr: ErrBuiltinToolUnsupported,
		},
		// Case F — passthrough
		{
			name:     "F_builtin_cross_passthrough",
			tool:     Tool{Type: "web_search", RawConfig: rawCfg},
			source:   ProtocolOpenAIResponses,
			target:   ProtocolOpenAIChat,
			policy:   BuiltinToolFallbackPassthrough,
			wantEmit: rawCfg,
		},
		// 未知 policy 字符串归一到 drop
		{
			name:   "unknown_policy_treated_as_drop",
			tool:   Tool{Type: "web_search", RawConfig: rawCfg},
			source: ProtocolOpenAIResponses,
			target: ProtocolOpenAIChat,
			policy: BuiltinToolFallbackPolicy("garbage"),
			wantDropped: &DroppedTool{
				Type:   "web_search",
				Reason: DroppedToolReasonCrossProtocolIncompatible,
			},
		},
		// 未知入站协议（零值）走跨协议分支
		{
			name:   "unknown_inbound_treated_as_cross_drop",
			tool:   Tool{Type: "web_search", RawConfig: rawCfg},
			source: "",
			target: ProtocolOpenAIChat,
			policy: BuiltinToolFallbackDrop,
			wantDropped: &DroppedTool{
				Type:   "web_search",
				Reason: DroppedToolReasonCrossProtocolIncompatible,
			},
		},
		// 内置工具但 RawConfig 为 nil —— 降级到跨协议分支即可（drop）
		{
			name:   "builtin_nil_rawconfig_drop",
			tool:   Tool{Type: "web_search"},
			source: ProtocolOpenAIResponses,
			target: ProtocolOpenAIResponses,
			policy: BuiltinToolFallbackDrop,
			wantDropped: &DroppedTool{
				Type:   "web_search",
				Reason: DroppedToolReasonCrossProtocolIncompatible,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			normalized := NormalizeBuiltinToolFallback(string(c.policy))
			got, err := ResolveTool(c.tool, c.source, c.target, normalized, stubEmit())

			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("want err %v, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}

			if c.wantDropped != nil {
				if got.Emit != nil {
					t.Errorf("expected Emit nil when dropped, got %v", got.Emit)
				}
				if got.Dropped == nil {
					t.Fatalf("expected Dropped non-nil")
				}
				if got.Dropped.Type != c.wantDropped.Type || got.Dropped.Reason != c.wantDropped.Reason {
					t.Errorf("want dropped %+v, got %+v", c.wantDropped, got.Dropped)
				}
				return
			}

			if got.Dropped != nil {
				t.Errorf("expected Dropped nil, got %+v", got.Dropped)
			}
			if !reflect.DeepEqual(got.Emit, c.wantEmit) {
				t.Errorf("want emit %v, got %v", c.wantEmit, got.Emit)
			}
		})
	}
}

func TestAssertToolsInvariant(t *testing.T) {
	// OpenAI Chat 嵌套形状：{type:"function", function:{name:"..."}}
	chatOK := map[string]any{"type": "function", "function": map[string]any{"name": "f"}}
	chatBad := map[string]any{"type": "function", "function": map[string]any{"name": ""}}

	// OpenAI Responses 扁平形状：{type:"function", name:"..."}
	respOK := map[string]any{"type": "function", "name": "g"}
	respBad := map[string]any{"type": "function", "name": ""}

	// Claude 形状：{name:"..."}（顶层无 type=function，但有 name）
	claudeOK := map[string]any{"name": "h", "input_schema": nil}
	claudeBad := map[string]any{"name": "", "input_schema": nil}

	// 非 function 条目（内置工具 RawConfig）不应触发断言
	builtinRaw := map[string]any{"type": "web_search", "external_web_access": true}

	cases := []struct {
		name    string
		tools   []any
		wantErr error
	}{
		{"empty", nil, nil},
		{"chat_ok", []any{chatOK}, nil},
		{"chat_bad", []any{chatBad}, ErrFunctionToolMissingName},
		{"responses_ok", []any{respOK}, nil},
		{"responses_bad", []any{respBad}, ErrFunctionToolMissingName},
		{"claude_ok", []any{claudeOK}, nil},
		{"claude_bad", []any{claudeBad}, ErrFunctionToolMissingName},
		{"builtin_passthrough_ok", []any{builtinRaw}, nil},
		{"mixed_one_bad", []any{chatOK, respBad}, ErrFunctionToolMissingName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AssertToolsInvariant(c.tools)
			if c.wantErr == nil {
				if err != nil {
					t.Errorf("want nil err, got %v", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Errorf("want err %v, got %v", c.wantErr, err)
			}
		})
	}
}

func TestRecordDroppedTools(t *testing.T) {
	t.Run("empty_is_noop", func(t *testing.T) {
		req := &Request{}
		RecordDroppedTools(req, nil)
		if req.Metadata != nil {
			t.Errorf("expected Metadata nil, got %v", req.Metadata)
		}
		RecordDroppedTools(req, []DroppedTool{})
		if req.Metadata != nil {
			t.Errorf("expected Metadata nil with empty slice, got %v", req.Metadata)
		}
	})

	t.Run("populates_metadata", func(t *testing.T) {
		req := &Request{}
		dropped := []DroppedTool{
			{Type: "web_search", Reason: DroppedToolReasonCrossProtocolIncompatible},
		}
		RecordDroppedTools(req, dropped)
		got, ok := req.Metadata["dropped_tools"].([]DroppedTool)
		if !ok {
			t.Fatalf("expected []DroppedTool, got %T", req.Metadata["dropped_tools"])
		}
		if len(got) != 1 || got[0].Type != "web_search" {
			t.Errorf("unexpected payload: %+v", got)
		}
	})

	t.Run("preserves_existing_metadata", func(t *testing.T) {
		req := &Request{Metadata: map[string]any{"trace_id": "abc"}}
		RecordDroppedTools(req, []DroppedTool{{Type: "file_search", Reason: "x"}})
		if req.Metadata["trace_id"] != "abc" {
			t.Errorf("existing key lost: %v", req.Metadata)
		}
		if _, ok := req.Metadata["dropped_tools"]; !ok {
			t.Errorf("dropped_tools not set")
		}
	})
}
