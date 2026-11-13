package ctxbuild

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func TestAffinityIdentityHeaderPriority(t *testing.T) {
	// 1. 按优先级读取非空会话头，其他名称不参与匹配。
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"explicit wins", map[string]string{"Session-Id": "explicit", "X-Claude-Code-Session-Id": "claude"}, "explicit"},
		{"claude fallback", map[string]string{"X-Claude-Code-Session-Id": "claude"}, "claude"},
		{"blank explicit", map[string]string{"Session-Id": " \t", "X-Claude-Code-Session-Id": " claude\t "}, "claude"},
		{"case insensitive", map[string]string{"x-claude-code-session-id": "claude"}, "claude"},
		{"both blank", map[string]string{"Session-Id": " ", "X-Claude-Code-Session-Id": "\t"}, ""},
		{"missing", nil, ""},
		{"no fuzzy match", map[string]string{"X-Other-SessionId": "other"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := make(http.Header)
			for name, value := range tc.headers {
				header.Set(name, value)
			}
			identity, ok := findAffinityIdentity(affinityIdentityInput{Header: header})
			require.Equal(t, tc.want != "", ok)
			require.Equal(t, tc.want, identity.Key)
			if ok {
				require.Equal(t, state.AffinityPartition{ByUser: true, ByToken: true, ByModel: true}, identity.Partition)
			}
			// 2. 会话存在时叠加到原来的用户、令牌和模型隔离键。
			require.Equal(t, affinity.Key{UserID: 1, TokenID: 13, RealModel: "deepseek-v4.1-flash", CacheKey: tc.want}, affinity.BuildKey(identity, 1, 13, "deepseek-v4.1-flash"))
		})
	}
}

func TestClaudeCodeAffinitySeparatesSessions(t *testing.T) {
	// 1. 同一用户、令牌和模型收到两个 Claude Code 会话。
	var keys []affinity.Key
	for _, session := range []string{"session-a", "session-b", "session-a"} {
		header := make(http.Header)
		header.Set(consts.HeaderClaudeCodeSessionID, session)
		identity, ok := findAffinityIdentity(affinityIdentityInput{Header: header})
		require.True(t, ok)
		keys = append(keys, affinity.BuildKey(identity, 1, 13, "deepseek-v4.1-flash"))
	}
	// 2. 不同会话不共用记录，同一会话复用同一个键。
	require.NotEqual(t, keys[0], keys[1])
	require.Equal(t, keys[0], keys[2])
}

func TestBuildClaudeCodeSessionAffinity(t *testing.T) {
	// 1. 从 Claude Messages HTTP 请求读取客户端会话。
	c := newGinCtxForTest(nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"deepseek-v4.1-flash","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`))
	c.Request.Header.Set(consts.HeaderClaudeCodeSessionID, "6cd106a5-5a32-4169-a500-ed829fc41791")
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 13})
	relay := newTestRelayCtx(t, c)
	require.NoError(t, Build(relay))
	// 2. 构建用于查询与续期的粘性键。
	key := affinity.BuildKey(relay.Input.AffinityIdentity, relay.Input.UserInfo.UserID, relay.Input.UserInfo.TokenID, relay.Input.Model)
	require.Equal(t, affinity.Key{UserID: 1, TokenID: 13, RealModel: "deepseek-v4.1-flash", CacheKey: "6cd106a5-5a32-4169-a500-ed829fc41791"}, key)
}
