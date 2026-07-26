package attemptproxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundAttemptValidateRejectsZeroAndUnknownValues(t *testing.T) {
	tests := []BoundAttempt{
		{Channel: ChannelRef{Source: SourceAdmin}, RealModel: "gpt-4o", Mode: ModeNative},
		{Channel: ChannelRef{ID: 1}, RealModel: "gpt-4o", Mode: ModeNative},
		{Channel: ChannelRef{Source: "unknown", ID: 1}, RealModel: "gpt-4o", Mode: ModeNative},
		{Channel: ChannelRef{Source: SourceAdmin, ID: 1}, Mode: ModeNative},
		{Channel: ChannelRef{Source: SourceAdmin, ID: 1}, RealModel: "gpt-4o", Mode: "unknown"},
	}
	for _, attempt := range tests {
		require.Error(t, attempt.Validate())
	}
}

func TestAttemptProxyMetaValidateRejectsEmptyRequestPath(t *testing.T) {
	meta := AttemptProxyMeta{
		Attempt: BoundAttempt{
			Channel: ChannelRef{Source: SourceAdmin, ID: 1}, RealModel: "gpt-4o", Mode: ModeNative,
		},
	}
	require.ErrorIs(t, meta.Validate(), ErrInvalidContract)
}

func TestAttemptProxyMetaContextRoundTripAndIsolation(t *testing.T) {
	meta := AttemptProxyMeta{
		Attempt: BoundAttempt{
			Channel: ChannelRef{Source: SourceAdmin, ID: 7}, RealModel: "gpt-4o", Mode: ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	ctx := WithMeta(t.Context(), meta)

	got, ok := MetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, meta, got)
	_, parentHasMeta := MetaFromContext(t.Context())
	require.False(t, parentHasMeta)
	got, ok = MetaFromContext(nil)
	require.False(t, ok)
	require.Zero(t, got)
}

func TestAttemptProxyMetaContextKeyIsPrivate(t *testing.T) {
	ctx := context.WithValue(t.Context(), "attemptproxy.meta", AttemptProxyMeta{})
	_, ok := MetaFromContext(ctx)
	require.False(t, ok)
}
