package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestLRUCache_TokenMissTriggersFetch 启动 master+agent，token 不预 warm，
// 发请求触发 miss → master fetchEntity 拉到 token + 顺便 warm user。
func TestLRUCache_TokenMissTriggersFetch(t *testing.T) {
	env := setupFullEnv(t, "agent-lru-fetch", 1)
	defer env.Close()

	uid := env.CreateUserWithQuota("alice", 100)
	tokenKey := env.CreateToken(uid, "alice-tok")
	env.CreateModelConfig("gpt-4o")
	env.CreateChannel("ch", 1, "k", "http://stub", "gpt-4o")

	// Wait for channel/model push to settle, but token push 走 apply-if-present 不会 warm
	time.Sleep(150 * time.Millisecond)
	env.SyncFromMaster()

	// LRU 模式下 FullSync 跳过 token；agent token cache 应为空
	if got := env.Store.TokenCount(); got != 0 {
		t.Fatalf("token cache should start empty (LRU + apply-if-present), got %d", got)
	}

	// 发请求触发 miss → fetch
	resp := env.ListModels(tokenKey)
	if resp.Code != http.StatusOK {
		t.Fatalf("ListModels code=%d body=%s", resp.Code, resp.Body.String())
	}

	// 现在 token + user 应都已 warm
	if got := env.Store.TokenCount(); got != 1 {
		t.Fatalf("after first request, token cache should have 1 entry, got %d", got)
	}
	if got := env.Store.UserCount(); got != 1 {
		t.Fatalf("user cache should be warmed by side payload, got %d", got)
	}
}

// TestLRUCache_PushUpdatesExistingOnly 验证 push apply-if-present：
// 已缓存 token 收到 push update 时覆写状态。
func TestLRUCache_PushUpdatesExistingOnly(t *testing.T) {
	env := setupFullEnv(t, "agent-lru-push", 1)
	defer env.Close()

	uid := env.CreateUserWithQuota("bob", 100)
	tokenKey := env.CreateToken(uid, "bob-tok")
	env.CreateModelConfig("gpt-4o")
	env.CreateChannel("ch", 1, "k", "http://stub", "gpt-4o")
	time.Sleep(150 * time.Millisecond)

	// 第一次请求 warm token cache
	env.ListModels(tokenKey)
	if env.Store.TokenCount() != 1 {
		t.Fatalf("expected 1 cached token, got %d", env.Store.TokenCount())
	}

	// master 端禁用 token：先查 token id
	listResp := env.DoAdmin("GET", "/api/admin/tokens?search=bob-tok", nil)
	defer listResp.Body.Close()
	raw, _ := io.ReadAll(listResp.Body)
	var listed struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode tokens list: %v body=%s", err, raw)
	}
	if len(listed.Data) == 0 {
		t.Fatalf("no tokens listed: %s", raw)
	}
	tokenID := uint(listed.Data[0]["id"].(float64))

	st := env.DoAdmin("PUT", "/api/admin/tokens/"+strconv.FormatUint(uint64(tokenID), 10),
		map[string]any{"status": 0})
	st.Body.Close()
	if st.StatusCode != http.StatusOK {
		t.Fatalf("disable token: %d", st.StatusCode)
	}

	// 等 push 处理
	time.Sleep(150 * time.Millisecond)

	// agent 端 token 应已被 apply-if-present 更新为 status=0
	tok := env.Store.GetToken(context.Background(), tokenKey)
	if tok == nil {
		t.Fatal("token unexpectedly evicted")
	}
	if tok.Status != 0 {
		t.Fatalf("token status should be updated to 0 via push, got %d", tok.Status)
	}
}

// TestLRUCache_NegativeCacheAbsorbsScanner 验证扫描器场景：
// 100 次同一伪 key 请求，agent 端 singleflight + 负缓存吸收。
// 通过观察 agent token cache stats 验证：misses>=1, negativeHits>=N-misses。
func TestLRUCache_NegativeCacheAbsorbsScanner(t *testing.T) {
	env := setupFullEnv(t, "agent-lru-scanner", 1)
	defer env.Close()
	env.CreateModelConfig("gpt-4o")
	env.CreateChannel("ch", 1, "k", "http://stub", "gpt-4o")
	time.Sleep(150 * time.Millisecond)

	// 第一次请求（串行）让 negative entry 进 cache
	if r := env.ListModels("sk-no-such-token"); r.Code != http.StatusUnauthorized {
		t.Fatalf("first scanner request: expected 401, got %d", r.Code)
	}

	// 后续 N 次并发同 key 请求应全部命中负缓存
	const N = 100
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := env.ListModels("sk-no-such-token")
			if resp.Code != http.StatusUnauthorized {
				t.Errorf("scanner request: expected 401, got %d", resp.Code)
			}
		}()
	}
	wg.Wait()

	stats := env.Store.CacheSnapshot()
	tokenStats := stats["token"]
	t.Logf("token cache stats: %+v", tokenStats)

	// 首次 miss 1 次（拉取 not found 写入负缓存）
	if tokenStats.Misses != 1 {
		t.Fatalf("expected exactly 1 miss (first request); got %d", tokenStats.Misses)
	}
	// 后续 N 次应全部命中负缓存
	if tokenStats.NegativeHits < int64(N) {
		t.Fatalf("expected NegativeHits >= %d after warm-up; got %d", N, tokenStats.NegativeHits)
	}
}
