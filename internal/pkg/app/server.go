package app

import (
	"context"
	"io"
	"net/http"
	"time"

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

type AttemptStreamOpener interface {
	OpenAttemptStream(context.Context, AttemptStreamRequest) (AttemptStream, error)
}

type ProbeStreamOpener interface {
	OpenProbeStream(context.Context, ProbeStreamRequest) (ProbeStream, error)
}

type RelayLink interface {
	AttemptStreamOpener
	ProbeStreamOpener
}
