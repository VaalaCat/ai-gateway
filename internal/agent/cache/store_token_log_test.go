package cache

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"

	"github.com/stretchr/testify/require"
)

// newStoreWithFailingToken 构造一个 tokenStore.primary loader 必失败的 Store（供 token 降级日志测试注入错误）。
func newStoreWithFailingToken(t *testing.T, loadErr error) *Store {
	t.Helper()
	s := NewStore(nil, config.AgentCacheConfig{})
	ts := &tokenStore{}
	primary, err := entitycache.NewLRUCache(entitycache.Config[string, *models.Token]{
		Capacity:    100,
		NegativeTTL: 30 * time.Second,
		Loader: entitycache.LoaderFunc[string, *models.Token](func(_ context.Context, _ string) (*models.Token, error) {
			return nil, loadErr
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts.primary = primary
	s.tokenStore = ts
	return s
}

func TestGetToken_LogsMasterUnreachableVsNotFound(t *testing.T) {
	// case A: 连不上 → Warn reason=master_unreachable，返回 nil（fail-closed）
	coreA, logsA := observer.New(zapcore.DebugLevel)
	sA := newStoreWithFailingToken(t, ws.ErrConnClosed)
	sA.SetLogger(zap.New(coreA))
	require.Nil(t, sA.GetToken(context.Background(), "sk-x"))
	warns := logsA.FilterMessage("token auth resolve failed").FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1)
	require.Equal(t, "master_unreachable", warns[0].ContextMap()["reason"])

	// case B: 真无效 key → 不打 Warn（not_found 不污染告警），返回 nil
	coreB, logsB := observer.New(zapcore.DebugLevel)
	sB := newStoreWithFailingToken(t, entitycache.ErrNotFound)
	sB.SetLogger(zap.New(coreB))
	require.Nil(t, sB.GetToken(context.Background(), "sk-x"))
	require.Empty(t, logsB.FilterMessage("token auth resolve failed").FilterLevelExact(zapcore.WarnLevel).All())
}

func TestGetTokenByID_LogsMasterUnreachableVsNotFound(t *testing.T) {
	// GetTokenByID 与 GetToken 共享同一 token 数据，降级处理需一致。
	// 预写 byID 反向索引，使 GetByID 走 primary.Get（命中失败 loader）。
	coreA, logsA := observer.New(zapcore.DebugLevel)
	sA := newStoreWithFailingToken(t, ws.ErrConnClosed)
	sA.tokenStore.byID.Store(uint(7), "sk-x")
	sA.SetLogger(zap.New(coreA))
	require.Nil(t, sA.GetTokenByID(context.Background(), 7))
	warns := logsA.FilterMessage("token auth resolve failed").FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, 1)
	require.Equal(t, "master_unreachable", warns[0].ContextMap()["reason"])

	coreB, logsB := observer.New(zapcore.DebugLevel)
	sB := newStoreWithFailingToken(t, entitycache.ErrNotFound)
	sB.tokenStore.byID.Store(uint(7), "sk-x")
	sB.SetLogger(zap.New(coreB))
	require.Nil(t, sB.GetTokenByID(context.Background(), 7))
	require.Empty(t, logsB.FilterMessage("token auth resolve failed").FilterLevelExact(zapcore.WarnLevel).All())
}
