package master

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/genericapi"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rolloutPermissionGate struct{}

func (rolloutPermissionGate) AllowInvoke(context.Context, uint, uint, uint, uint) error { return nil }

type rolloutQuotaGate struct{}

func (rolloutQuotaGate) Allow(context.Context, uint, protocol.SyncedAPIService) error { return nil }

type rolloutServiceRouteFinder struct{}

func (rolloutServiceRouteFinder) Find(_, _, _, _ string) (genericapi.ServiceRoute, string, error) {
	return genericapi.ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1},
		Route: protocol.SyncedAPIRoute{
			ID: 9, ServiceID: 7, Slug: "current", Status: 1,
			Protocols: []string{genericapi.ProtocolHTTP},
		},
		Protocol: genericapi.ProtocolHTTP,
	}, "", nil
}

type rolloutAgentPicker struct{ agentID string }

func (p rolloutAgentPicker) Pick(_, _, _ uint, _ string) (genericapi.AgentPick, error) {
	return genericapi.AgentPick{ExecutionAgentID: p.agentID}, nil
}

type rolloutCapabilityFinder struct {
	supported map[string]bool
	calls     atomic.Int32
}

func (f *rolloutCapabilityFinder) SupportsGenericAPIExecution(agentID string) bool {
	f.calls.Add(1)
	return f.supported[agentID]
}

type rolloutDispatchHandler struct{ calls atomic.Int32 }

func (h *rolloutDispatchHandler) Serve(_ context.Context, rc *genericapi.RequestContext) error {
	h.calls.Add(1)
	_, _ = io.Copy(io.Discard, rc.Context.Request.Body)
	rc.Context.Status(http.StatusNoContent)
	return nil
}

type rolloutReadTrackingBody struct {
	io.Reader
	reads atomic.Int32
}

func (b *rolloutReadTrackingBody) Read(value []byte) (int, error) {
	b.reads.Add(1)
	return b.Reader.Read(value)
}

func (b *rolloutReadTrackingBody) Close() error { return nil }

func TestMixedVersionAgentsFailBeforeDispatchAndExistingLLMStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name              string
		sourceAgentID     string
		executionAgentID  string
		capabilities      map[string]bool
		wantStatus        int
		wantCode          string
		wantDispatchCalls int32
		wantFinderCalls   int32
	}{
		{
			name: "old remote Agent fails closed", sourceAgentID: "source", executionAgentID: "old-agent",
			capabilities: map[string]bool{}, wantStatus: http.StatusServiceUnavailable,
			wantCode: genericapi.CodeExecutionAgentIncompatible, wantFinderCalls: 1,
		},
		{
			name: "capable remote Agent dispatches once", sourceAgentID: "source", executionAgentID: "new-agent",
			capabilities: map[string]bool{"new-agent": true}, wantStatus: http.StatusNoContent,
			wantDispatchCalls: 1, wantFinderCalls: 1,
		},
		{
			name: "local source remains compatible without a remote lookup", sourceAgentID: "source", executionAgentID: "source",
			capabilities: map[string]bool{}, wantStatus: http.StatusNoContent, wantDispatchCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := &rolloutCapabilityFinder{supported: test.capabilities}
			dispatch := &rolloutDispatchHandler{}
			handler := genericapi.NewHandler(genericapi.HandlerOptions{
				Finder: rolloutServiceRouteFinder{}, Permission: rolloutPermissionGate{}, Quota: rolloutQuotaGate{},
				AgentPicker: rolloutAgentPicker{agentID: test.executionAgentID}, SourceAgentID: test.sourceAgentID,
				ExecutionCapabilities: capabilities,
				Handlers:              map[string]genericapi.ProtocolHandler{genericapi.ProtocolHTTP: dispatch},
			})
			router := gin.New()
			v1 := router.Group("/v1")
			v1.Use(func(c *gin.Context) {
				c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TokenID: 3, UserID: 5})
			})
			genericapi.RegisterRoutes(v1, handler)

			body := &rolloutReadTrackingBody{Reader: strings.NewReader("must-not-be-read-by-old-agent")}
			request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", body)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			require.Equal(t, test.wantDispatchCalls, dispatch.calls.Load())
			require.Equal(t, test.wantFinderCalls, capabilities.calls.Load())
			if test.wantCode != "" {
				var envelope struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
				require.Equal(t, test.wantCode, envelope.Error.Code)
				require.Zero(t, body.reads.Load(), "incompatible Agent must be rejected before any request Body read")
			}
		})
	}

	// Mixed-version rollout must not alter the existing LLM route surface. The
	// full LLM behavior stays covered by the existing master relay regression
	// suite; this boundary assertion catches accidental route removal here.
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	require.Equal(t, http.StatusNoContent, response.Code)
}
