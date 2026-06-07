package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/stretchr/testify/require"
)

func TestClassifyResolveErr(t *testing.T) {
	require.Equal(t, "master_unreachable", classifyResolveErr(ws.ErrConnClosed))
	// 当前分支内部超时统一走 ctx → DeadlineExceeded(无独立 ErrCallTimeout)。
	require.Equal(t, "control_timeout", classifyResolveErr(context.DeadlineExceeded))
	require.Equal(t, "client_canceled", classifyResolveErr(context.Canceled))
	require.Equal(t, "not_found", classifyResolveErr(entitycache.ErrNotFound))
	require.Equal(t, "unknown", classifyResolveErr(errors.New("boom")))
	require.Equal(t, "", classifyResolveErr(nil))
}
