package legacy

import (
	"testing"
)

func TestParseJSONMap(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect int // expected map length, -1 means nil
	}{
		{"empty string", "", -1},
		{"valid json", `{"key":"value","num":123}`, 2},
		{"invalid json", `{bad`, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseJSONMap(tt.input)
			if tt.expect == -1 {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
			} else if len(result) != tt.expect {
				t.Errorf("expected len %d, got %d", tt.expect, len(result))
			}
		})
	}
}

func TestParseChannelSetting(t *testing.T) {
	raw := `{"force_format":true,"thinking_to_content":true,"proxy":"http://proxy:8080","system_prompt":"You are helpful."}`
	s := ParseChannelSetting(raw)
	if !s.ForceFormat {
		t.Error("ForceFormat should be true")
	}
	if !s.ThinkingToContent {
		t.Error("ThinkingToContent should be true")
	}
	if s.Proxy != "http://proxy:8080" {
		t.Errorf("Proxy = %s, want http://proxy:8080", s.Proxy)
	}
	if s.SystemPrompt != "You are helpful." {
		t.Errorf("SystemPrompt = %s, want 'You are helpful.'", s.SystemPrompt)
	}

	empty := ParseChannelSetting("")
	if empty.ForceFormat || empty.Proxy != "" {
		t.Error("empty input should return zero value")
	}
}

func TestParseChannelOtherSettings(t *testing.T) {
	raw := `{"claude_beta_query":true,"allow_service_tier":true,"aws_key_type":"api_key"}`
	s := ParseChannelOtherSettings(raw)
	if !s.ClaudeBetaQuery {
		t.Error("ClaudeBetaQuery should be true")
	}
	if !s.AllowServiceTier {
		t.Error("AllowServiceTier should be true")
	}
	if s.AwsKeyType != "api_key" {
		t.Errorf("AwsKeyType = %s, want api_key", s.AwsKeyType)
	}

	empty := ParseChannelOtherSettings("")
	if empty.ClaudeBetaQuery {
		t.Error("empty input should return zero value")
	}
}
