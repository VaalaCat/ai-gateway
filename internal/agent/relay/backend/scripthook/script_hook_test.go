package scripthook

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestScriptHeaderMap(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer x")
	h.Set("X-Foo", "bar")
	m := scriptHeaderMap(h)
	assert.Equal(t, "Bearer x", m["Authorization"])
	assert.Equal(t, "bar", m["X-Foo"])

	hMulti := http.Header{}
	hMulti.Add("X-Multi", "a")
	hMulti.Add("X-Multi", "b")
	mMulti := scriptHeaderMap(hMulti)
	assert.Equal(t, "a, b", mMulti["X-Multi"])
}

type stubProvider struct{ scripts []*script.Compiled }

func (s stubProvider) MatchScripts(_ uint, _ string) []*script.Compiled { return s.scripts }

type stubCache struct {
	app.AgentCache
	eng *script.Engine
}

func (s stubCache) ScriptEngine() *script.Engine { return s.eng }

type stubAgent struct {
	app.AgentApplication
	cache app.AgentCache
}

func (s stubAgent) GetCache() app.AgentCache              { return s.cache }
func (s stubAgent) GetRouteForwarder() app.RouteForwarder { return nil }
func (s stubAgent) GetLogger() *zap.Logger                { return zap.NewNop() }
func (s stubAgent) GetConfig() *config.AgentRuntimeConfig { return nil }
func (s stubAgent) GetTransportPool() app.TransportPool   { return nil }
func (s stubAgent) RelayTimeout() time.Duration           { return 0 }

func engineWithCode(t *testing.T, code string) *script.Engine {
	t.Helper()
	c, err := script.Compile(models.AdminScript{Name: "s", Code: code})
	require.NoError(t, err)
	return script.NewEngine(stubProvider{[]*script.Compiled{c}}, zap.NewNop(), time.Second)
}

func newUpstreamReq(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://upstream.test/v1", bytes.NewReader(body))
	require.NoError(t, err)
	return req
}

func ginCtxWithRecorder(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, err := http.NewRequest(http.MethodPost, "http://client.test/v1", nil)
	require.NoError(t, err)
	c.Request = req
	return c, w
}

func TestRunUpstreamScriptsReject(t *testing.T) {
	eng := engineWithCode(t, `function onUpstreamRequest(ctx){ ctx.reject(403, "no") }`)
	agent := stubAgent{cache: stubCache{eng: eng}}
	c, w := ginCtxWithRecorder(t)
	rctx := &state.RelayContext{Context: c}
	ch := &models.Channel{}
	body := []byte(`{"model":"gpt"}`)
	upstreamReq := newUpstreamReq(t, body)

	newBody, rejected, res := RunUpstreamScripts(agent, c, rctx, ch, codec.Protocol("openai"), "gpt", upstreamReq, body)

	assert.True(t, rejected)
	assert.True(t, res.Written)
	assert.Error(t, res.Err)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, body, newBody)
}

func TestRunUpstreamScriptsChanged(t *testing.T) {
	eng := engineWithCode(t, `function onUpstreamRequest(ctx){ ctx.body.added = 1 }`)
	agent := stubAgent{cache: stubCache{eng: eng}}
	c, _ := ginCtxWithRecorder(t)
	rctx := &state.RelayContext{Context: c}
	ch := &models.Channel{}
	body := []byte(`{"model":"gpt"}`)
	upstreamReq := newUpstreamReq(t, body)

	newBody, rejected, res := RunUpstreamScripts(agent, c, rctx, ch, codec.Protocol("openai"), "gpt", upstreamReq, body)

	assert.False(t, rejected)
	assert.NoError(t, res.Err)
	assert.Contains(t, string(newBody), "added")

	got, err := io.ReadAll(upstreamReq.Body)
	require.NoError(t, err)
	assert.Equal(t, newBody, got)
	assert.Equal(t, int64(len(newBody)), upstreamReq.ContentLength)

	require.NotNil(t, upstreamReq.GetBody)
	rc, err := upstreamReq.GetBody()
	require.NoError(t, err)
	got2, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, newBody, got2)
}

func TestRunUpstreamScriptsHeaderOps(t *testing.T) {
	eng := engineWithCode(t, `function onUpstreamRequest(ctx){ ctx.setHeader("X-Foo","bar"); ctx.removeHeader("X-Bar") }`)
	agent := stubAgent{cache: stubCache{eng: eng}}
	c, _ := ginCtxWithRecorder(t)
	rctx := &state.RelayContext{Context: c}
	ch := &models.Channel{}
	body := []byte(`{"model":"gpt"}`)
	upstreamReq := newUpstreamReq(t, body)
	upstreamReq.Header.Set("X-Bar", "remove-me")

	newBody, rejected, res := RunUpstreamScripts(agent, c, rctx, ch, codec.Protocol("openai"), "gpt", upstreamReq, body)

	assert.False(t, rejected)
	assert.NoError(t, res.Err)
	assert.Equal(t, "bar", upstreamReq.Header.Get("X-Foo"))
	assert.Empty(t, upstreamReq.Header.Get("X-Bar"))
	assert.Equal(t, body, newBody)
}
