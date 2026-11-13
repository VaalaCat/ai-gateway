package upstream

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestApplyChannelAuth(t *testing.T) {
	defaultAuth := &models.AuthType{Header: "Authorization", Value: "Bearer ${CHANNEL_KEY}"}
	tests := []struct {
		name       string
		auth       models.ChannelAuth
		protocol   llmkit.Protocol
		key        string
		wantHeader string
		wantValue  string
		wantCode   string
	}{
		{name: "empty preserves defaults", protocol: llmkit.ProtocolClaudeMessages, wantHeader: "X-Api-Key", wantValue: "default-key"},
		{name: "messages inherits bearer", auth: models.ChannelAuth{Default: defaultAuth}, protocol: llmkit.ProtocolClaudeMessages, key: "selected-key", wantHeader: "Authorization", wantValue: "Bearer selected-key"},
		{name: "key is not a template", auth: models.ChannelAuth{Default: defaultAuth}, protocol: llmkit.ProtocolClaudeMessages, key: "literal-${OTHER}", wantHeader: "Authorization", wantValue: "Bearer literal-${OTHER}"},
		{name: "protocol replaces entire pair", auth: models.ChannelAuth{Default: defaultAuth, Protocols: map[string]models.AuthType{"messages": {Header: "X-Custom-Auth", Value: "literal-secret"}}}, protocol: llmkit.ProtocolClaudeMessages, wantHeader: "X-Custom-Auth", wantValue: "literal-secret"},
		{name: "responses override", auth: models.ChannelAuth{Protocols: map[string]models.AuthType{"responses": {Header: "X-Response-Key", Value: "${CHANNEL_KEY}"}}}, protocol: llmkit.ProtocolOpenAIResponses, key: "response-key", wantHeader: "X-Response-Key", wantValue: "response-key"},
		{name: "chat override", auth: models.ChannelAuth{Protocols: map[string]models.AuthType{"chat_completions": {Header: "X-Chat-Key", Value: "${CHANNEL_KEY}"}}}, protocol: llmkit.ProtocolOpenAIChat, key: "chat-key", wantHeader: "X-Chat-Key", wantValue: "chat-key"},
		{name: "gemini override", auth: models.ChannelAuth{Protocols: map[string]models.AuthType{"generate_content": {Header: "Authorization", Value: "Bearer ${CHANNEL_KEY}"}}}, protocol: llmkit.ProtocolGemini, key: "gemini-key", wantHeader: "Authorization", wantValue: "Bearer gemini-key"},
		{name: "unmatched protocol preserves defaults", auth: models.ChannelAuth{Protocols: map[string]models.AuthType{"responses": {Header: "X-Response-Key", Value: "literal"}}}, protocol: llmkit.ProtocolClaudeMessages, wantHeader: "X-Api-Key", wantValue: "default-key"},
		{name: "missing selected key", auth: models.ChannelAuth{Default: defaultAuth}, protocol: llmkit.ProtocolClaudeMessages, wantCode: "upstream_auth_key_missing"},
		{name: "invalid persisted config", auth: models.ChannelAuth{Default: &models.AuthType{Header: "Host", Value: "never-echo-this"}}, wantCode: "invalid_upstream_auth"},
		{name: "expanded invalid key", auth: models.ChannelAuth{Default: defaultAuth}, key: "never-echo-this\r\nInjected: yes", wantCode: "invalid_upstream_auth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/messages", nil)
			request.Header.Set("X-Api-Key", "default-key")
			request.Header["authorization"] = []string{"client-gateway-credential"}
			request.Header["x-API-key"] = []string{"another-default"}
			request.Header.Set("X-Goog-Api-Key", "default-google-key")
			channel := &models.Channel{ChannelCore: models.ChannelCore{Auth: datatypes.NewJSONType(test.auth)}, Key: test.key}
			err := ApplyChannelAuth(request, channel, test.protocol)
			if test.wantCode != "" {
				var authErr *ChannelAuthError
				require.ErrorAs(t, err, &authErr)
				require.Equal(t, test.wantCode, authErr.Code)
				require.NotContains(t, err.Error(), "never-echo-this")
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantValue, request.Header.Get(test.wantHeader))
			if test.wantHeader != "X-Api-Key" {
				require.Empty(t, request.Header.Get("X-Api-Key"))
				require.NotContains(t, request.Header, "x-API-key")
				require.NotContains(t, request.Header, "authorization")
				require.Empty(t, request.Header.Get("X-Goog-Api-Key"))
			}
			require.Equal(t, test.auth, channel.Auth.Data(), "request expansion must not rewrite channel configuration")
		})
	}
}
