package channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestBuildDataFlowResponse_EmptyChannel(t *testing.T) {
	resp := buildDataFlowResponse(&models.Channel{})
	if resp.ResolvedProtocol != "openai_chat" {
		t.Fatalf("ResolvedProtocol = %q, want openai_chat", resp.ResolvedProtocol)
	}
	if len(resp.Steps) != 10 {
		t.Fatalf("Steps len = %d, want 10 (full catalog)", len(resp.Steps))
	}
	active := map[string]bool{}
	for _, s := range resp.Steps {
		active[s.Key] = s.Active
	}
	for _, k := range []string{"thinking_passthrough", "thinking_strip", "encode", "forward_client_headers", "upstream_script"} {
		if !active[k] {
			t.Fatalf("step %q should be active for empty openai_chat channel", k)
		}
	}
	for _, k := range []string{"model_mapping", "inject_system_prompt", "role_mapping", "param_override", "header_override"} {
		if active[k] {
			t.Fatalf("step %q should be skipped for empty channel", k)
		}
	}
}

func TestBuildDataFlowResponse_ModelMappingActive(t *testing.T) {
	resp := buildDataFlowResponse(&models.Channel{ModelMapping: `{"gpt-4o":"gpt-4o-mini"}`})
	for _, s := range resp.Steps {
		if s.Key == "model_mapping" {
			if !s.Active {
				t.Fatal("model_mapping should be active")
			}
			if s.Detail != "1" {
				t.Fatalf("model_mapping Detail = %q, want \"1\"", s.Detail)
			}
			return
		}
	}
	t.Fatal("model_mapping step missing from catalog")
}

func TestBuildDataFlowResponse_ParamOverrideActive(t *testing.T) {
	resp := buildDataFlowResponse(&models.Channel{ChannelCore: models.ChannelCore{ParamOverride: `{"temperature":0.5}`}})
	for _, s := range resp.Steps {
		if s.Key == "param_override" {
			if !s.Active {
				t.Fatal("param_override should be active")
			}
			if s.Detail != "temperature" {
				t.Fatalf("param_override Detail = %q, want \"temperature\"", s.Detail)
			}
			return
		}
	}
	t.Fatal("param_override step missing")
}

func TestBuildDataFlowResponse_RoleMappingActive(t *testing.T) {
	resp := buildDataFlowResponse(&models.Channel{ChannelCore: models.ChannelCore{RoleMapping: `{"default":{"system":"user"}}`}})
	for _, s := range resp.Steps {
		if s.Key == "role_mapping" {
			if !s.Active {
				t.Fatal("role_mapping should be active when RoleMapping configured")
			}
			return
		}
	}
	t.Fatal("role_mapping step missing")
}
