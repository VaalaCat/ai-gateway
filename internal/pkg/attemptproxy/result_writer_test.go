package attemptproxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type attemptResultWriterStub struct{}

func (attemptResultWriterStub) WriteAttemptResult(AttemptProxyResult) error { return nil }

func TestAttemptResultWriterContextRoundTrip(t *testing.T) {
	want := attemptResultWriterStub{}
	ctx := WithAttemptResultWriter(t.Context(), want)
	got, ok := AttemptResultWriterFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, want, got)
}

func TestAttemptResultWriterContextRejectsNilInputs(t *testing.T) {
	require.NotPanics(t, func() {
		ctx := WithAttemptResultWriter(nil, nil)
		_, ok := AttemptResultWriterFromContext(ctx)
		require.False(t, ok)
	})
	_, ok := AttemptResultWriterFromContext(context.Background())
	require.False(t, ok)
	var typedNil *typedNilAttemptResultWriter
	ctx := WithAttemptResultWriter(t.Context(), typedNil)
	_, ok = AttemptResultWriterFromContext(ctx)
	require.False(t, ok)
}

type typedNilAttemptResultWriter struct{}

func (*typedNilAttemptResultWriter) WriteAttemptResult(AttemptProxyResult) error { return nil }
