package api

import (
	"context"
	"time"
)

const postCommitPublishTimeout = 10 * time.Second

// NewPostCommitPublishContext keeps request-scoped values while giving
// authoritative post-commit queries and events their own bounded lifetime.
func NewPostCommitPublishContext(requestCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(requestCtx), postCommitPublishTimeout)
}
