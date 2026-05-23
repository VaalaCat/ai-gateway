package upstream

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestParseProtocolOverride(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[codec.Protocol]codec.Protocol
	}{
		{
			name: "valid mapping",
			in: map[string]any{
				"openai_chat":      "claude",
				"openai_responses": "openai_chat",
			},
			want: map[codec.Protocol]codec.Protocol{
				codec.ProtocolOpenAIChat:      codec.ProtocolClaude,
				codec.ProtocolOpenAIResponses: codec.ProtocolOpenAIChat,
			},
		},
		{
			name: "auto and empty values dropped",
			in: map[string]any{
				"openai_chat":      "auto",
				"openai_responses": "",
				"claude":           "openai_chat",
			},
			want: map[codec.Protocol]codec.Protocol{
				codec.ProtocolClaude: codec.ProtocolOpenAIChat,
			},
		},
		{
			name: "invalid keys/values dropped",
			in: map[string]any{
				"gemini":      "claude",
				"openai_chat": "gemini",
				"unknown":     "openai_chat",
				"claude":      "openai_chat",
			},
			want: map[codec.Protocol]codec.Protocol{
				codec.ProtocolClaude: codec.ProtocolOpenAIChat,
			},
		},
		{
			name: "non-string values ignored",
			in: map[string]any{
				"openai_chat": 123,
				"claude":      "openai_chat",
			},
			want: map[codec.Protocol]codec.Protocol{
				codec.ProtocolClaude: codec.ProtocolOpenAIChat,
			},
		},
		{
			name: "nil input",
			in:   nil,
			want: nil,
		},
		{
			name: "empty input",
			in:   map[string]any{},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseProtocolOverride(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChannelProtocolOverride(t *testing.T) {
	// Rewritten to use ChannelOverrideRulesFor + rules.ChannelLevel after
	// channelProtocolOverride was removed in favour of the new override rules API.
	tests := []struct {
		name          string
		otherSettings string
		want          map[codec.Protocol]codec.Protocol
	}{
		{
			name:          "empty other_settings",
			otherSettings: "",
			want:          nil,
		},
		{
			name:          "no protocol_override key",
			otherSettings: `{"builtin_tool_fallback":"drop"}`,
			want:          nil,
		},
		{
			name:          "valid mapping",
			otherSettings: `{"protocol_override":{"openai_chat":"claude"}}`,
			want:          map[codec.Protocol]codec.Protocol{codec.ProtocolOpenAIChat: codec.ProtocolClaude},
		},
		{
			name:          "invalid json",
			otherSettings: `{not json`,
			want:          nil,
		},
		{
			name:          "protocol_override wrong type",
			otherSettings: `{"protocol_override":"not-an-object"}`,
			want:          nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: tc.otherSettings}}
			rules := ChannelOverrideRulesFor(ch)
			var got map[codec.Protocol]codec.Protocol
			if rules != nil {
				got = rules.ChannelLevel
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseModelProtocolOverride_Valid(t *testing.T) {
	raw := []any{
		map[string]any{
			"model": "gpt-4o",
			"overrides": map[string]any{
				"openai_chat": "openai_responses",
			},
		},
		map[string]any{
			"model": "deepseek-.*",
			"overrides": map[string]any{
				"*": "claude",
			},
		},
	}
	rules := parseModelProtocolOverride(raw /*channelID=*/, 1)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if !rules[0].IsExact || rules[0].PatternRaw != "gpt-4o" {
		t.Fatalf("rule[0] should be exact 'gpt-4o', got IsExact=%v Pattern=%s", rules[0].IsExact, rules[0].PatternRaw)
	}
	if rules[1].IsExact || rules[1].PatternRaw != "deepseek-.*" {
		t.Fatalf("rule[1] should be regex 'deepseek-.*'; got IsExact=%v Pattern=%s", rules[1].IsExact, rules[1].PatternRaw)
	}
}

func TestParseModelProtocolOverride_InvalidRegex(t *testing.T) {
	raw := []any{
		map[string]any{
			"model":     "[invalid(",
			"overrides": map[string]any{"openai_chat": "claude"},
		},
		map[string]any{
			"model":     "valid-.*",
			"overrides": map[string]any{"openai_chat": "claude"},
		},
	}
	rules := parseModelProtocolOverride(raw, 1)
	if len(rules) != 1 || rules[0].PatternRaw != "valid-.*" {
		t.Fatalf("expected single valid rule; got %+v", rules)
	}
}

func TestParseModelProtocolOverride_InvalidProtocol(t *testing.T) {
	raw := []any{
		map[string]any{
			"model":     "gpt-4o",
			"overrides": map[string]any{"unknown": "claude"},
		},
		map[string]any{
			"model":     "gpt-4o",
			"overrides": map[string]any{"openai_chat": "fake_protocol"},
		},
		map[string]any{
			"model":     "gpt-4o",
			"overrides": map[string]any{"openai_chat": "claude"},
		},
	}
	rules := parseModelProtocolOverride(raw, 1)
	if len(rules) != 1 {
		t.Fatalf("expected 1 valid rule, got %d", len(rules))
	}
}

func TestParseModelProtocolOverride_Empty(t *testing.T) {
	if got := parseModelProtocolOverride(nil, 1); got != nil {
		t.Fatalf("nil input should return nil; got %v", got)
	}
	if got := parseModelProtocolOverride([]any{}, 1); got != nil {
		t.Fatalf("empty input should return nil; got %v", got)
	}
}

func TestParseModelProtocolOverride_AutoSkipped(t *testing.T) {
	raw := []any{
		map[string]any{
			"model":     "gpt-4o",
			"overrides": map[string]any{"openai_chat": "auto"},
		},
	}
	rules := parseModelProtocolOverride(raw, 1)
	if len(rules) != 0 {
		t.Fatalf("auto value should be skipped; got rules %+v", rules)
	}
}

func TestChannelOverrideRulesFor_PicksUpBoth(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, OtherSettings: `{
			"protocol_override": {"openai_chat": "claude"},
			"model_protocol_override": [
				{"model": "gpt-4o", "overrides": {"openai_chat": "openai_responses"}}
			]
		}`}}
	rules := ChannelOverrideRulesFor(ch)
	if rules == nil {
		t.Fatal("expected non-nil rules")
	}
	if len(rules.ChannelLevel) != 1 {
		t.Fatalf("ChannelLevel len = %d, want 1", len(rules.ChannelLevel))
	}
	if len(rules.ModelLevel) != 1 {
		t.Fatalf("ModelLevel len = %d, want 1", len(rules.ModelLevel))
	}
}

func TestChannelOverrideRulesFor_EmptyOtherSettings(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, OtherSettings: ""}}
	if rules := ChannelOverrideRulesFor(ch); rules != nil {
		t.Fatalf("empty OtherSettings should return nil; got %+v", rules)
	}
}

func TestChannelOverrideRulesFor_MalformedJSON(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, OtherSettings: "{not json"}}
	if rules := ChannelOverrideRulesFor(ch); rules != nil {
		t.Fatalf("malformed JSON should return nil; got %+v", rules)
	}
}

func TestChannelOverrideRulesFor_OnlyModelLevel(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, OtherSettings: `{
			"model_protocol_override": [
				{"model": "gpt-4o", "overrides": {"openai_chat": "openai_responses"}}
			]
		}`}}
	rules := ChannelOverrideRulesFor(ch)
	if rules == nil {
		t.Fatal("expected non-nil rules")
	}
	if rules.ChannelLevel != nil {
		t.Fatalf("ChannelLevel should be nil; got %+v", rules.ChannelLevel)
	}
	if len(rules.ModelLevel) != 1 {
		t.Fatalf("ModelLevel len = %d, want 1", len(rules.ModelLevel))
	}
}

func newRule(pattern string, isExact bool, ov map[string]string) modelOverrideRule {
	re := regexp.MustCompile("^" + pattern + "$")
	out := make(map[codec.Protocol]codec.Protocol, len(ov))
	for k, v := range ov {
		var key codec.Protocol
		if k == "*" {
			key = ProtocolWildcard
		} else {
			key = codec.Protocol(k)
		}
		out[key] = codec.Protocol(v)
	}
	return modelOverrideRule{Pattern: re, PatternRaw: pattern, IsExact: isExact, Overrides: out}
}

func TestResolveOverride_NilRules(t *testing.T) {
	if got := ResolveOverride(nil, "gpt-4o"); got != nil {
		t.Fatalf("nil rules → nil; got %v", got)
	}
}

func TestResolveOverride_FallbackToChannelLevel(t *testing.T) {
	rules := &ChannelOverrideRules{
		ChannelLevel: map[codec.Protocol]codec.Protocol{"openai_chat": "claude"},
	}
	got := ResolveOverride(rules, "gpt-4o")
	want := map[codec.Protocol]codec.Protocol{"openai_chat": "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestResolveOverride_ExactBeatsRegex(t *testing.T) {
	rules := &ChannelOverrideRules{
		ModelLevel: []modelOverrideRule{
			newRule("gpt-.*", false, map[string]string{"openai_chat": "claude"}),
			newRule("gpt-4o", true, map[string]string{"openai_chat": "openai_responses"}),
		},
	}
	got := ResolveOverride(rules, "gpt-4o")
	if got["openai_chat"] != "openai_responses" {
		t.Fatalf("exact rule should win; got %v", got)
	}
}

func TestResolveOverride_ExactInboundBeatsWildcardInbound(t *testing.T) {
	rules := &ChannelOverrideRules{
		ModelLevel: []modelOverrideRule{
			newRule("gpt-4o", true, map[string]string{"*": "claude", "openai_chat": "openai_responses"}),
		},
	}
	got := ResolveOverride(rules, "gpt-4o")
	if got["openai_chat"] != "openai_responses" {
		t.Fatalf("explicit inbound key should beat wildcard; got %v", got)
	}
	if got["claude"] != "claude" {
		t.Fatalf("claude inbound should expand from *; got %v", got)
	}
}

func TestResolveOverride_RegexLengthTieBreak(t *testing.T) {
	rules := &ChannelOverrideRules{
		ModelLevel: []modelOverrideRule{
			newRule("gpt-.*", false, map[string]string{"openai_chat": "claude"}),
			newRule("gpt-4-.*", false, map[string]string{"openai_chat": "openai_responses"}),
		},
	}
	got := ResolveOverride(rules, "gpt-4-turbo")
	if got["openai_chat"] != "openai_responses" {
		t.Fatalf("longer pattern should win on tie; got %v", got)
	}
}

func TestResolveOverride_RegexConfigOrderTieBreak(t *testing.T) {
	rules := &ChannelOverrideRules{
		ModelLevel: []modelOverrideRule{
			newRule("gpt-.+", false, map[string]string{"openai_chat": "claude"}),
			newRule("gpt-.+", false, map[string]string{"openai_chat": "openai_responses"}),
		},
	}
	got := ResolveOverride(rules, "gpt-4o")
	if got["openai_chat"] != "claude" {
		t.Fatalf("first config rule should win on length tie; got %v", got)
	}
}

func TestResolveOverride_NoModelMatchUsesChannelLevel(t *testing.T) {
	rules := &ChannelOverrideRules{
		ChannelLevel: map[codec.Protocol]codec.Protocol{"openai_chat": "claude"},
		ModelLevel: []modelOverrideRule{
			newRule("nomatch-.*", false, map[string]string{"openai_chat": "openai_responses"}),
		},
	}
	got := ResolveOverride(rules, "gpt-4o")
	want := map[codec.Protocol]codec.Protocol{"openai_chat": "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no model match → channel level; got %v want %v", got, want)
	}
}

// TestResolveOverride_WildcardExpandsToAllInbounds_NotEndpoints is the
// regression test for a bug where the wildcard inbound was expanded over
// channel.Endpoints (a JSON string of upstream endpoints) instead of the
// fixed set of valid inbound protocols. Inbound protocol is determined
// by the client request URL, not by the channel's upstream endpoints, so
// the wildcard MUST cover all 3 valid inbounds regardless of channel
// configuration. Reachability is enforced later by codec.NegotiateOutboundProtocol.
func TestResolveOverride_WildcardExpandsToAllInbounds_NotEndpoints(t *testing.T) {
	// Reproduces user-reported bug: model "kimi.*" with overrides {"*":"claude"}.
	// Before fix: inbound openai_chat → wildcard not expanded → empty map.
	// After fix: every valid inbound (openai_chat/openai_responses/claude) → claude.
	rules := &ChannelOverrideRules{
		ModelLevel: []modelOverrideRule{
			newRule("kimi.*", false, map[string]string{"*": "claude"}),
		},
	}
	got := ResolveOverride(rules, "kimi-for-coding")
	if got[codec.ProtocolOpenAIChat] != codec.ProtocolClaude {
		t.Fatalf("openai_chat should map to claude via wildcard; got map=%v", got)
	}
	if got[codec.ProtocolOpenAIResponses] != codec.ProtocolClaude {
		t.Fatalf("openai_responses should map to claude via wildcard; got map=%v", got)
	}
	if got[codec.ProtocolClaude] != codec.ProtocolClaude {
		t.Fatalf("claude should map to claude via wildcard (identity is fine); got map=%v", got)
	}
}
