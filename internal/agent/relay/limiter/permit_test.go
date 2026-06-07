package limiter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemStore_Concurrency(t *testing.T) {
	s := NewMemStore()
	k := BucketKey{LimiterID: 1, Bucket: "c:admin:45"}
	r1, ok := s.TryConcurrency(k, 2)
	require.True(t, ok)
	_, ok = s.TryConcurrency(k, 2)
	require.True(t, ok)
	_, ok = s.TryConcurrency(k, 2) // 满
	require.False(t, ok)
	r1()                             // 释放一个
	r3, ok := s.TryConcurrency(k, 2) // 又能占
	require.True(t, ok)
	r3()

	_, ok = s.TryConcurrency(BucketKey{LimiterID: 2, Bucket: "x"}, 0) // cap<=0 → 拒
	require.False(t, ok)

	// 不同 bucket 独立
	_, ok = s.TryConcurrency(BucketKey{LimiterID: 1, Bucket: "c:admin:99"}, 1)
	require.True(t, ok)
}

func TestMemStore_OccupiedCounter(t *testing.T) {
	s := NewMemStore()
	k := BucketKey{LimiterID: 1, Bucket: "shared"}
	rel1, ok1 := s.TryConcurrency(k, 2)
	rel2, ok2 := s.TryConcurrency(k, 2)
	_, ok3 := s.TryConcurrency(k, 2)
	if !ok1 || !ok2 || ok3 {
		t.Fatalf("cap=2 should allow 2 deny 3: %v %v %v", ok1, ok2, ok3)
	}
	if got := s.occupiedOf(k); got != 2 {
		t.Fatalf("occupied=%d want 2", got)
	}
	rel1()
	rel2()
	if got := s.occupiedOf(k); got != 0 {
		t.Fatalf("occupied=%d want 0 after release", got)
	}
}

func TestMemStore_RateFixedWindow(t *testing.T) {
	s := NewMemStore()
	clock := int64(1000)
	s.now = func() int64 { return clock } // 注入时钟
	k := BucketKey{LimiterID: 1, Bucket: "u:1"}

	require.True(t, s.TryRate(k, 2, 1000))
	require.True(t, s.TryRate(k, 2, 1000))
	require.False(t, s.TryRate(k, 2, 1000)) // 窗口内超额

	clock += 1000                          // 跨到下一窗口
	require.True(t, s.TryRate(k, 2, 1000)) // 自然恢复

	require.False(t, s.TryRate(k, 0, 1000)) // cap<=0 → 拒
	require.False(t, s.TryRate(k, 2, 0))    // window<=0 → 拒
}

func TestMemStore_SnapshotBuckets(t *testing.T) {
	s := NewMemStore()
	kc := BucketKey{LimiterID: 1, Bucket: "u:7"}
	s.TryConcurrency(kc, 5) // occupied=1
	kr := BucketKey{LimiterID: 2, Bucket: "shared"}
	s.TryRate(kr, 10, 60000) // count=1

	got := map[BucketKey]BucketUsage{}
	for _, b := range s.SnapshotBuckets() {
		got[b.Key] = b
	}
	if got[kc].Occupied != 1 || got[kc].IsRate {
		t.Fatalf("conc bucket wrong: %+v", got[kc])
	}
	if got[kr].Occupied != 1 || !got[kr].IsRate || got[kr].WindowStartMs == 0 {
		t.Fatalf("rate bucket wrong: %+v", got[kr])
	}
}
