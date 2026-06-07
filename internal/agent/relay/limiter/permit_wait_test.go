package limiter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemStore_Waiters(t *testing.T) {
	s := NewMemStore()
	k := BucketKey{LimiterID: 1, Bucket: "x"}
	require.True(t, s.AddWaiter(k, 2))
	require.True(t, s.AddWaiter(k, 2))
	require.False(t, s.AddWaiter(k, 2)) // 队列满
	s.RemoveWaiter(k)
	require.True(t, s.AddWaiter(k, 2))  // 腾出一个
	require.False(t, s.AddWaiter(k, 0)) // max 0 → 不排
}

func TestMemStore_WaitC_SignalOnRelease(t *testing.T) {
	s := NewMemStore()
	k := BucketKey{LimiterID: 1, Bucket: "c"}
	rel, ok := s.TryConcurrency(k, 1)
	require.True(t, ok)
	select {
	case <-s.WaitC(k):
		t.Fatal("未释放时不应有信号")
	default:
	}
	rel()
	select {
	case <-s.WaitC(k): // 释放后应有信号
	default:
		t.Fatal("释放应触发 WaitC 信号")
	}
}
