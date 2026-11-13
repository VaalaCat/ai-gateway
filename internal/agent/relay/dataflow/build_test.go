package dataflow

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func keysOf(f *ChannelDataFlow) []string {
	var out []string
	for _, info := range f.Describe() {
		out = append(out, info.Key)
	}
	return out
}

func keysOfInfos(infos []StepInfo) []string {
	keys := make([]string, 0, len(infos))
	for _, info := range infos {
		keys = append(keys, info.Key)
	}
	return keys
}

func TestBuild_BareChannel_OpenAIChat(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI}, Key: "k"}
	got := keysOfInfos(DescribeChannelDataFlow(ch, llmkit.ProtocolOpenAIChat))
	want := []string{"thinking_passthrough", "thinking_strip", "encode", "forward_client_headers", "upstream_script"}
	if !equalStrs(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestBuild_FullChannel_OpenAIChat(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID:            1,
			Type:          consts.ChannelTypeOpenAI,
			SystemPrompt:  "p",
			RoleMapping:   `{"default":{"system":"user"}}`,
			ParamOverride: `{"temperature":0.5}`,
		},
		Key:            "k",
		ModelMapping:   `{"a":"b"}`,
		HeaderOverride: `{"X-A":"1"}`,
	}
	want := []string{"model_mapping", "inject_system_prompt", "role_mapping",
		"thinking_passthrough", "thinking_strip", "encode", "forward_client_headers", "param_override", "header_override", "upstream_script"}
	if got := keysOfInfos(DescribeChannelDataFlow(ch, llmkit.ProtocolOpenAIChat)); !equalStrs(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestBuild_NonOpenAIChat_NoThinking(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeAnthropic}, Key: "k"}
	got := keysOfInfos(DescribeChannelDataFlow(ch, llmkit.ProtocolClaudeMessages))
	for _, k := range got {
		if k == "thinking_passthrough" || k == "thinking_strip" {
			t.Fatalf("thinking step present for non-openai_chat: %v", got)
		}
	}
}

func TestDescribeChannelDataFlowIncludesToolSchemaCompatibility(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI,
			SystemPrompt: "p", RoleMapping: `{"default":{"system":"user"}}`,
			ParamOverride: `{"temperature":0.5}`, OtherSettings: `{"inline_image_url":true,"model_tool_schema_compatibility":[{"model_pattern":"gemini","profile":"gemini_compat"}]}`,
		},
		ModelMapping: `{"a":"b"}`, HeaderOverride: `{"X-A":"1"}`,
	}
	want := []string{
		"model_mapping", "inject_system_prompt", "role_mapping",
		"thinking_passthrough", "thinking_strip", "tool_compatibility", "inline_image",
		"encode", "forward_client_headers", "param_override", "header_override", "upstream_script",
	}
	if got := keysOfInfos(AllStepInfos()); !equalStrs(got, want) {
		t.Fatalf("AllStepInfos keys = %v, want configured step order %v", got, want)
	}
	if got := keysOfInfos(DescribeChannelDataFlow(ch, llmkit.ProtocolOpenAIChat)); !equalStrs(got, want) {
		t.Fatalf("active dataflow keys = %v, want all configured steps %v", got, want)
	}
}

func TestBuildChannelRequestFlowOrder(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID:            1,
			Type:          consts.ChannelTypeOpenAI,
			SystemPrompt:  "p",
			RoleMapping:   `{"default":{"system":"user"}}`,
			OtherSettings: `{"inline_image_url":true}`,
		},
		Key:          "k",
		ModelMapping: `{"a":"b"}`,
	}

	got := keysOf(BuildChannelRequestFlow(ch, llmkit.ProtocolOpenAIChat, StepDeps{}))
	want := []string{
		"model_mapping",
		"inject_system_prompt",
		"role_mapping",
		"thinking_passthrough",
		"thinking_strip",
		"inline_image",
	}
	if !equalStrs(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestBuildChannelRequestFlowEmptyConfigKeepsOnlyRequiredSteps(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeAnthropic}}

	got := keysOf(BuildChannelRequestFlow(ch, llmkit.ProtocolClaude, StepDeps{}))
	if len(got) != 0 {
		t.Fatalf("keys = %v, want no request steps", got)
	}
}

func TestBuildChannelRequestFlowClaudeThinking(t *testing.T) {
	for _, test := range []struct {
		name         string
		rules        string
		existing     bool
		wantThinking bool
	}{
		{"mapped_ep_matches", `[{"model_pattern":"^ep-","send_back_thinking":true}]`, false, true},
		{"original_model_matches", `[{"model_pattern":"^claude-","send_back_thinking":true}]`, false, true},
		{"unmatched_does_not_add", `[{"model_pattern":"^other-","send_back_thinking":true}]`, false, false},
		{"first_false_wins", `[{"model_pattern":"^ep-","send_back_thinking":false},{"model_pattern":".*","send_back_thinking":true}]`, false, false},
		{"original_false_before_mapped_true", `[{"model_pattern":"^claude-","send_back_thinking":false},{"model_pattern":"^ep-","send_back_thinking":true}]`, false, false},
		{"mapped_false_before_original_true", `[{"model_pattern":"^ep-","send_back_thinking":false},{"model_pattern":"^claude-","send_back_thinking":true}]`, false, false},
		{"mapped_true_before_original_false", `[{"model_pattern":"^ep-","send_back_thinking":true},{"model_pattern":"^claude-","send_back_thinking":false}]`, false, true},
		{"default_does_not_add", `[]`, false, false},
		{"enabled_preserves_real_thinking", `[{"model_pattern":"^ep-","send_back_thinking":true}]`, true, true},
		{"default_preserves_real_thinking", `[]`, true, true},
		{"false_preserves_real_thinking", `[{"model_pattern":"^ep-","send_back_thinking":false}]`, true, true},
		{"unmatched_preserves_real_thinking", `[{"model_pattern":"^other-","send_back_thinking":true}]`, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 1. 解码包含工具调用历史的 Claude 请求。
			thinkingJSON := ""
			if test.existing {
				thinkingJSON = `{"type":"thinking","thinking":"real reasoning","signature":"signed-thinking"},`
			}
			codec := llmkit.NewCodec()
			decoded, err := codec.DecodeRequest(llmkit.DecodeRequestInput{
				Method: "POST", Path: "/v1/messages",
				Body: []byte(`{"model":"claude-model","max_tokens":100,"messages":[
					{"role":"user","content":"use tool"},
					{"role":"assistant","content":[` + thinkingJSON + `{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},
					{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result"}]}
				]}`),
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2. 运行模型映射和 thinking 规则，再编码成 Claude 上游请求。
			ch := &models.Channel{
				ChannelCore: models.ChannelCore{
					Type:          consts.ChannelTypeAnthropic,
					OtherSettings: `{"model_thinking_passthrough":` + test.rules + `}`,
				},
				ModelMapping: `{"claude-model":"ep-20260611"}`,
			}
			pass := &Pass{Original: CloneRequest(&decoded.Request), Working: &decoded.Request}
			flow := BuildChannelRequestFlow(ch, llmkit.ProtocolClaudeMessages, StepDeps{})
			if err := flow.Run(context.Background(), pass); err != nil {
				t.Fatal(err)
			}
			encoded, err := codec.EncodeRequest(llmkit.EncodeRequestInput{
				Request: *pass.Working,
				Target:  llmkit.Target{Protocol: llmkit.ProtocolClaudeMessages, Model: pass.Working.Model},
				Options: llmkit.ConversionOptions{RequestFields: llmkit.DefaultRequestFieldPermissions()},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 3. 核对实际 wire 上的模型、thinking 内容和工具调用顺序。
			var body struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(encoded.Body, &body); err != nil {
				t.Fatal(err)
			}
			if body.Model != "ep-20260611" || len(body.Messages) != 3 || body.Messages[1].Role != "assistant" {
				t.Fatalf("unexpected upstream request: %s", encoded.Body)
			}
			var content []map[string]any
			if err := json.Unmarshal(body.Messages[1].Content, &content); err != nil {
				t.Fatal(err)
			}
			want := []map[string]any{}
			if test.wantThinking {
				thinking := map[string]any{"type": "thinking", "thinking": ""}
				if test.existing {
					thinking["thinking"] = "real reasoning"
					thinking["signature"] = "signed-thinking"
				}
				want = append(want, thinking)
			}
			want = append(want, map[string]any{"type": "tool_use", "id": "call_1", "name": "lookup", "input": map[string]any{}})
			if !reflect.DeepEqual(content, want) {
				t.Fatalf("assistant content = %#v, want %#v; body=%s", content, want, encoded.Body)
			}
		})
	}
}

func TestBuildChannelRequestFlowNilAgentDescribeDoesNotPanic(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID:            1,
			Type:          consts.ChannelTypeOpenAI,
			OtherSettings: `{"inline_image_url":true}`,
		},
	}

	flow := BuildChannelRequestFlow(ch, llmkit.ProtocolOpenAIChat, StepDeps{Agent: nil})
	got := keysOf(flow)
	want := []string{"thinking_passthrough", "thinking_strip", "inline_image"}
	if !equalStrs(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
