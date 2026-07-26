package dataflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/gin-gonic/gin"

	agentapp "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func TestStepUpstreamScript_NoEngineNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gw/v1/chat/completions", strings.NewReader("{}"))

	s := &StepUpstreamScript{
		agent:   nil, // engineOf(nil) == nil → noop
		c:       c,
		rctx:    &state.RelayContext{Context: c, Input: state.RelayInput{}},
		attempt: state.Attempt{Channel: &models.Channel{}},
		proto:   codec.ProtocolOpenAIChat,
	}
	p := &Pass{
		Original: &codec.Request{Model: "real-model"},
		HTTPReq:  httptest.NewRequest(http.MethodPost, "https://up/v1/chat/completions", strings.NewReader(`{"a":1}`)),
		Body:     []byte(`{"a":1}`),
	}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Aborted {
		t.Fatal("should not abort without engine")
	}
	if string(p.Body) != `{"a":1}` {
		t.Fatalf("body changed: %s", p.Body)
	}
}

func TestStepUpstreamScriptScopesAndReject(t *testing.T) {
	tests := []struct {
		name        string
		scope       models.ScriptScope
		code        string
		user        *app.UserInfo
		wantBody    string
		wantAborted bool
		attempt     state.Attempt
	}{
		{
			name:     "private scope rewrites body",
			scope:    models.ScriptScope{PrivateChannelIDs: []uint{7}},
			code:     `function onUpstreamRequest(ctx){ ctx.body.scope = "private" }`,
			wantBody: `{"a":1,"scope":"private"}`,
			attempt:  state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 3}}, RealModel: "provider-model", Source: state.SourcePrivate, SourceID: 7},
		},
		{
			name:     "admin request does not match private scope",
			scope:    models.ScriptScope{PrivateChannelIDs: []uint{7}},
			code:     `function onUpstreamRequest(ctx){ ctx.body.scope = "wrong" }`,
			wantBody: `{"a":1}`,
			attempt:  state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 3}}, RealModel: "provider-model", Source: state.SourceAdmin, SourceID: 7},
		},
		{
			name:        "private scope reject aborts pass",
			scope:       models.ScriptScope{PrivateChannelIDs: []uint{7}},
			code:        `function onUpstreamRequest(ctx){ ctx.reject(403, "blocked") }`,
			wantBody:    `{"a":1}`,
			wantAborted: true,
			attempt:     state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 3}}, RealModel: "provider-model", Source: state.SourcePrivate, SourceID: 7},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := cache.NewStore(nil, config.AgentCacheConfig{})
			store.LoadScripts([]models.AdminScript{{
				ID:      1,
				Name:    "upstream-scope",
				Enabled: true,
				Code:    tt.code,
				Scope:   datatypes.NewJSONType(tt.scope),
			}})
			agent := agentapp.NewDefaultAgentApplication(store, nil, zap.NewNop(), &config.AgentRuntimeConfig{}, nil)
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "http://gateway/v1/chat/completions", nil)
			rctx := &state.RelayContext{Context: c, Input: state.RelayInput{UserInfo: tt.user}}
			s := &StepUpstreamScript{
				agent:   agent,
				c:       c,
				rctx:    rctx,
				attempt: tt.attempt,
				proto:   codec.ProtocolOpenAIChat,
			}
			p := &Pass{
				Original: &codec.Request{Model: "provider-model"},
				HTTPReq:  httptest.NewRequest(http.MethodPost, "https://up/v1/chat/completions", strings.NewReader(`{"a":1}`)),
				Body:     []byte(`{"a":1}`),
			}

			if err := s.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if p.Aborted != tt.wantAborted {
				t.Fatalf("aborted = %v, want %v", p.Aborted, tt.wantAborted)
			}
			if got := string(p.Body); got != tt.wantBody {
				t.Fatalf("body = %s, want %s", got, tt.wantBody)
			}
		})
	}
}
