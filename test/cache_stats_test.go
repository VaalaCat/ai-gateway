package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// TestE2E_CacheStats_AggregateAndExpose verifies that:
//  1. agent heartbeat 携带的 CacheStats 被 master Hub 接收
//  2. GET /api/admin/cache/stats 把单 agent 快照原样返回
//  3. cluster 聚合在单 agent 时等于该 agent 自身（hit_rate / util 计算正确）
//  4. full-sync 实体（capacity=0）的 hit_rate/util 为 null
func TestE2E_CacheStats_AggregateAndExpose(t *testing.T) {
	env := setupFullEnv(t, "agent-cache-stats", 0)
	defer env.Close()

	// 手动发一次 heartbeat（agent.Server.heartbeatLoop 在测试里没启动）。
	hb := protocol.HeartbeatParams{
		Uptime:  10,
		Version: 1,
		CacheStats: map[string]protocol.CacheEntityStats{
			"token":   {Hits: 80, Misses: 20, Evictions: 3, NegativeHits: 1, Size: 90, Capacity: 100},
			"user":    {Hits: 30, Misses: 10, Evictions: 0, NegativeHits: 0, Size: 40, Capacity: 50},
			"channel": {Hits: 0, Misses: 0, Evictions: 0, NegativeHits: 0, Size: 5, Capacity: 0},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := env.wsClient.Call(ctx, consts.RPCAgentHeartbeat, hb); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	// Hub 在 RPC handler 里同步写 runtimes，调返回时已可见。

	resp := env.DoAdmin("GET", "/api/admin/cache/stats", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, raw)
	}
	var got struct {
		Agents []struct {
			AgentID    string                               `json:"agent_id"`
			Name       string                               `json:"name"`
			Online     bool                                 `json:"online"`
			CacheStats map[string]protocol.CacheEntityStats `json:"cache_stats"`
		} `json:"agents"`
		Cluster map[string]struct {
			Hits         int64    `json:"hits"`
			Misses       int64    `json:"misses"`
			Evictions    int64    `json:"evictions"`
			NegativeHits int64    `json:"negative_hits"`
			Size         int      `json:"size"`
			Capacity     int      `json:"capacity"`
			HitRate      *float64 `json:"hit_rate"`
			Util         *float64 `json:"util"`
		} `json:"cluster"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(got.Agents))
	}
	a := got.Agents[0]
	if a.AgentID != "agent-cache-stats" || !a.Online {
		t.Fatalf("agent identity/online wrong: %+v", a)
	}
	if a.Name == "" {
		t.Fatalf("agent name should be populated from DAO, got empty: %+v", a)
	}
	if a.CacheStats["token"].Hits != 80 {
		t.Fatalf("agent token hits = %d, want 80", a.CacheStats["token"].Hits)
	}

	tokenAgg := got.Cluster["token"]
	if tokenAgg.Hits != 80 || tokenAgg.Misses != 20 || tokenAgg.Size != 90 || tokenAgg.Capacity != 100 {
		t.Fatalf("cluster token sums wrong: %+v", tokenAgg)
	}
	if tokenAgg.Evictions != 3 || tokenAgg.NegativeHits != 1 {
		t.Fatalf("cluster token evictions/negative_hits wrong: %+v", tokenAgg)
	}
	if tokenAgg.HitRate == nil || *tokenAgg.HitRate < 0.79 || *tokenAgg.HitRate > 0.81 {
		t.Fatalf("cluster token hit_rate wrong: %v", tokenAgg.HitRate)
	}
	if tokenAgg.Util == nil || *tokenAgg.Util < 0.89 || *tokenAgg.Util > 0.91 {
		t.Fatalf("cluster token util wrong: %v", tokenAgg.Util)
	}

	chAgg := got.Cluster["channel"]
	if chAgg.Size != 5 {
		t.Fatalf("cluster channel size wrong: %+v", chAgg)
	}
	if chAgg.HitRate != nil {
		t.Fatalf("cluster channel hit_rate must be nil, got %v", *chAgg.HitRate)
	}
	if chAgg.Util != nil {
		t.Fatalf("cluster channel util must be nil, got %v", *chAgg.Util)
	}
}
