package sync

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeBroadcaster 捕获 Publisher 发出的广播与定向通知。线程安全：MemoryBus 异步投递。
type fakeBroadcaster struct {
	mu       sync.Mutex
	pushes   []protocol.SyncPushParams
	notifies []fakeNotify
}

type orderedBlockingBroadcaster struct {
	calls         atomic.Int64
	firstEntered  chan protocol.SyncPushParams
	secondEntered chan protocol.SyncPushParams
	releaseFirst  chan struct{}
	mu            sync.Mutex
	pushes        []protocol.SyncPushParams
}

func newOrderedBlockingBroadcaster() *orderedBlockingBroadcaster {
	return &orderedBlockingBroadcaster{
		firstEntered:  make(chan protocol.SyncPushParams, 1),
		secondEntered: make(chan protocol.SyncPushParams, 1),
		releaseFirst:  make(chan struct{}),
	}
}

func (b *orderedBlockingBroadcaster) Broadcast(_ string, params any) {
	push, ok := params.(protocol.SyncPushParams)
	if !ok {
		return
	}
	switch b.calls.Add(1) {
	case 1:
		b.firstEntered <- push
		<-b.releaseFirst
	case 2:
		b.secondEntered <- push
	}
	b.mu.Lock()
	b.pushes = append(b.pushes, push)
	b.mu.Unlock()
}

func (*orderedBlockingBroadcaster) NotifyAgent(string, string, any) {}

func (b *orderedBlockingBroadcaster) snapshot() []protocol.SyncPushParams {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]protocol.SyncPushParams(nil), b.pushes...)
}

// fakeNotify 记录一次 NotifyAgent 调用。
type fakeNotify struct {
	agentID string
	method  string
	params  any
}

type inertSubscription struct{}

func (inertSubscription) Unsubscribe() error { return nil }

type subscriptionAuditBus struct {
	topics   []string
	patterns []string
}

func (b *subscriptionAuditBus) Publish(context.Context, eventbus.Event) error { return nil }

func (b *subscriptionAuditBus) Subscribe(topic string, _ eventbus.EventHandler) (eventbus.Subscription, error) {
	b.topics = append(b.topics, topic)
	return inertSubscription{}, nil
}

func (b *subscriptionAuditBus) SubscribePattern(pattern string, _ eventbus.EventHandler) (eventbus.Subscription, error) {
	b.patterns = append(b.patterns, pattern)
	return inertSubscription{}, nil
}

func (b *subscriptionAuditBus) Close() error { return nil }

func (b *subscriptionAuditBus) matches(topic string) bool {
	for _, subscribed := range b.topics {
		if subscribed == topic {
			return true
		}
	}
	for _, pattern := range b.patterns {
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(topic, strings.TrimSuffix(pattern, "*")) {
			return true
		}
		if pattern == topic {
			return true
		}
	}
	return false
}

func (f *fakeBroadcaster) Broadcast(_ string, params any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := params.(protocol.SyncPushParams); ok {
		f.pushes = append(f.pushes, p)
	}
}

func (f *fakeBroadcaster) NotifyAgent(agentID, method string, params any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifies = append(f.notifies, fakeNotify{agentID: agentID, method: method, params: params})
}

func (f *fakeBroadcaster) snapshot() []protocol.SyncPushParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.SyncPushParams(nil), f.pushes...)
}

func (f *fakeBroadcaster) notifySnapshot() []fakeNotify {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeNotify(nil), f.notifies...)
}

// waitFor 轮询等待 cond 成立（MemoryBus 异步投递 → 不能即时断言）。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}

func TestPublisher_RoutesUserQuotaSyncToSourceAgent(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	p := &Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}
	p.Start()

	want := protocol.UserQuotaSync{
		AgentID: "agent-7",
		Users:   []protocol.SyncedUser{{ID: 3, GroupID: 1, Quota: 50}},
	}
	if err := events.PublishUserQuotaSync(context.Background(), bus, want); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, func() bool { return len(fb.notifySnapshot()) > 0 })

	notifies := fb.notifySnapshot()
	if len(notifies) != 1 {
		t.Fatalf("want 1 notify, got %d", len(notifies))
	}
	n := notifies[0]
	if n.agentID != "agent-7" {
		t.Fatalf("agentID = %q, want agent-7", n.agentID)
	}
	if n.method != consts.RPCSyncUserQuota {
		t.Fatalf("method = %q, want %q", n.method, consts.RPCSyncUserQuota)
	}
	users, ok := n.params.([]protocol.SyncedUser)
	if !ok {
		t.Fatalf("params type = %T, want []protocol.SyncedUser", n.params)
	}
	if len(users) != 1 || users[0].ID != 3 || users[0].Quota != 50 {
		t.Fatalf("users payload = %+v, want [{ID:3 ... Quota:50}]", users)
	}

	// 不应触发全量 Broadcast。
	if got := len(fb.snapshot()); got != 0 {
		t.Fatalf("Broadcast count = %d, want 0 (targeted notify only)", got)
	}
}

func TestPublisher_BroadcastsPrivateChannelInvalidate(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	p := &Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}
	p.Start()

	if err := events.PublishPrivateChannelInvalidate(context.Background(), bus, []uint{38}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, func() bool { return len(fb.snapshot()) > 0 })

	pushes := fb.snapshot()
	if len(pushes) != 1 {
		t.Fatalf("want 1 push, got %d", len(pushes))
	}
	if pushes[0].Entity != events.EntityPrivateChannel || pushes[0].Action != "invalidate" {
		t.Fatalf("unexpected push entity/action: %+v", pushes[0])
	}
	var payload protocol.PrivateChannelInvalidatePayload
	if err := json.Unmarshal(pushes[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if len(payload.AffectedUserIDs) != 1 || payload.AffectedUserIDs[0] != 38 {
		t.Fatalf("AffectedUserIDs = %v, want [38]", payload.AffectedUserIDs)
	}
}

func TestPublisherAndEventRegistryExcludeMasterSigningSources(t *testing.T) {
	bus := &subscriptionAuditBus{}
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	(&Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}).Start()
	registry := events.NewRegistry()

	for _, entity := range []string{"master_signing_key", "master_signing_keys"} {
		for _, action := range []string{events.ActionCreate, events.ActionUpdate, events.ActionDelete} {
			for _, topic := range []string{
				entity + "." + action,
				"sync." + entity + "." + action,
			} {
				if bus.matches(topic) {
					t.Fatal("publisher subscribed to a master signing topic")
				}
				if _, ok := registry.PayloadType(topic); ok {
					t.Fatal("event registry exposed a master signing topic")
				}
			}
		}
	}
}

func TestPublisherBroadcastsGenericAPIProjectionAndRoleSetInvalidation(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	(&Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}).Start()

	require.NoError(t, events.Publish(context.Background(), bus, events.APIServiceUpdateTopic,
		protocol.SyncedAPIService{ID: 9, Slug: "weather", Name: "Weather", ConsumesQuota: true, Status: 1}))
	require.NoError(t, events.Publish(context.Background(), bus, events.TokenAPIRolesSyncedTopic,
		protocol.APIRoleSetInvalidate{PrincipalID: 14}))

	waitFor(t, func() bool { return len(fb.snapshot()) == 2 })
	pushes := fb.snapshot()
	require.Equal(t, int64(1), pushes[0].Version)
	require.Equal(t, int64(2), pushes[1].Version)
	require.Equal(t, events.EntityAPIService, pushes[0].Entity)
	require.Equal(t, events.ActionUpdate, pushes[0].Action)
	require.NotContains(t, string(pushes[0].Data), "price")
	require.Equal(t, events.EntityTokenAPIRoleSet, pushes[1].Entity)
	require.Equal(t, events.ActionInvalidate, pushes[1].Action)
	require.JSONEq(t, `{"principal_id":14}`, string(pushes[1].Data))
}

func TestPublisherSerializesVersionAllocationWithWireBroadcastOrder(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	broadcaster := newOrderedBlockingBroadcaster()
	var version atomic.Int64
	(&Publisher{hub: broadcaster, bus: bus, version: &version, logger: zap.NewNop()}).Start()
	first := protocol.SyncedAPIService{ID: 1, Slug: "first"}
	second := protocol.SyncedAPIService{ID: 2, Slug: "second"}
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	secondStarted := make(chan struct{})
	var group conc.WaitGroup
	group.Go(func() {
		firstResult <- events.Publish(context.Background(), bus, events.APIServiceUpdateTopic, first)
	})
	firstPush := <-broadcaster.firstEntered
	group.Go(func() {
		close(secondStarted)
		secondResult <- events.Publish(context.Background(), bus, events.APIServiceUpdateTopic, second)
	})
	<-secondStarted

	secondEnteredEarly := false
	select {
	case <-broadcaster.secondEntered:
		secondEnteredEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	close(broadcaster.releaseFirst)
	group.Wait()

	require.NoError(t, <-firstResult)
	require.NoError(t, <-secondResult)
	require.False(t, secondEnteredEarly, "second Broadcast entered before the first wire enqueue completed")
	require.Equal(t, int64(1), firstPush.Version)
	var firstPayload protocol.SyncedAPIService
	require.NoError(t, json.Unmarshal(firstPush.Data, &firstPayload))
	require.Equal(t, first.ID, firstPayload.ID)
	pushes := broadcaster.snapshot()
	require.Len(t, pushes, 2)
	require.Equal(t, []int64{1, 2}, []int64{pushes[0].Version, pushes[1].Version})
	var wireFirst, wireSecond protocol.SyncedAPIService
	require.NoError(t, json.Unmarshal(pushes[0].Data, &wireFirst))
	require.NoError(t, json.Unmarshal(pushes[1].Data, &wireSecond))
	require.Equal(t, []uint{first.ID, second.ID}, []uint{wireFirst.ID, wireSecond.ID})
}

func TestPublisherConcurrentBroadcastsHaveStrictlyMonotonicWireVersions(t *testing.T) {
	const publishCount = 32
	bus := eventbus.NewMemoryBus()
	broadcaster := &fakeBroadcaster{}
	var version atomic.Int64
	(&Publisher{hub: broadcaster, bus: bus, version: &version, logger: zap.NewNop()}).Start()
	start := make(chan struct{})
	errorsCh := make(chan error, publishCount)
	var group conc.WaitGroup
	for id := uint(1); id <= publishCount; id++ {
		id := id
		group.Go(func() {
			<-start
			errorsCh <- events.Publish(context.Background(), bus, events.APIServiceUpdateTopic,
				protocol.SyncedAPIService{ID: id, Slug: "concurrent"})
		})
	}
	close(start)
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}

	pushes := broadcaster.snapshot()
	require.Len(t, pushes, publishCount)
	seenIDs := make(map[uint]struct{}, publishCount)
	for index, push := range pushes {
		require.Equal(t, int64(index+1), push.Version)
		var payload protocol.SyncedAPIService
		require.NoError(t, json.Unmarshal(push.Data, &payload))
		seenIDs[payload.ID] = struct{}{}
	}
	require.Len(t, seenIDs, publishCount)
}

func TestPublisherRegistersAllGenericAPITypedTopics(t *testing.T) {
	bus := &subscriptionAuditBus{}
	(&Publisher{hub: &fakeBroadcaster{}, bus: bus, version: &atomic.Int64{}, logger: zap.NewNop()}).Start()
	for _, topic := range []string{
		"api_service.create", "api_service.update", "api_service.delete",
		"api_route.create", "api_route.update", "api_route.delete",
		"api_upstream.create", "api_upstream.update", "api_upstream.delete",
		"api_role.create", "api_role.update", "api_role.delete",
		"user.api_roles_synced", "user_group.api_roles_synced", "user_group.api_roles_deleted", "token.api_roles_synced",
	} {
		require.True(t, bus.matches(topic), topic)
		require.NotNil(t, func() any {
			typ, ok := events.NewRegistry().PayloadType(topic)
			if !ok {
				return nil
			}
			return typ
		}(), topic)
	}
}

func TestPublisherVersionsEveryGenericAPIFullCacheCRUDPush(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	(&Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}).Start()

	type publishCase struct {
		entity   string
		action   string
		expected any
		publish  func() error
	}
	service := protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: "Weather", ConsumesQuota: true, Status: 1}
	route := protocol.SyncedAPIRoute{ID: 2, ServiceID: 1, Slug: "forecast", Protocols: []string{"http"}, Status: 1}
	upstream := protocol.SyncedAPIUpstream{ID: 3, BackendID: 1, Name: "primary", BaseURL: "https://example.com", AuthType: "none", Status: 1}
	role := protocol.SyncedAPIRole{ID: 4, Name: "Invoker", Permissions: []protocol.APIPermissionGrant{}}
	cases := []publishCase{
		{events.EntityAPIService, events.ActionCreate, service, func() error { return events.Publish(context.Background(), bus, events.APIServiceCreateTopic, service) }},
		{events.EntityAPIService, events.ActionUpdate, service, func() error { return events.Publish(context.Background(), bus, events.APIServiceUpdateTopic, service) }},
		{events.EntityAPIService, events.ActionDelete, service, func() error { return events.Publish(context.Background(), bus, events.APIServiceDeleteTopic, service) }},
		{events.EntityAPIRoute, events.ActionCreate, route, func() error { return events.Publish(context.Background(), bus, events.APIRouteCreateTopic, route) }},
		{events.EntityAPIRoute, events.ActionUpdate, route, func() error { return events.Publish(context.Background(), bus, events.APIRouteUpdateTopic, route) }},
		{events.EntityAPIRoute, events.ActionDelete, route, func() error { return events.Publish(context.Background(), bus, events.APIRouteDeleteTopic, route) }},
		{events.EntityAPIUpstream, events.ActionCreate, upstream, func() error {
			return events.Publish(context.Background(), bus, events.APIUpstreamCreateTopic, upstream)
		}},
		{events.EntityAPIUpstream, events.ActionUpdate, upstream, func() error {
			return events.Publish(context.Background(), bus, events.APIUpstreamUpdateTopic, upstream)
		}},
		{events.EntityAPIUpstream, events.ActionDelete, upstream, func() error {
			return events.Publish(context.Background(), bus, events.APIUpstreamDeleteTopic, upstream)
		}},
		{events.EntityAPIRole, events.ActionCreate, role, func() error { return events.Publish(context.Background(), bus, events.APIRoleCreateTopic, role) }},
		{events.EntityAPIRole, events.ActionUpdate, role, func() error { return events.Publish(context.Background(), bus, events.APIRoleUpdateTopic, role) }},
		{events.EntityAPIRole, events.ActionDelete, role, func() error { return events.Publish(context.Background(), bus, events.APIRoleDeleteTopic, role) }},
	}
	for i, tc := range cases {
		require.NoError(t, tc.publish())
		pushes := fb.snapshot()
		require.Len(t, pushes, i+1)
		push := pushes[i]
		require.Equal(t, tc.entity, push.Entity)
		require.Equal(t, tc.action, push.Action)
		require.Equal(t, int64(i+1), push.Version)
		expectedJSON, err := json.Marshal(tc.expected)
		require.NoError(t, err)
		require.JSONEq(t, string(expectedJSON), string(push.Data))
		body := string(push.Data)
		for _, forbidden := range []string{"price_per_call", "credential_ciphertext", "proxy_url_ciphertext"} {
			require.NotContains(t, body, forbidden)
		}
	}
}
