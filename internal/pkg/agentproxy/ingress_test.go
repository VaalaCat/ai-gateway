package agentproxy

import (
	"context"
	"testing"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestIngressMetaRoundTripAndIsolation(t *testing.T) {
	meta := IngressMeta{
		Kind: IngressKindTunnel, SourceAgentID: "source-a", RouteID: 42,
		StreamID: wire.StreamID{1}, Hop: 1,
	}
	ctx := WithIngressMeta(t.Context(), meta)

	got, ok := IngressMetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, meta, got)
	_, parentHasMeta := IngressMetaFromContext(t.Context())
	require.False(t, parentHasMeta)
}

func TestIngressKindValuesRemainTransportSpecific(t *testing.T) {
	require.Equal(t, "direct", IngressKindDirect)
	require.Equal(t, "tunnel", IngressKindTunnel)
	require.NotEqual(t, IngressKindDirect, IngressKindTunnel)
}

func TestIngressMetaRejectsNilContext(t *testing.T) {
	require.Panics(t, func() { WithIngressMeta(nil, IngressMeta{}) })
	got, ok := IngressMetaFromContext(nil)
	require.False(t, ok)
	require.Zero(t, got)
}

func TestIngressMetaCannotBeForgedWithStringContextKey(t *testing.T) {
	ctx := context.WithValue(t.Context(), "agentproxy.ingress", IngressMeta{Kind: "forged"})
	_, ok := IngressMetaFromContext(ctx)
	require.False(t, ok)
}
