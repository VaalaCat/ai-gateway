package attemptproxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttemptProxyMetaRoundTrip(t *testing.T) {
	meta := AttemptProxyMeta{
		Attempt: BoundAttempt{
			Channel:   ChannelRef{Source: SourcePrivate, ID: 9},
			RealModel: "gpt-4o-2024-08-06",
			Mode:      ModePassthrough,
		},
		RequestPath: "/v1/chat/completions",
	}
	raw, err := EncodeMeta(meta)
	require.NoError(t, err)
	got, err := DecodeMeta(raw)
	require.NoError(t, err)
	require.Equal(t, meta, got)
}

func TestAttemptProxyMetaRoundTripsAdminAndPrivate(t *testing.T) {
	tests := []AttemptProxyMeta{
		{
			Attempt: BoundAttempt{
				Channel: ChannelRef{Source: SourceAdmin, ID: 1}, RealModel: "gpt-4o", Mode: ModeNative,
			},
			RequestPath: "/v1/responses",
		},
		{
			Attempt: BoundAttempt{
				Channel: ChannelRef{Source: SourcePrivate, ID: 2}, RealModel: "claude-3-7-sonnet", Mode: ModeLegacy,
			},
			RequestPath: "/v1/messages",
		},
	}
	for _, meta := range tests {
		raw, err := EncodeMeta(meta)
		require.NoError(t, err)
		got, err := DecodeMeta(raw)
		require.NoError(t, err)
		require.Equal(t, meta, got)
	}
}

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

func TestDecodeMetaAllowsUnknownJSONFields(t *testing.T) {
	raw := `{
		"attempt": {
			"channel": {"source": "admin", "id": 7, "future_channel": true},
			"real_model": "gpt-4o",
			"mode": "native",
			"future_attempt": "value"
		},
		"request_path": "/v1/responses",
		"future_meta": {"version": 2}
	}`
	got, err := DecodeMeta(raw)
	require.NoError(t, err)
	require.Equal(t, AttemptProxyMeta{
		Attempt: BoundAttempt{
			Channel: ChannelRef{Source: SourceAdmin, ID: 7}, RealModel: "gpt-4o", Mode: ModeNative,
		},
		RequestPath: "/v1/responses",
	}, got)
}

func TestDecodeMetaValidatesContract(t *testing.T) {
	_, err := DecodeMeta(`{"attempt":{"channel":{"source":"admin","id":0},"real_model":"gpt-4o","mode":"native"},"request_path":"/v1/responses"}`)
	require.ErrorIs(t, err, ErrInvalidContract)
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
