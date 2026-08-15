package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type postCommitContextKey struct{}

// Break caught: using the request context after a successful commit lets a
// disconnected client cancel the authoritative query and event publication.
func TestNewPostCommitPublishContextDetachesCancellationAndKeepsRequestValues(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.WithValue(context.Background(), postCommitContextKey{}, "request-value"))
	cancelRequest()

	publishCtx, cancelPublish := NewPostCommitPublishContext(requestCtx)
	defer cancelPublish()

	require.NoError(t, publishCtx.Err())
	require.Equal(t, "request-value", publishCtx.Value(postCommitContextKey{}))
	deadline, ok := publishCtx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(10*time.Second), deadline, time.Second)
}
