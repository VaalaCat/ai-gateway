package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
)

// MasterServer Master 控制面服务器
// 负责管理 API、WebSocket Hub、计费结算等控制面功能
type MasterServer interface {
	Run() error
	Shutdown(ctx context.Context) error
	InitAdminUser(username, password string) error
}

// AgentServer Agent 数据面服务器
// 负责请求中继、配置缓存同步、指标上报等数据面功能
type AgentServer interface {
	Run() error
	RunBackground(ctx context.Context)
	Shutdown(ctx context.Context) error
	MountRoutes(router *gin.Engine)
	GetRelayLink() RelayLink
}

type ProbePolicy = tunnel.ProbePolicy

const (
	ProbeRespectBusinessPolicy = tunnel.ProbeRespectBusinessPolicy
	ProbeBypassBusinessPolicy  = tunnel.ProbeBypassBusinessPolicy
)

type AttemptStreamRequest struct {
	TargetAgentID string
	RouteID       uint
	RequestID     string
	Method        string
	Path          string
	Header        http.Header
	BodyLength    int64
	Remaining     time.Duration
	Hop           uint8
	Attempt       attemptwire.AttemptProxyMeta
}

type ProbeStreamRequest struct {
	TargetAgentID string
	RequestID     string
	Remaining     time.Duration
	Policy        ProbePolicy
}

type AttemptStream interface {
	Commit(ctx context.Context) error
	Upload(ctx context.Context, src io.Reader) error
	CopyAttemptResponse(ctx context.Context, dst http.ResponseWriter) (attemptwire.AttemptProxyResult, error)
	CommitState() tunnel.CommitState
	Cancel(cause error)
	Close() error
}

type ProbeStream interface {
	Commit(ctx context.Context) error
	Upload(ctx context.Context, src io.Reader) error
	CopyResponse(ctx context.Context, dst http.ResponseWriter) error
	CommitState() tunnel.CommitState
	Cancel(cause error)
	Close() error
}

type APIOpen struct {
	TargetAgentID string
	RouteID       uint
	RequestID     string
	Method        string
	Path          string
	Header        http.Header
	BodyLength    int64
	Remaining     time.Duration
	Hop           uint8
	API           apiattempt.APIAttemptMeta
}

type APIResponseEventKind uint8

const (
	APIResponseHeaders APIResponseEventKind = iota + 1
	APIResponseData
	APIResponseEnd
	APIResponseResult
)

type APIResponseEvent struct {
	Kind     APIResponseEventKind
	Headers  *tunnel.Headers
	Data     []byte
	Trailers *tunnel.Trailers
	Result   *apiattempt.APIExecutionResult
}

type HTTPAPIProtocolError struct {
	Stage string
	Code  string
}

func (e *HTTPAPIProtocolError) Error() string {
	if e == nil {
		return "generic HTTP API stream protocol error"
	}
	return fmt.Sprintf("generic HTTP API stream protocol error: stage=%s code=%s", e.Stage, e.Code)
}

// HTTPAPIStream is the Source-side consumer contract for one Generic API
// attempt. Open completes the Tunnel Ready/Commit handshake before request data
// may be sent. Receive preserves Headers/Data/End/Result wire order.
type HTTPAPIStream interface {
	Open(context.Context, APIOpen) error
	SendRequestData(context.Context, []byte) error
	EndRequest(context.Context, tunnel.Trailers) error
	Receive(context.Context) (APIResponseEvent, error)
	Cancel(cause error)
	Close() error
}

type AttemptStreamOpener interface {
	OpenAttemptStream(context.Context, AttemptStreamRequest) (AttemptStream, error)
}

type ProbeStreamOpener interface {
	OpenProbeStream(context.Context, ProbeStreamRequest) (ProbeStream, error)
}

type HTTPAPIStreamOpener interface {
	OpenHTTPAPIStream(context.Context, APIOpen) (HTTPAPIStream, error)
}

type WebSocketOpen struct {
	TargetAgentID string
	RouteID       uint
	RequestID     string
	Path          string
	Header        http.Header
	Remaining     time.Duration
	Hop           uint8
	API           apiattempt.APIAttemptMeta
}

type WebSocketAccepted = tunnel.WebSocketAccepted
type WebSocketRejection = tunnel.WebSocketRejection
type WebSocketEvent = tunnel.WebSocketEvent
type WebSocketEventKind = tunnel.WebSocketEventKind

const (
	WebSocketMessageStartEvent  = tunnel.WebSocketMessageStartEvent
	WebSocketMessageDataEvent   = tunnel.WebSocketMessageDataEvent
	WebSocketMessageEndEvent    = tunnel.WebSocketMessageEndEvent
	WebSocketPingEvent          = tunnel.WebSocketPingEvent
	WebSocketPongEvent          = tunnel.WebSocketPongEvent
	WebSocketCloseEvent         = tunnel.WebSocketCloseEvent
	WebSocketTextMessage        = tunnel.WebSocketTextMessage
	WebSocketBinaryMessage      = tunnel.WebSocketBinaryMessage
	WebSocketCloseProtocolError = tunnel.WebSocketCloseProtocolError
)

// WebSocketAPIStream is the source-side transport contract for one Generic
// API WebSocket. Events preserve message boundaries; data events may be
// fragmented at the negotiated tunnel data limit.
type WebSocketAPIStream interface {
	Open(context.Context, WebSocketOpen) (WebSocketAccepted, error)
	ProviderAcceptance() WebSocketAccepted
	SendEvent(context.Context, WebSocketEvent) error
	ReceiveEvent(context.Context) (WebSocketEvent, error)
	ReceiveResult(context.Context) (apiattempt.APIExecutionResult, error)
	Close() error
}

type WebSocketExecutionError struct {
	Result apiattempt.APIExecutionResult
	Err    error
}

func (e *WebSocketExecutionError) Error() string {
	if e == nil || e.Err == nil {
		return "generic WebSocket API execution failed"
	}
	return e.Err.Error()
}

func (e *WebSocketExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type WebSocketAPIStreamOpener interface {
	OpenWebSocketAPIStream(context.Context, WebSocketOpen) (WebSocketAPIStream, error)
}

type RelayLink interface {
	AttemptStreamOpener
	ProbeStreamOpener
	HTTPAPIStreamOpener
}
