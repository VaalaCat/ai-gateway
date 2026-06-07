package rpc

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestHandleLimiterUsage_JoinsMetadata(t *testing.T) {
	store := limiter.NewMemStore()
	store.TryConcurrency(limiter.BucketKey{LimiterID: 7, Bucket: "u:1"}, 5)
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{{ID: 7, Name: "free", Metric: "concurrency", KeyBy: "per_user", Capacity: 5}})

	res, err := HandleLimiterUsage(store, idx)
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := res.([]protocol.LimiterBucketStat)
	if !ok {
		t.Fatalf("want []protocol.LimiterBucketStat, got %T", res)
	}
	var row *protocol.LimiterBucketStat
	for i := range rows {
		if rows[i].LimiterID == 7 {
			row = &rows[i]
		}
	}
	if row == nil {
		t.Fatal("limiter 7 bucket missing")
	}
	if row.Name != "free" || row.Capacity != 5 || row.Occupied != 1 || row.Metric != "concurrency" || row.Bucket != "u:1" {
		t.Fatalf("metadata join wrong: %+v", *row)
	}
}
