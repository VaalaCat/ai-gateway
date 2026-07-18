package attemptproxy

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/ctxbuild"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

var ErrContextBuildRejected = errors.New("attempt proxy context rejected")

type ContextBuilder interface {
	Build(*gin.Context, attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error)
}

type contextBuilder struct {
	agent app.AgentApplication
}

func NewContextBuilder(agent app.AgentApplication) ContextBuilder {
	return &contextBuilder{agent: agent}
}

func (b *contextBuilder) Build(c *gin.Context, meta attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error) {
	if b == nil || isNilDependency(b.agent) {
		return nil, nil, fmt.Errorf("%w: agent unavailable", ErrContextBuildRejected)
	}
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil, nil, fmt.Errorf("%w: request unavailable", ErrContextBuildRejected)
	}
	if err := meta.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid metadata", ErrContextBuildRejected)
	}
	if !attemptwire.ProviderPathAllowed(c.Request.Method, meta.RequestPath) {
		return nil, nil, fmt.Errorf("%w: provider path not allowed", ErrContextBuildRejected)
	}

	// The internal endpoint owns authentication, but the shared relay builder and
	// all provider backends must observe the original provider path.
	requestURL := c.Request.URL
	endpointPath, endpointRawPath := requestURL.Path, requestURL.RawPath
	requestURL.Path = meta.RequestPath
	requestURL.RawPath = meta.RequestPath
	rctx := relay.NewContext(c, b.agent)
	release := func() {
		relay.CloseContext(rctx)
		requestURL.Path = endpointPath
		requestURL.RawPath = endpointRawPath
	}
	if err := ctxbuild.Build(rctx); err != nil {
		release()
		return nil, nil, err
	}
	rctx.Input.Model = meta.Attempt.RealModel
	return rctx, release, nil
}
