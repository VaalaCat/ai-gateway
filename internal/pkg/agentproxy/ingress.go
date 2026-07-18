package agentproxy

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type ingressMetaContextKey struct{}

const (
	IngressKindDirect = "direct"
	IngressKindTunnel = "tunnel"
)

type IngressMeta struct {
	Kind          string
	SourceAgentID string
	RouteID       uint
	StreamID      tunnel.StreamID
	Hop           uint8
	Attempt       *attemptproxy.AttemptProxyMeta
}

func WithIngressMeta(ctx context.Context, meta IngressMeta) context.Context {
	return context.WithValue(ctx, ingressMetaContextKey{}, meta)
}

func IngressMetaFromContext(ctx context.Context) (IngressMeta, bool) {
	if ctx == nil {
		return IngressMeta{}, false
	}
	meta, ok := ctx.Value(ingressMetaContextKey{}).(IngressMeta)
	return meta, ok
}
