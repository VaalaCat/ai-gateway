package dataflow

import (
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

func TestDescribeChannelDataFlowPreservesElevenStepContract(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI,
			SystemPrompt: "p", RoleMapping: `{"default":{"system":"user"}}`,
			ParamOverride: `{"temperature":0.5}`, OtherSettings: `{"inline_image_url":true}`,
		},
		ModelMapping: `{"a":"b"}`, HeaderOverride: `{"X-A":"1"}`,
	}
	want := []string{
		"model_mapping", "inject_system_prompt", "role_mapping",
		"thinking_passthrough", "thinking_strip", "inline_image",
		"encode", "forward_client_headers", "param_override", "header_override", "upstream_script",
	}
	if got := keysOfInfos(AllStepInfos()); !equalStrs(got, want) {
		t.Fatalf("AllStepInfos keys = %v, want stable 11-step contract %v", got, want)
	}
	if got := keysOfInfos(DescribeChannelDataFlow(ch, llmkit.ProtocolOpenAIChat)); !equalStrs(got, want) {
		t.Fatalf("active dataflow keys = %v, want all 11 configured steps %v", got, want)
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
