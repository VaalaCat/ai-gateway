package dataflow

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func keysOf(f *ChannelDataFlow) []string {
	var out []string
	for _, info := range f.Describe() {
		out = append(out, info.Key)
	}
	return out
}

func TestBuild_BareChannel_OpenAIChat(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI}, Key: "k"}
	f := BuildChannelDataFlow(ch, codec.ProtocolOpenAIChat, &openai.ChatCodec{}, StepDeps{})
	got := keysOf(f)
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
	f := BuildChannelDataFlow(ch, codec.ProtocolOpenAIChat, &openai.ChatCodec{}, StepDeps{})
	want := []string{"model_mapping", "inject_system_prompt", "role_mapping",
		"thinking_passthrough", "thinking_strip", "encode", "forward_client_headers", "param_override", "header_override", "upstream_script"}
	if got := keysOf(f); !equalStrs(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestBuild_NonOpenAIChat_NoThinking(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeAnthropic}, Key: "k"}
	f := BuildChannelDataFlow(ch, codec.ProtocolClaude, &openai.ChatCodec{}, StepDeps{})
	for _, k := range keysOf(f) {
		if k == "thinking_passthrough" || k == "thinking_strip" {
			t.Fatalf("thinking step present for non-openai_chat: %v", keysOf(f))
		}
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
