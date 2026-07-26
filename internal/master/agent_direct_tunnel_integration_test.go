package master

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	masteragentauth "github.com/VaalaCat/ai-gateway/internal/master/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	mastertunnel "github.com/VaalaCat/ai-gateway/internal/master/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/models"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type directTunnelRuntime string

const (
	directTunnelStandalone directTunnelRuntime = "standalone"
	directTunnelEmbedded   directTunnelRuntime = "embedded"
)

type directTunnelFault string

const (
	directTunnelNoFault          directTunnelFault = ""
	directTunnelDropCommittedAck directTunnelFault = "drop_committed_ack"
	directTunnelDropResult       directTunnelFault = "drop_result"
	directTunnelMalformedResult  directTunnelFault = "malformed_result"
	directTunnelDuplicateResult  directTunnelFault = "duplicate_result"
	directTunnelResultAfterEnd   directTunnelFault = "result_after_end"
	directTunnelDropEnd          directTunnelFault = "drop_end"
	directTunnelDropResponse     directTunnelFault = "drop_response_after_dispatch"
)

type directTunnelFixtureOptions struct {
	sourceRuntime directTunnelRuntime
	targetRuntime directTunnelRuntime
	provider      http.Handler
	traceMode     models.TokenTraceMode
	fault         directTunnelFault
	invalidTicket bool
	relayTimeout  int
}

type directTunnelIntegrationFixture struct {
	*routedRelayFixture
	targetHTTP         *httptest.Server
	directProxy        *directTunnelFrameProxy
	closedTarget       string
	sourceEndpoint     string
	standaloneRuns     []*directTunnelStandaloneRun
	standaloneRunCalls atomic.Int32
}

type directTunnelStandaloneRun struct {
	done chan struct{}
	err  error
}

func TestDirectTunnelEndToEndResponseModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name          string
		sourceRuntime directTunnelRuntime
		targetRuntime directTunnelRuntime
		stream        bool
		contentType   string
		provider      http.Handler
		assertBody    func(*testing.T, string)
	}{
		{
			name: "json standalone to embedded", sourceRuntime: directTunnelStandalone,
			targetRuntime: directTunnelEmbedded, contentType: consts.ContentTypeJSON,
			provider: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
				_, _ = io.WriteString(w, `{"ok":true,"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
			}),
			assertBody: func(t *testing.T, body string) {
				require.JSONEq(t, `{"ok":true,"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, body)
			},
		},
		{
			name: "sse embedded to standalone", sourceRuntime: directTunnelEmbedded,
			targetRuntime: directTunnelStandalone, stream: true, contentType: consts.ContentTypeSSE,
			provider: newDirectTunnelTwoFlushSSE(),
			assertBody: func(t *testing.T, body string) {
				require.Contains(t, body, `"content":"one"`)
				require.Contains(t, body, `"prompt_tokens":2`)
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{
				sourceRuntime: test.sourceRuntime, targetRuntime: test.targetRuntime, provider: test.provider,
				traceMode: models.TokenTraceModeHeaders,
			})
			requestID := "req-response-" + strings.NewReplacer(" ", "-", "/", "-").Replace(test.name)

			var status int
			var header http.Header
			var body []byte
			var err error
			if provider, ok := test.provider.(*directTunnelSSEProvider); ok {
				t.Cleanup(provider.release)
				status, header, body, err = f.requestSSEAfterFirstEvent(t, requestID, provider)
			} else {
				status, header, body, err = f.requestResponse(t.Context(), test.stream, "", requestID)
			}

			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status, string(body))
			require.Contains(t, header.Get(consts.HeaderContentType), test.contentType)
			test.assertBody(t, string(body))
			require.EqualValues(t, 1, f.targetProviderCalls.Load())
			require.Zero(t, f.sourceProviderCalls.Load())
			require.EqualValues(t, 1, f.directProxy.connections.Load(), "request must use the Direct P2P socket")
			require.True(t, strings.HasPrefix(f.directProxy.URL(), "https://"), "Direct fixture must exercise WSS")
			require.EqualValues(t, 1, f.standaloneRunCalls.Load(), "standalone runtime must execute Server.Run")

			rawResult := f.directProxy.singleResult(t)
			require.LessOrEqual(t, len(rawResult), attemptwire.MaxResultWireBytes)
			result, err := attemptwire.DecodeResultJSON(rawResult)
			require.NoError(t, err)
			require.True(t, result.ProviderDispatched)
			require.Equal(t, 1, result.Dispatches)
			require.Equal(t, 2, result.PromptTokens)
			require.Equal(t, 1, result.CompletionTokens)
			require.NotNil(t, result.Trace)
			require.NotEmpty(t, result.Trace.InboundHeaders)
			require.NotEmpty(t, result.Trace.OutboundHeaders)
			require.NotEmpty(t, result.Trace.ResponseHeaders)
			require.Empty(t, result.Trace.InboundBody)
			require.Empty(t, result.Trace.OutboundBody)
			require.Empty(t, result.Trace.ResponseBody)
			require.Empty(t, result.Trace.ClientResponseBody)
			require.NotNil(t, result.Trace.FailureFallback)
			require.NotEmpty(t, result.Trace.FailureFallback.InboundBody)
			require.NotEmpty(t, result.Trace.FailureFallback.OutboundBody)
			require.NotEmpty(t, result.Trace.FailureFallback.ResponseBody)
			require.NotEmpty(t, result.Trace.FailureFallback.ClientResponseBody)
			requireDirectResponseFrameOrder(t, f.directProxy.responseFrameOrder())
			usage := f.usageByRequestID(requestID)
			require.Equal(t, "direct", usage.AgentRoutePath)
			require.Equal(t, result.PromptTokens, usage.PromptTokens)
			require.Equal(t, result.CompletionTokens, usage.CompletionTokens)
			require.True(t, usage.HasTrace)
			traceRow := f.traceByRequestID(requestID)
			require.NotEmpty(t, traceRow.InboundHeaders)
			require.NotEmpty(t, traceRow.OutboundHeaders)
			require.NotEmpty(t, traceRow.ResponseHeaders)
			require.Empty(t, traceRow.InboundBody)
			require.Empty(t, traceRow.OutboundBody)
			require.Empty(t, traceRow.ResponseBody)
			require.Empty(t, traceRow.ClientResponseBody)
			require.EqualValues(t, 1, f.sourceCalls.publisher.Load())
			require.Zero(t, f.targetCalls.publisher.Load())
			require.Equal(t, 1, f.source.DirectSessionPool.Snapshot().Active,
				"a successful Direct session must remain reusable")
		})
	}
}

func TestDirectTunnelHeaderOnlyFailureKeepsFullTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{
		sourceRuntime: directTunnelEmbedded,
		targetRuntime: directTunnelStandalone,
		traceMode:     models.TokenTraceModeHeaders,
		provider: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"direct failed","type":"server_error"}}`)
		}),
	})
	requestID := "req-direct-header-failure"

	status, _, body, err := f.requestResponse(t.Context(), false, "", requestID)

	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, status, string(body))
	require.EqualValues(t, 1, f.targetProviderCalls.Load())
	require.Zero(t, f.sourceProviderCalls.Load())
	rawResult := f.directProxy.singleResult(t)
	result, err := attemptwire.DecodeResultJSON(rawResult)
	require.NoError(t, err)
	require.Equal(t, attemptwire.ResultProviderFailed, result.Kind)
	require.True(t, result.ProviderDispatched)
	require.True(t, result.ProviderResultKnown)
	require.Equal(t, http.StatusInternalServerError, result.HTTPStatus)
	require.NotNil(t, result.Trace)
	require.Equal(t, string(trace.StageUpstreamStatus), result.Trace.ErrorStage)
	require.NotEmpty(t, result.Trace.InboundBody)
	require.NotEmpty(t, result.Trace.OutboundBody)
	require.NotEmpty(t, result.Trace.ResponseBody)
	require.NotEmpty(t, result.Trace.ClientResponseBody)
	require.Contains(t, result.Trace.ClientResponseBody, "direct failed", "source outer error replaced target trace")
	traceRow := f.traceByRequestID(requestID)
	require.NotEmpty(t, traceRow.InboundBody)
	require.NotEmpty(t, traceRow.OutboundBody)
	require.NotEmpty(t, traceRow.ResponseBody)
	require.NotEmpty(t, traceRow.ClientResponseBody)
	require.Contains(t, traceRow.ClientResponseBody, "direct failed", "persisted trace lost target response")
}

func TestDirectTunnelHeaderOnlyCodecTransformCarriesFailureFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{
		traceMode: models.TokenTraceModeHeaders,
		provider: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
			_, _ = io.WriteString(w, `{"id":"resp-direct","object":"response","status":"completed","model":"gpt-4o","output":[{"type":"message","id":"msg-direct","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
		}),
	})
	for _, server := range []*agent.Server{f.source, f.target} {
		channel := server.Store.GetChannel(7)
		require.NotNil(t, channel)
		channel.PassthroughEnabled = false
		channel.SupportedAPITypes = `["responses"]`
		channel.Endpoints = `{"responses":"/v1/responses"}`
		server.Store.SetChannel(channel)
		server.Store.RebuildModelIndex()
	}
	requestID := "req-direct-header-codec-fallback"
	requestBody := relayRequestBody(false)
	status, _, body, err := f.requestResponse(t.Context(), false, "", requestID)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, string(body))
	require.NotEmpty(t, body)
	rawResult := f.directProxy.singleResult(t)
	result, err := attemptwire.DecodeResultJSON(rawResult)
	require.NoError(t, err)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.NotNil(t, result.Trace)
	require.Empty(t, result.Trace.InboundBody, "normal target result must stay headers-only on the wire")
	require.Empty(t, result.Trace.OutboundBody)
	require.Empty(t, result.Trace.ResponseBody)
	require.Empty(t, result.Trace.ClientResponseBody)
	require.NotNil(t, result.Trace.FailureFallback)
	fallback := result.Trace.FailureFallback
	require.JSONEq(t, requestBody, fallback.InboundBody)
	require.Contains(t, fallback.OutboundBody, `"input"`)
	require.NotContains(t, fallback.OutboundBody, `"messages"`)
	require.Contains(t, fallback.OutboundBody, `"model":"gpt-4o"`)
	require.Contains(t, fallback.ResponseBody, `"object":"response"`)
	require.Contains(t, fallback.ClientResponseBody, `"object":"chat.completion"`)
	require.NotEqual(t, fallback.ResponseBody, fallback.ClientResponseBody, "codec transform was not exercised")

	traceRow := f.traceByRequestID(requestID)
	require.NotEmpty(t, traceRow.InboundHeaders)
	require.NotEmpty(t, traceRow.OutboundHeaders)
	require.NotEmpty(t, traceRow.ResponseHeaders)
	require.Empty(t, traceRow.InboundBody)
	require.Empty(t, traceRow.OutboundBody)
	require.Empty(t, traceRow.ResponseBody)
	require.Empty(t, traceRow.ClientResponseBody)
	usage := f.usageByRequestID(requestID)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 1, usage.CompletionTokens)
}

func TestTunnelResultFrameRelayPayloadIsOpaqueThroughMaster(t *testing.T) {
	f := newMasterResultFrameSocketFixture(t)
	source := f.connect("source")
	target := f.connect("target")
	id := wire.StreamID{81}
	f.open(source, target, id)

	headers, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, f.limits.MaxMetadataBytes)
	require.NoError(t, err)
	f.write(target, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: id, Sequence: 1, Payload: headers,
	})
	require.Equal(t, wire.FrameHeaders, f.read(source).Type)
	opaque := []byte(`{"kind":`)
	f.write(target, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: id, Sequence: 2, Payload: opaque,
	})
	forwarded := f.read(source)
	require.Equal(t, wire.FrameAttemptResult, forwarded.Type)
	require.Equal(t, opaque, forwarded.Payload)
	f.write(target, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: id, Sequence: 3,
	})
	require.Equal(t, wire.FrameEnd, f.read(source).Type)
}

func TestTunnelResultFrameMasterRejectsMissingAndOutOfOrder(t *testing.T) {
	for _, test := range []struct {
		name   string
		frames func(*masterResultFrameSocketFixture, wire.StreamID) []wire.Frame
	}{
		{
			name: "missing result",
			frames: func(f *masterResultFrameSocketFixture, id wire.StreamID) []wire.Frame {
				headers, err := wire.EncodeMetadata(
					wire.Headers{StatusCode: http.StatusOK}, f.limits.MaxMetadataBytes,
				)
				require.NoError(t, err)
				return []wire.Frame{
					{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: id, Sequence: 1, Payload: headers},
					{Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: id, Sequence: 2},
				}
			},
		},
		{
			name: "result before headers",
			frames: func(_ *masterResultFrameSocketFixture, id wire.StreamID) []wire.Frame {
				return []wire.Frame{{
					Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: id, Sequence: 1,
					Payload: []byte(`{"kind":"succeeded"}`),
				}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newMasterResultFrameSocketFixture(t)
			source := f.connect("source")
			target := f.connect("target")
			id := wire.StreamID{82}
			f.open(source, target, id)

			frames := test.frames(f, id)
			for index, frame := range frames {
				f.write(target, frame)
				if index < len(frames)-1 {
					require.Equal(t, frame.Type, f.read(source).Type)
				}
			}
			resetFrame := f.read(source)
			require.Equal(t, wire.FrameReset, resetFrame.Type)
			var reset wire.Reset
			require.NoError(t, wire.DecodeMetadata(resetFrame.Payload, &reset, f.limits.MaxMetadataBytes))
			require.Equal(t, wire.ErrorCodeSessionClosed, reset.Code)
			require.Equal(t, "peer", reset.Stage)
		})
	}
}

func TestTunnelResultFrameDirectSourceReplacesProtocolPollutedSession(t *testing.T) {
	for _, test := range []struct {
		name  string
		fault directTunnelFault
	}{
		{name: "malformed result", fault: directTunnelMalformedResult},
		{name: "duplicate result", fault: directTunnelDuplicateResult},
		{name: "result after end", fault: directTunnelResultAfterEnd},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{
				provider: relayProviderSuccess(nil), fault: test.fault,
			})
			requestID := "req-result-source-" + strings.ReplaceAll(test.name, " ", "-")

			status, _, _, err := f.requestResponse(t.Context(), false, "", requestID)

			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.EqualValues(t, 1, f.targetProviderCalls.Load())
			require.Zero(t, f.sourceProviderCalls.Load(), "a committed protocol failure must not replay")
			require.Equal(t, "direct", f.usageByRequestID(requestID).AgentRoutePath)
			reset := f.directProxy.singleSourceReset(t)
			require.Equal(t, wire.ErrorCodeRelayProtocol, reset.Code)
			require.True(t, reset.Committed)
			require.Eventually(t, func() bool {
				return f.source.DirectSessionPool.Snapshot().Active == 0
			}, 2*time.Second, 5*time.Millisecond, "a protocol-invalid Result must evict the whole Direct session")

			nextRequestID := requestID + "-replacement"
			status, _, _, err = f.requestResponse(t.Context(), false, "", nextRequestID)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Eventually(t, func() bool {
				return f.directProxy.connections.Load() == 2
			}, 2*time.Second, 5*time.Millisecond, "the next request must dial a replacement session")
			require.Zero(t, f.sourceProviderCalls.Load(), "committed protocol failures must never replay locally")
		})
	}
}

func TestDirectTunnelFallbackAndNoReplayMatrix(t *testing.T) {
	type matrixCase struct {
		name          string
		provider      http.Handler
		fault         directTunnelFault
		invalidJWT    bool
		sourceRuntime directTunnelRuntime
		targetRuntime directTunnelRuntime
		configure     func(*directTunnelIntegrationFixture)
		wantPath      string
		wantStatus    int
		wantTarget    int32
	}
	cases := []matrixCase{
		{
			name: "dial precommit falls back relay", provider: relayProviderSuccess(nil), wantPath: "relay", wantStatus: http.StatusOK,
			wantTarget: 1,
			configure:  func(f *directTunnelIntegrationFixture) { f.setDirectAddress(f.closedTarget) },
		},
		{
			name: "standalone dial precommit falls back relay", provider: relayProviderSuccess(nil),
			sourceRuntime: directTunnelStandalone, wantPath: "relay", wantStatus: http.StatusOK, wantTarget: 1,
			configure: func(f *directTunnelIntegrationFixture) { f.setDirectAddress(f.closedTarget) },
		},
		{
			name: "auth precommit falls back relay", provider: relayProviderSuccess(nil), invalidJWT: true,
			wantPath: "relay", wantStatus: http.StatusOK, wantTarget: 1,
		},
		{
			// The Target ACK precedes Source RequestData/End. Losing it therefore prevents
			// provider dispatch; the separate response-loss case covers post-dispatch no-replay.
			name: "commit ack lost never replays", provider: relayProviderSuccess(nil), fault: directTunnelDropCommittedAck,
			wantPath: "direct", wantStatus: http.StatusBadGateway,
		},
		{
			name: "response lost after dispatch never replays", provider: relayProviderSuccess(nil), fault: directTunnelDropResponse,
			wantPath: "direct", wantStatus: http.StatusBadGateway, wantTarget: 1,
		},
		{
			name: "partial sse never replays", provider: relayProviderInterruptedSSE(), wantPath: "direct", wantStatus: http.StatusOK,
			wantTarget: 1,
		},
		{
			name: "result without end never replays", provider: relayProviderSuccess(nil), fault: directTunnelDropEnd,
			wantPath: "direct", wantStatus: http.StatusOK, wantTarget: 1,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{
				sourceRuntime: test.sourceRuntime, targetRuntime: test.targetRuntime,
				provider: test.provider, fault: test.fault, invalidTicket: test.invalidJWT,
			})
			if test.configure != nil {
				test.configure(f)
			}
			requestID := "req-matrix-" + strings.NewReplacer(" ", "-", "/", "-").Replace(test.name)

			status, _, _, err := f.requestResponse(t.Context(), strings.Contains(test.name, "sse"), "", requestID)

			require.NoError(t, err)
			require.Equal(t, test.wantStatus, status)
			require.Equal(t, test.wantTarget, f.targetProviderCalls.Load())
			require.Zero(t, f.sourceProviderCalls.Load(), "committed Direct failures must not replay locally")
			usage := f.usageByRequestID(requestID)
			require.Equal(t, test.wantPath, usage.AgentRoutePath)
		})
	}
}

func TestDirectTunnelSoftAndHardFallback(t *testing.T) {
	for _, test := range []struct {
		name       string
		hardTarget string
		wantStatus int
		wantSource int32
	}{
		{name: "soft direct relay local", wantStatus: http.StatusOK, wantSource: 1},
		{name: "hard direct relay 502", hardTarget: "target", wantStatus: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{provider: relayProviderSuccess(nil)})
			f.setDirectAddress(f.closedTarget)
			f.setTargetPolicy(func(target *models.Agent) { target.RelayInboundEnabled = false })
			requestID := "req-ladder-" + strings.ReplaceAll(test.name, " ", "-")

			status, _, _, err := f.requestResponse(t.Context(), false, test.hardTarget, requestID)

			require.NoError(t, err)
			require.Equal(t, test.wantStatus, status)
			require.Equal(t, test.wantSource, f.sourceProviderCalls.Load())
			require.Zero(t, f.targetProviderCalls.Load())
		})
	}
}

func TestTransportPolicyEndToEnd(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*directTunnelIntegrationFixture)
		wantPath  string
	}{
		{
			name: "source direct outbound off uses relay", wantPath: "relay",
			configure: func(f *directTunnelIntegrationFixture) {
				f.setSourcePolicy(func(source *models.Agent) { source.DirectOutboundEnabled = false })
			},
		},
		{
			name: "target direct inbound off uses relay", wantPath: "relay",
			configure: func(f *directTunnelIntegrationFixture) {
				f.setTargetPolicy(func(target *models.Agent) { target.DirectInboundEnabled = false })
			},
		},
		{
			name: "source relay outbound off uses local", wantPath: "local",
			configure: func(f *directTunnelIntegrationFixture) {
				f.setDirectAddress(f.closedTarget)
				f.setSourcePolicy(func(source *models.Agent) { source.RelayOutboundEnabled = false })
			},
		},
		{
			name: "target relay inbound off uses local", wantPath: "local",
			configure: func(f *directTunnelIntegrationFixture) {
				f.setDirectAddress(f.closedTarget)
				f.setTargetPolicy(func(target *models.Agent) { target.RelayInboundEnabled = false })
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{provider: relayProviderSuccess(nil)})
			test.configure(f)
			requestID := "req-policy-" + strings.ReplaceAll(test.name, " ", "-")
			status, _, _, err := f.requestResponse(t.Context(), false, "", requestID)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, test.wantPath, f.usageByRequestID(requestID).AgentRoutePath)
		})
	}

	t.Run("hot update direct relay local", func(t *testing.T) {
		f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{provider: relayProviderSuccess(nil)})
		for index, want := range []string{"direct", "relay", "local"} {
			if index == 1 {
				f.setTargetPolicy(func(target *models.Agent) { target.DirectInboundEnabled = false })
			}
			if index == 2 {
				f.setTargetPolicy(func(target *models.Agent) { target.RelayInboundEnabled = false })
			}
			requestID := fmt.Sprintf("req-policy-hot-%d", index)
			status, _, _, err := f.requestResponse(t.Context(), false, "", requestID)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, want, f.usageByRequestID(requestID).AgentRoutePath)
		}
	})
}

func TestDirectTunnelLargeStreamingResponseIsBounded(t *testing.T) {
	const (
		chunkBytes = 64 << 10
		chunkCount = 1024
		totalBytes = int64(chunkBytes * chunkCount)
	)
	providerDone := make(chan struct{})
	firstConsumed := make(chan struct{})
	provider := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer close(providerDone)
		w.Header().Set(consts.HeaderContentType, "application/octet-stream")
		chunk := make([]byte, chunkBytes)
		for index := range chunkCount {
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
			if index == 0 {
				select {
				case <-firstConsumed:
				case <-time.After(2 * time.Second):
					return
				}
			}
		}
	})
	f := newDirectTunnelIntegrationFixture(t, directTunnelFixtureOptions{provider: provider, relayTimeout: 10})
	requestID := "req-direct-large-stream"
	req, err := f.newRequest(t.Context(), false, "", requestID)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	written := &directTunnelCountingWriter{first: firstConsumed}

	_, err = io.Copy(written, resp.Body)

	require.NoError(t, err)
	require.Equal(t, totalBytes, written.bytes.Load())
	require.Greater(t, written.writes.Load(), int64(1))
	require.EqualValues(t, 1, f.targetProviderCalls.Load())
	select {
	case <-providerDone:
	default:
		t.Fatal("first response bytes reached the source before provider completion, but provider did not finish")
	}
	rawResult := f.directProxy.singleResult(t)
	require.LessOrEqual(t, len(rawResult), attemptwire.MaxResultWireBytes)
	require.Equal(t, "direct", f.usageByRequestID(requestID).AgentRoutePath)
	bufferedLimit := f.limits.InitialStreamWindow + f.limits.MaxQueuedSessionBytes
	sourceSnapshot := f.source.DirectSessionPool.Snapshot()
	targetSnapshot := f.target.DirectTunnelIngress.Snapshot()
	require.Zero(t, sourceSnapshot.BufferedBytes)
	require.Zero(t, targetSnapshot.BufferedBytes)
	require.Positive(t, sourceSnapshot.MaxSessionPeakBufferedBytes)
	require.Positive(t, targetSnapshot.MaxSessionPeakBufferedBytes)
	require.LessOrEqual(t, sourceSnapshot.MaxSessionPeakBufferedBytes, bufferedLimit)
	require.LessOrEqual(t, targetSnapshot.MaxSessionPeakBufferedBytes, bufferedLimit)
}

type directTunnelCountingWriter struct {
	bytes  atomic.Int64
	writes atomic.Int64
	first  chan struct{}
	once   sync.Once
}

type masterResultFrameSocketFixture struct {
	t      *testing.T
	hub    *mastertunnel.Hub
	server *httptest.Server
	signer *masteragentauth.Signer
	limits wire.Limits
	wsURL  string
	conns  []*websocket.Conn
}

func newMasterResultFrameSocketFixture(t *testing.T) *masterResultFrameSocketFixture {
	t.Helper()
	signer := newAgentRouteSigner(t)
	limits := wire.Limits{
		MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10,
		MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4,
	}
	hub := mastertunnel.NewHub(mastertunnel.HubOptions{
		InstanceID: "master-route-fixture", Signer: signer, Agents: newRoutedRelayAgentLookup(),
		Limits: limits, Logger: zap.NewNop(),
	})
	router := gin.New()
	router.GET("/ws/agent-relay", hub.HandleWS)
	server := httptest.NewServer(router)
	f := &masterResultFrameSocketFixture{
		t: t, hub: hub, server: server, signer: signer, limits: limits,
		wsURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent-relay",
	}
	t.Cleanup(func() {
		for _, conn := range f.conns {
			_ = conn.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, hub.Close(ctx))
		server.Close()
	})
	return f
}

func (f *masterResultFrameSocketFixture) connect(agentID string) *websocket.Conn {
	f.t.Helper()
	ticket, _, err := f.signer.SignRelay(agentID, 0)
	require.NoError(f.t, err)
	conn, _, err := websocket.DefaultDialer.Dial(
		f.wsURL, http.Header{consts.HeaderAuthorization: {consts.BearerPrefix + string(ticket)}},
	)
	require.NoError(f.t, err)
	f.conns = append(f.conns, conn)
	require.NoError(f.t, conn.WriteJSON(wire.Hello{Nonce: "nonce-" + agentID}))
	var welcome wire.Welcome
	require.NoError(f.t, conn.ReadJSON(&welcome))
	require.NoError(f.t, conn.WriteJSON(wire.Authenticated{SessionGeneration: welcome.SessionGeneration}))
	var confirmed wire.Confirmed
	require.NoError(f.t, conn.ReadJSON(&confirmed))
	require.Equal(f.t, welcome.SessionGeneration, confirmed.SessionGeneration)
	require.Eventually(f.t, func() bool {
		snapshot, ok := f.hub.Snapshot(agentID)
		return ok && snapshot.AcceptingNewStreams
	}, time.Second, time.Millisecond)
	return conn
}

func (f *masterResultFrameSocketFixture) open(source, target *websocket.Conn, id wire.StreamID) {
	f.t.Helper()
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModePassthrough,
		},
		RequestPath: "/v1/chat/completions",
	}
	payload, err := wire.EncodeMetadata(wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath, TargetAgentID: "target",
		RemainingNanos: int64(time.Second), ResponseWindow: f.limits.InitialStreamWindow, Attempt: &meta,
	}, f.limits.MaxMetadataBytes)
	require.NoError(f.t, err)
	f.write(source, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload,
	})
	forwarded := f.read(target)
	require.Equal(f.t, wire.FrameOpen, forwarded.Type)
}

func (f *masterResultFrameSocketFixture) write(conn *websocket.Conn, frame wire.Frame) {
	f.t.Helper()
	payload, err := wire.Encode(frame, f.limits)
	require.NoError(f.t, err)
	require.NoError(f.t, conn.WriteMessage(websocket.BinaryMessage, payload))
}

func (f *masterResultFrameSocketFixture) read(conn *websocket.Conn) wire.Frame {
	f.t.Helper()
	require.NoError(f.t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	messageType, payload, err := conn.ReadMessage()
	require.NoError(f.t, err)
	require.NoError(f.t, conn.SetReadDeadline(time.Time{}))
	require.Equal(f.t, websocket.BinaryMessage, messageType)
	frame, err := wire.Decode(payload, f.limits)
	require.NoError(f.t, err)
	return frame
}

func (w *directTunnelCountingWriter) Write(payload []byte) (int, error) {
	w.bytes.Add(int64(len(payload)))
	w.writes.Add(1)
	w.once.Do(func() { close(w.first) })
	return len(payload), nil
}

type directTunnelSSEProvider struct {
	releaseSecond chan struct{}
	done          chan struct{}
	releaseOnce   sync.Once
}

func newDirectTunnelTwoFlushSSE() *directTunnelSSEProvider {
	return &directTunnelSSEProvider{releaseSecond: make(chan struct{}), done: make(chan struct{})}
}

func (p *directTunnelSSEProvider) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	defer close(p.done)
	w.Header().Set(consts.HeaderContentType, consts.ContentTypeSSE)
	_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-direct\",\"choices\":[{\"delta\":{\"content\":\"one\"},\"index\":0}]}\n\n")
	w.(http.Flusher).Flush()
	select {
	case <-p.releaseSecond:
	case <-request.Context().Done():
		return
	}
	_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-direct\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n")
	w.(http.Flusher).Flush()
}

func (p *directTunnelSSEProvider) release() {
	p.releaseOnce.Do(func() { close(p.releaseSecond) })
}

func newDirectTunnelIntegrationFixture(
	t *testing.T,
	opts directTunnelFixtureOptions,
) *directTunnelIntegrationFixture {
	t.Helper()
	if opts.sourceRuntime == "" {
		opts.sourceRuntime = directTunnelEmbedded
	}
	if opts.targetRuntime == "" {
		opts.targetRuntime = directTunnelEmbedded
	}
	if opts.provider == nil {
		opts.provider = relayProviderSuccess(nil)
	}
	if opts.relayTimeout <= 0 {
		opts.relayTimeout = 2
	}
	base := &routedRelayFixture{t: t, db: newAgentRouteUsageDB(t)}
	f := &directTunnelIntegrationFixture{routedRelayFixture: base}
	t.Cleanup(f.close)
	base.signer = newAgentRouteSigner(t)
	base.limits = wire.Limits{
		MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10,
		MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4,
	}
	base.lookup = newRoutedRelayAgentLookup()
	base.hub = mastertunnel.NewHub(mastertunnel.HubOptions{
		InstanceID: "master-route-fixture", Signer: base.signer, Agents: base.lookup,
		Limits: base.limits, Logger: zap.NewNop(),
	})
	hubRouter := gin.New()
	hubRouter.GET("/ws/agent-relay", base.hub.HandleWS)
	base.hubServer = httptest.NewServer(trackAgentRouteActive(&base.hubHTTPActive, hubRouter))
	relayURI := "ws" + strings.TrimPrefix(base.hubServer.URL, "http") + "/ws/agent-relay"

	base.sourceProvider = base.newProvider(&base.sourceProviderCalls, relayProviderSuccess(nil))
	base.targetProvider = base.newProvider(&base.targetProviderCalls, opts.provider)
	forwardAuth := func() agentproxy.ForwardAuthSnapshot {
		return agentproxy.ForwardAuthSnapshot{
			Capabilities: []string{protocol.AgentCapabilityForwardV1},
			SigningKeys:  []pkgagentauth.PublicKey{base.signer.PublicKey()},
		}
	}
	var targetStandaloneEndpoint string
	base.target, targetStandaloneEndpoint = f.newRuntimeAgent(
		"target",
		base.targetProvider.URL,
		opts.targetRuntime,
		opts.traceMode,
		agent.StandaloneOptions{ForwardAuthSnapshotLoader: forwardAuth},
		opts.relayTimeout,
	)
	targetRouter := gin.New()
	if opts.targetRuntime == directTunnelStandalone {
		targetRouter = base.target.Router
	}
	if opts.targetRuntime == directTunnelEmbedded {
		base.installManager(base.target, "target", base.signer, base.limits, base.target.NewTunnelTargetHandler(targetRouter))
		base.mountRoutedTargetRoutes(targetRouter)
	}
	targetEndpoint := targetStandaloneEndpoint
	if opts.targetRuntime == directTunnelEmbedded {
		f.targetHTTP = httptest.NewServer(trackAgentRouteActive(&base.httpActive, targetRouter))
		targetEndpoint = f.targetHTTP.URL
	}
	f.directProxy = newDirectTunnelFrameProxy(t, targetEndpoint, base.limits, opts.fault)
	sourceSigner := base.signer
	if opts.invalidTicket {
		sourceSigner = newAgentRouteSigner(t)
	}
	var sourceStandaloneEndpoint string
	base.source, sourceStandaloneEndpoint = f.newRuntimeAgent(
		"source",
		base.sourceProvider.URL,
		opts.sourceRuntime,
		opts.traceMode,
		agent.StandaloneOptions{
			DirectSessionDialer: agenttunnel.NewDirectDialer(agenttunnel.DirectDialerOptions{
				TLSClientConfig: f.directProxy.TLSClientConfig(), Logger: zap.NewNop(),
			}),
			ForwardCredentialReader: forwardCredentialReaderFunc(func() (agentauthcache.ForwardCredential, error) {
				ticket, expiresAt, err := sourceSigner.SignForward("source")
				return agentauthcache.ForwardCredential{Ticket: ticket, ExpiresAt: expiresAt}, err
			}),
		},
		opts.relayTimeout,
	)
	sourceRouter := gin.New()
	if opts.sourceRuntime == directTunnelStandalone {
		sourceRouter = base.source.Router
	}
	if opts.sourceRuntime == directTunnelEmbedded {
		base.installManager(base.source, "source", base.signer, base.limits, base.source.NewTunnelTargetHandler(sourceRouter))
		f.replaceSourceDirectPool(opts.invalidTicket, f.directProxy.TLSClientConfig())
		base.mountRoutedSourceRoutes(sourceRouter)
	}
	f.closedTarget = closedDirectTunnelAddress(t)
	f.configureAgentCaches(f.directProxy.URL())
	base.source.Store.RouteIndex.Put(&models.AgentRoute{
		ID: 77, SourceType: "token", SourceID: 1, Model: "gpt-4o", AgentID: "target", Priority: 100,
	})
	if opts.targetRuntime == directTunnelStandalone {
		f.startStandaloneServer(base.target, targetEndpoint, "target")
	}
	if opts.sourceRuntime == directTunnelEmbedded {
		base.sourceHTTP = httptest.NewServer(trackAgentRouteActive(&base.httpActive, sourceRouter))
		f.sourceEndpoint = base.sourceHTTP.URL
	} else {
		f.sourceEndpoint = sourceStandaloneEndpoint
		f.startStandaloneServer(base.source, f.sourceEndpoint, "source")
	}
	f.startRuntimeManagers(relayURI, opts.sourceRuntime, opts.targetRuntime)
	return f
}

func (f *directTunnelIntegrationFixture) newRuntimeAgent(
	id, providerURL string,
	runtime directTunnelRuntime,
	traceMode models.TokenTraceMode,
	standalone agent.StandaloneOptions,
	relayTimeout int,
) (*agent.Server, string) {
	f.t.Helper()
	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			MasterURL: "http://127.0.0.1:1", CredentialsFile: filepath.Join(f.t.TempDir(), id+"-direct.json"),
			PreferredAddrTag: "local",
		},
		Runtime: config.RuntimeConfig{RelayTimeout: relayTimeout}, Relay: config.RelayConfig{Timeout: relayTimeout},
	}
	var (
		srv                *agent.Server
		err                error
		standaloneEndpoint string
	)
	if runtime == directTunnelStandalone {
		standalone.TunnelManagerBuilder = func(srv *agent.Server) *agenttunnel.Manager {
			return f.buildManager(
				id,
				f.signer,
				f.limits,
				srv.NewTunnelTargetHandler(srv.Router),
			)
		}
	}
	switch runtime {
	case directTunnelStandalone:
		cfg.Agent.Listen, standaloneEndpoint = reserveDirectTunnelHTTPAddress(f.t)
		require.NoError(f.t, os.WriteFile(
			cfg.Agent.CredentialsFile,
			[]byte(fmt.Sprintf(`{"agent_id":%q,"secret":"secret"}`, id)),
			0o600,
		))
		srv, err = agent.New(cfg, zap.NewNop(), standalone)
	case directTunnelEmbedded:
		srv, err = agent.NewEmbedded(
			cfg, zap.NewNop(), &enrollment.Credentials{AgentID: id, Secret: "secret"},
		)
	default:
		f.t.Fatalf("unknown direct tunnel runtime %q", runtime)
	}
	require.NoError(f.t, err)
	srv.Store.SetAgent(newTransportEnabledAgent(models.Agent{AgentID: id, Status: consts.StatusEnabled}))
	srv.Store.SetToken(&models.Token{
		ID: 1, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1,
		TraceEnabled: true, TraceMode: traceMode,
	})
	srv.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: providerURL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "provider-key", Models: "gpt-4o",
	})
	srv.Store.RebuildModelIndex()
	srv.Store.LoadSettings([]models.Setting{
		{Key: "retry_max_channels", Value: "1"},
		{Key: "max_retries_per_channel", Value: "0"},
		{Key: "breaker_enabled", Value: "0"},
	})
	settler := billing.NewSettler(&agentRouteDBProvider{db: f.db}, nil, zap.NewNop())
	calls := &f.sourceCalls
	if id == "target" {
		calls = &f.targetCalls
	}
	_, err = events.SubscribeUsageCompleted(srv.Bus, func(ctx context.Context, entry protocol.UsageLogEntry) error {
		calls.publisher.Add(1)
		settler.Settle(ctx, id, []protocol.UsageLogEntry{entry})
		return nil
	})
	require.NoError(f.t, err)
	return srv, standaloneEndpoint
}

func (f *directTunnelIntegrationFixture) startStandaloneServer(
	srv *agent.Server,
	endpoint, name string,
) {
	f.t.Helper()
	run := &directTunnelStandaloneRun{done: make(chan struct{})}
	f.standaloneRuns = append(f.standaloneRuns, run)
	go func() {
		defer close(run.done)
		run.err = srv.Run()
	}()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	require.Eventually(f.t, func() bool {
		select {
		case <-run.done:
			return false
		default:
		}
		response, err := client.Get(endpoint + "/ping")
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 2*time.Second, 5*time.Millisecond, "%s standalone Server.Run did not become ready", name)
	f.standaloneRunCalls.Add(1)
}

func (f *directTunnelIntegrationFixture) startRuntimeManagers(
	relayURI string,
	sourceRuntime, targetRuntime directTunnelRuntime,
) {
	f.t.Helper()
	servers := []struct {
		srv     *agent.Server
		runtime directTunnelRuntime
	}{
		{srv: f.source, runtime: sourceRuntime},
		{srv: f.target, runtime: targetRuntime},
	}
	for _, server := range servers {
		if server.srv.TunnelManager == nil {
			continue
		}
		if server.runtime == directTunnelEmbedded {
			runCtx, cancel := context.WithCancel(context.WithoutCancel(f.t.Context()))
			done := make(chan error, 1)
			go func(manager *agenttunnel.Manager) { done <- manager.Run(runCtx) }(server.srv.TunnelManager)
			f.managerRuns = append(f.managerRuns, routedRelayManagerRun{cancel: cancel, done: done})
		}
		server.srv.TunnelManager.Apply(agenttunnel.Desired{
			Mode: "custom", ConfiguredURI: relayURI, EffectiveURI: relayURI,
		})
	}
	for _, server := range servers {
		if server.srv.TunnelManager == nil {
			continue
		}
		require.Eventually(f.t, func() bool {
			return server.srv.TunnelManager.Snapshot().AcceptingNewStreams
		}, 2*time.Second, 5*time.Millisecond)
	}
}

func (f *directTunnelIntegrationFixture) replaceSourceDirectPool(invalidTicket bool, tlsConfig *tls.Config) {
	f.t.Helper()
	if previous := f.source.DirectSessionPool; previous != nil {
		ctx, cancel := agentRouteCleanupContext(f.t)
		require.NoError(f.t, previous.Close(ctx))
		cancel()
		requireAgentRouteDone(f.t, previous.Done(), "constructor direct session pool")
	}
	signer := f.signer
	if invalidTicket {
		signer = newAgentRouteSigner(f.t)
	}
	f.source.DirectSessionPool = agenttunnel.NewDirectSessionPool(agenttunnel.DirectSessionPoolOptions{
		SourceAgentID: "source",
		DirectOutboundEnabled: func() bool {
			source := f.source.Store.GetAgent("source")
			return source != nil && source.DirectOutboundEnabled
		},
		Dialer: agenttunnel.NewDirectDialer(agenttunnel.DirectDialerOptions{
			TLSClientConfig: tlsConfig, Logger: zap.NewNop(),
		}),
		Credentials: forwardCredentialReaderFunc(func() (agentauthcache.ForwardCredential, error) {
			ticket, expiresAt, err := signer.SignForward("source")
			return agentauthcache.ForwardCredential{Ticket: ticket, ExpiresAt: expiresAt}, err
		}),
		Limits:       func() wire.Limits { return f.limits },
		MaxSessions:  func() int { return 4 },
		IdleTimeout:  func() time.Duration { return time.Minute },
		DrainTimeout: func() time.Duration { return time.Second },
		Logger:       zap.NewNop(),
	})
}

func (f *directTunnelIntegrationFixture) configureAgentCaches(directURL string) {
	f.t.Helper()
	for _, server := range []*agent.Server{f.source, f.target} {
		server.Store.SetAgent(newTransportEnabledAgent(models.Agent{AgentID: "source", Status: consts.StatusEnabled}))
		server.Store.SetAgent(newTransportEnabledAgent(models.Agent{AgentID: "target", Status: consts.StatusEnabled}))
		server.Store.SetAgentCapabilities("source", []string{
			protocol.AgentCapabilityTunnelV2, protocol.AgentCapabilityForwardV1,
			protocol.AgentCapabilityDirectTunnelV1,
		})
		server.Store.SetAgentCapabilities("target", []string{
			protocol.AgentCapabilityTunnelV2, protocol.AgentCapabilityForwardV1,
			protocol.AgentCapabilityDirectTunnelV1,
		})
	}
	f.setDirectAddress(directURL)
}

func (f *directTunnelIntegrationFixture) setDirectAddress(address string) {
	f.t.Helper()
	target := f.source.Store.GetAgent("target")
	require.NotNil(f.t, target)
	target.HTTPAddresses = fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, address)
	f.source.Store.SetAgent(target)
}

func (f *directTunnelIntegrationFixture) setSourcePolicy(update func(*models.Agent)) {
	f.t.Helper()
	for _, server := range []*agent.Server{f.source, f.target} {
		source := server.Store.GetAgent("source")
		require.NotNil(f.t, source)
		update(source)
		server.Store.SetAgent(source)
	}
}

func (f *directTunnelIntegrationFixture) setTargetPolicy(update func(*models.Agent)) {
	f.t.Helper()
	for _, server := range []*agent.Server{f.source, f.target} {
		target := server.Store.GetAgent("target")
		require.NotNil(f.t, target)
		update(target)
		server.Store.SetAgent(target)
	}
}

func (f *directTunnelIntegrationFixture) newRequest(
	ctx context.Context,
	stream bool,
	hardTarget, requestID string,
) (*http.Request, error) {
	body := relayRequestBody(stream)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, f.sourceEndpoint+"/v1/chat/completions", strings.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"route-token")
	req.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	req.Header.Set(consts.HeaderXRequestID, requestID)
	if hardTarget != "" {
		req.Header.Set(consts.HeaderXAgentID, hardTarget)
	}
	return req, nil
}

func (f *directTunnelIntegrationFixture) requestResponse(
	ctx context.Context,
	stream bool,
	hardTarget, requestID string,
) (int, http.Header, []byte, error) {
	req, err := f.newRequest(ctx, stream, hardTarget, requestID)
	if err != nil {
		return 0, nil, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Clone(), body, err
}

func (f *directTunnelIntegrationFixture) requestSSEAfterFirstEvent(
	t *testing.T,
	requestID string,
	provider *directTunnelSSEProvider,
) (int, http.Header, []byte, error) {
	t.Helper()
	req, err := f.newRequest(t.Context(), true, "", requestID)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	firstLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	separator, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, firstLine, `"content":"one"`)
	require.Equal(t, "\n", separator)
	select {
	case <-provider.done:
		t.Fatal("provider completed before the client observed the first flushed event")
	default:
	}

	provider.release()
	rest, err := io.ReadAll(reader)
	if err != nil {
		return resp.StatusCode, resp.Header.Clone(), nil, err
	}
	select {
	case <-provider.done:
	case <-t.Context().Done():
		return resp.StatusCode, resp.Header.Clone(), nil, context.Cause(t.Context())
	}
	return resp.StatusCode, resp.Header.Clone(), append([]byte(firstLine+separator), rest...), nil
}

func (f *directTunnelIntegrationFixture) usageByRequestID(requestID string) models.UsageLog {
	f.t.Helper()
	var usage models.UsageLog
	require.Eventually(f.t, func() bool {
		return f.db.Where("request_id = ?", requestID).First(&usage).Error == nil
	}, 2*time.Second, 5*time.Millisecond)
	return usage
}

func (f *directTunnelIntegrationFixture) traceByRequestID(requestID string) models.UsageLogTrace {
	f.t.Helper()
	var traceRow models.UsageLogTrace
	require.Eventually(f.t, func() bool {
		return f.db.Where("request_id = ?", requestID).First(&traceRow).Error == nil
	}, 2*time.Second, 5*time.Millisecond)
	return traceRow
}

func (f *directTunnelIntegrationFixture) close() {
	f.closeOnce.Do(func() {
		if err := f.closeResources(); err != nil {
			f.t.Errorf("direct integration cleanup: %v", err)
		}
	})
}

func (f *directTunnelIntegrationFixture) closeResources() error {
	var cleanupErrs []error
	if f.sourceHTTP != nil {
		f.sourceHTTP.Close()
	}
	if f.directProxy != nil {
		cleanupErrs = append(cleanupErrs, f.directProxy.Close())
	}
	if f.targetHTTP != nil {
		f.targetHTTP.Close()
	}
	cleanupErrs = append(cleanupErrs, f.closeForwarder())
	if f.sourceTransport != nil {
		f.sourceTransport.CloseIdleConnections()
	}
	if f.targetTransport != nil {
		f.targetTransport.CloseIdleConnections()
	}
	cleanupErrs = append(cleanupErrs,
		shutdownDirectTunnelServer(f.source, "direct integration source"),
		shutdownDirectTunnelServer(f.target, "direct integration target"),
		f.waitStandaloneRuns(),
		f.stopEmbeddedManagerRuns(),
		f.closeHubResources(),
	)
	cleanupErrs = append(cleanupErrs, f.closeProviderResources())
	return errors.Join(cleanupErrs...)
}

func (f *directTunnelIntegrationFixture) closeForwarder() error {
	if f.sourceDirect == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := f.sourceDirect.Close(ctx)
	cancel()
	return errors.Join(err, waitDirectTunnelCleanup(
		"direct integration forwarder Done", func() bool {
			select {
			case <-f.sourceDirect.Done():
				return true
			default:
				return false
			}
		},
	))
}

func shutdownDirectTunnelServer(srv *agent.Server, name string) error {
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := srv.Shutdown(ctx)
	cancel()
	if err != nil {
		err = fmt.Errorf("%s shutdown: %w", name, err)
	}
	doneErr := waitDirectTunnelCleanup(name+" Done", func() bool {
		select {
		case <-srv.Done():
			return true
		default:
			return false
		}
	})
	var resourceErr error
	if counts := srv.ResourceCountsForTest(); counts != (app.ResourceCounts{}) {
		resourceErr = fmt.Errorf("%s retained resources: %+v", name, counts)
	}
	return errors.Join(err, doneErr, resourceErr)
}

func (f *directTunnelIntegrationFixture) waitStandaloneRuns() error {
	var cleanupErrs []error
	for _, run := range f.standaloneRuns {
		select {
		case <-run.done:
			if run.err == nil {
				cleanupErrs = append(cleanupErrs, errors.New("standalone Server.Run stopped without an error"))
			}
		case <-time.After(2 * time.Second):
			cleanupErrs = append(cleanupErrs, errors.New("standalone Server.Run did not stop"))
		}
	}
	return errors.Join(cleanupErrs...)
}

func (f *directTunnelIntegrationFixture) stopEmbeddedManagerRuns() error {
	var cleanupErrs []error
	for _, run := range f.managerRuns {
		run.cancel()
		select {
		case <-run.done:
		case <-time.After(2 * time.Second):
			cleanupErrs = append(cleanupErrs, errors.New("embedded relay manager Run did not stop"))
		}
	}
	return errors.Join(cleanupErrs...)
}

func (f *directTunnelIntegrationFixture) closeHubResources() error {
	var cleanupErrs []error
	if f.hub != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cleanupErrs = append(cleanupErrs, f.hub.Close(ctx))
		cancel()
		cleanupErrs = append(cleanupErrs, waitDirectTunnelActiveZero(&f.hubHTTPActive, "relay hub HTTP"))
	}
	if f.hubServer != nil {
		f.hubServer.Close()
	}
	return errors.Join(cleanupErrs...)
}

func (f *directTunnelIntegrationFixture) closeProviderResources() error {
	providerErr := waitDirectTunnelActiveZero(&f.providerActive, "direct integration provider")
	if f.sourceProvider != nil {
		f.sourceProvider.Close()
	}
	if f.targetProvider != nil {
		f.targetProvider.Close()
	}
	return errors.Join(providerErr, waitDirectTunnelActiveZero(&f.httpActive, "direct integration HTTP"))
}

func waitDirectTunnelActiveZero(active *atomic.Int32, name string) error {
	return waitDirectTunnelCleanup(name, func() bool { return active.Load() == 0 })
}

func waitDirectTunnelCleanup(name string, ready func() bool) error {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return nil
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("%s did not settle", name)
		case <-ticker.C:
		}
	}
}

func closedDirectTunnelAddress(t *testing.T) string {
	_, endpoint := reserveDirectTunnelHTTPAddress(t)
	return endpoint
}

func reserveDirectTunnelHTTPAddress(t *testing.T) (listen, endpoint string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address, "http://" + address
}

type directTunnelFrameProxy struct {
	t            *testing.T
	limits       wire.Limits
	fault        directTunnelFault
	target       string
	server       *httptest.Server
	connections  atomic.Int32
	active       atomic.Int32
	mu           sync.Mutex
	result       [][]byte
	frameOrder   []wire.Type
	sourceFrames []wire.Frame
	conns        map[*websocket.Conn]struct{}
	closeOnce    sync.Once
}

func newDirectTunnelFrameProxy(
	t *testing.T,
	target string,
	limits wire.Limits,
	fault directTunnelFault,
) *directTunnelFrameProxy {
	t.Helper()
	p := &directTunnelFrameProxy{
		t: t, limits: limits, fault: fault, target: target,
		conns: make(map[*websocket.Conn]struct{}),
	}
	p.server = httptest.NewUnstartedServer(http.HandlerFunc(p.handle))
	p.server.StartTLS()
	return p
}

func (p *directTunnelFrameProxy) URL() string { return p.server.URL }

func (p *directTunnelFrameProxy) TLSClientConfig() *tls.Config {
	return p.server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
}

func (p *directTunnelFrameProxy) handle(w http.ResponseWriter, request *http.Request) {
	upstreamURL := "ws" + strings.TrimPrefix(p.target, "http") + request.URL.RequestURI()
	upstreamHeader := http.Header{}
	upstreamHeader[consts.HeaderAuthorization] = request.Header.Values(consts.HeaderAuthorization)
	upstream, response, err := websocket.DefaultDialer.Dial(upstreamURL, upstreamHeader)
	if err != nil {
		if response != nil {
			http.Error(w, http.StatusText(response.StatusCode), response.StatusCode)
			_ = response.Body.Close()
			return
		}
		http.Error(w, "direct upstream unavailable", http.StatusBadGateway)
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true }, EnableCompression: false,
	}
	downstream, err := upgrader.Upgrade(w, request, nil)
	if err != nil {
		_ = upstream.Close()
		return
	}
	p.connections.Add(1)
	p.active.Add(1)
	p.track(upstream, true)
	p.track(downstream, true)
	defer func() {
		p.track(upstream, false)
		p.track(downstream, false)
		_ = upstream.Close()
		_ = downstream.Close()
		p.active.Add(-1)
	}()

	proxyCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	responseConsumed := make(chan struct{})
	var responseConsumedOnce sync.Once
	var workers conc.WaitGroup
	workers.Go(func() {
		p.copyFrames(proxyCtx, cancel, downstream, upstream, false, responseConsumed, &responseConsumedOnce)
	})
	workers.Go(func() {
		p.copyFrames(proxyCtx, cancel, upstream, downstream, true, responseConsumed, &responseConsumedOnce)
	})
	workers.Wait()
}

func (p *directTunnelFrameProxy) copyFrames(
	ctx context.Context,
	cancel context.CancelFunc,
	source, destination *websocket.Conn,
	targetToSource bool,
	responseConsumed chan struct{},
	responseConsumedOnce *sync.Once,
) {
	defer cancel()
	var heldResult []byte
	for context.Cause(ctx) == nil {
		messageType, payload, err := source.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			if destination.WriteMessage(messageType, payload) != nil {
				return
			}
			continue
		}
		frame, err := wire.Decode(payload, p.limits)
		if err != nil {
			return
		}
		p.recordFrame(frame, targetToSource)
		if !targetToSource {
			if frame.Type == wire.FrameWindowUpdate {
				responseConsumedOnce.Do(func() { close(responseConsumed) })
			}
			if destination.WriteMessage(messageType, payload) != nil {
				return
			}
			continue
		}
		switch {
		case p.fault == directTunnelDropCommittedAck && frame.Type == wire.FrameCommitted:
			p.closePair(source, destination)
			return
		case p.fault == directTunnelDropResponse && frame.Type == wire.FrameHeaders:
			p.closePair(source, destination)
			return
		case p.fault == directTunnelDropResult && frame.Type == wire.FrameAttemptResult:
			continue
		case p.fault == directTunnelMalformedResult && frame.Type == wire.FrameAttemptResult:
			frame.Payload = []byte(`{"kind":`)
			malformed, encodeErr := wire.Encode(frame, p.limits)
			if encodeErr != nil || destination.WriteMessage(websocket.BinaryMessage, malformed) != nil {
				return
			}
			continue
		case p.fault == directTunnelDuplicateResult && frame.Type == wire.FrameAttemptResult:
			if destination.WriteMessage(messageType, payload) != nil {
				return
			}
			frame.Sequence++
			duplicate, encodeErr := wire.Encode(frame, p.limits)
			if encodeErr != nil || destination.WriteMessage(websocket.BinaryMessage, duplicate) != nil {
				return
			}
			continue
		case p.fault == directTunnelResultAfterEnd && frame.Type == wire.FrameAttemptResult:
			heldResult = append([]byte(nil), payload...)
			continue
		case p.fault == directTunnelResultAfterEnd && frame.Type == wire.FrameEnd:
			if destination.WriteMessage(messageType, payload) != nil {
				return
			}
			if len(heldResult) > 0 {
				if destination.WriteMessage(websocket.BinaryMessage, heldResult) != nil {
					return
				}
				heldResult = nil
			}
			continue
		case p.fault == directTunnelDropEnd && frame.Type == wire.FrameEnd:
			select {
			case <-responseConsumed:
			case <-ctx.Done():
				return
			}
			p.closePair(source, destination)
			return
		}
		if destination.WriteMessage(messageType, payload) != nil {
			return
		}
	}
}
func (*directTunnelFrameProxy) closePair(first, second *websocket.Conn) {
	_ = first.Close()
	_ = second.Close()
}

func (p *directTunnelFrameProxy) recordFrame(frame wire.Frame, targetToSource bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !targetToSource {
		frame.Payload = append([]byte(nil), frame.Payload...)
		p.sourceFrames = append(p.sourceFrames, frame)
		return
	}
	p.frameOrder = append(p.frameOrder, frame.Type)
	if frame.Type == wire.FrameAttemptResult {
		p.result = append(p.result, append([]byte(nil), frame.Payload...))
	}
}

func (p *directTunnelFrameProxy) singleSourceReset(t *testing.T) wire.Reset {
	t.Helper()
	var resetFrames []wire.Frame
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		resetFrames = resetFrames[:0]
		for _, frame := range p.sourceFrames {
			if frame.Type == wire.FrameReset {
				resetFrames = append(resetFrames, frame)
			}
		}
		return len(resetFrames) == 1
	}, 2*time.Second, 5*time.Millisecond, "Source must reject the protocol-invalid stream with one Reset")
	require.Never(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		count := 0
		for _, frame := range p.sourceFrames {
			if frame.Type == wire.FrameReset {
				count++
			}
		}
		return count != 1
	}, 50*time.Millisecond, 5*time.Millisecond, "Source must not emit a duplicate Reset")
	var reset wire.Reset
	require.NoError(t, wire.DecodeMetadata(resetFrames[0].Payload, &reset, p.limits.MaxMetadataBytes))
	return reset
}

func (p *directTunnelFrameProxy) singleResult(t *testing.T) []byte {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Len(t, p.result, 1)
	return append([]byte(nil), p.result[0]...)
}

func (p *directTunnelFrameProxy) responseFrameOrder() []wire.Type {
	p.mu.Lock()
	defer p.mu.Unlock()
	order := make([]wire.Type, 0, len(p.frameOrder))
	for _, frameType := range p.frameOrder {
		switch frameType {
		case wire.FrameHeaders, wire.FrameResponseData, wire.FrameAttemptResult, wire.FrameEnd:
			order = append(order, frameType)
		}
	}
	return order
}

func requireDirectResponseFrameOrder(t *testing.T, order []wire.Type) {
	t.Helper()
	require.GreaterOrEqual(t, len(order), 4)
	require.Equal(t, wire.FrameHeaders, order[0])
	require.Equal(t, wire.FrameAttemptResult, order[len(order)-2])
	require.Equal(t, wire.FrameEnd, order[len(order)-1])
	for _, frameType := range order[1 : len(order)-2] {
		require.Equal(t, wire.FrameResponseData, frameType)
	}
}

func (p *directTunnelFrameProxy) track(conn *websocket.Conn, add bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if add {
		p.conns[conn] = struct{}{}
		return
	}
	delete(p.conns, conn)
}

func (p *directTunnelFrameProxy) Close() error {
	var closeErr error
	p.closeOnce.Do(func() {
		p.mu.Lock()
		connections := make([]*websocket.Conn, 0, len(p.conns))
		for conn := range p.conns {
			connections = append(connections, conn)
		}
		p.mu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		p.server.Close()
		closeErr = waitDirectTunnelCleanup("direct frame proxy", func() bool { return p.active.Load() == 0 })
	})
	return closeErr
}
