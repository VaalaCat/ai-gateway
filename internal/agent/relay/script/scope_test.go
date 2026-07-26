package script

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchScope(t *testing.T) {
	input := MatchInput{Source: attemptproxy.SourceAdmin, ChannelID: 7, Model: "gpt-4o", UserID: 11, GroupID: 13}
	tests := []struct {
		name  string
		scope models.ScriptScope
		input MatchInput
		want  bool
	}{
		{name: "global", scope: models.ScriptScope{}, input: MatchInput{}, want: true},
		{name: "admin channel", scope: models.ScriptScope{ChannelIDs: []uint{7}}, input: input, want: true},
		{name: "private channel", scope: models.ScriptScope{PrivateChannelIDs: []uint{7}}, input: MatchInput{Source: attemptproxy.SourcePrivate, ChannelID: 7}, want: true},
		{name: "model", scope: models.ScriptScope{ModelNames: []string{"gpt-4o"}}, input: input, want: true},
		{name: "group", scope: models.ScriptScope{GroupIDs: []uint{13}}, input: input, want: true},
		{name: "user", scope: models.ScriptScope{UserIDs: []uint{11}}, input: input, want: true},
		{name: "any condition OR", scope: models.ScriptScope{ChannelIDs: []uint{99}, ModelNames: []string{"other"}, UserIDs: []uint{11}}, input: input, want: true},
		{name: "same numeric source isolated admin", scope: models.ScriptScope{PrivateChannelIDs: []uint{7}}, input: input, want: false},
		{name: "same numeric source isolated private", scope: models.ScriptScope{ChannelIDs: []uint{7}}, input: MatchInput{Source: attemptproxy.SourcePrivate, ChannelID: 7}, want: false},
		{name: "zero values do not match", scope: models.ScriptScope{ChannelIDs: []uint{0}, PrivateChannelIDs: []uint{0}, ModelNames: []string{""}, GroupIDs: []uint{0}, UserIDs: []uint{0}}, input: MatchInput{}, want: false},
		{name: "unknown source does not match channel", scope: models.ScriptScope{ChannelIDs: []uint{7}, PrivateChannelIDs: []uint{7}}, input: MatchInput{ChannelID: 7}, want: false},
		{name: "no match", scope: models.ScriptScope{ChannelIDs: []uint{8}, PrivateChannelIDs: []uint{8}, ModelNames: []string{"other"}, GroupIDs: []uint{14}, UserIDs: []uint{12}}, input: input, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MatchScope(tt.scope, tt.input))
		})
	}
}

func TestMatchScope_LegacyJSONScope(t *testing.T) {
	var scope models.ScriptScope
	require.NoError(t, json.Unmarshal([]byte(`{"channel_ids":[1],"model_names":["gpt-4o"]}`), &scope))

	assert.True(t, MatchScope(scope, MatchInput{Source: attemptproxy.SourceAdmin, ChannelID: 1}))
	assert.True(t, MatchScope(scope, MatchInput{Model: "gpt-4o"}))
	assert.False(t, MatchScope(scope, MatchInput{Source: attemptproxy.SourceAdmin, ChannelID: 2, Model: "other"}))
}
