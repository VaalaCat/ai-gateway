package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

var _ app.Syncer = (*Syncer)(nil)

const (
	maxAgentPushBuffer      = 4096
	maxAgentRoutePushBuffer = 4096
)

var (
	errAgentPushBufferOverflow      = errors.New("agent full sync push buffer overflow")
	errAgentRoutePushBufferOverflow = errors.New("agent route full sync push buffer overflow")
	ErrControlSessionChanged        = errors.New("control session changed")
)

// ControlSession is an unforgeable lease for one installed Master connection.
// Its fields stay private so only Syncer can decide whether a caller may commit.
type ControlSession struct {
	generation uint64
	client     app.WSClient
	ctx        context.Context
	cancel     context.CancelCauseFunc
}

type controlSessionContextKey struct{}

func contextWithControlSession(ctx context.Context, session *ControlSession) context.Context {
	if ctx == nil || session == nil {
		return ctx
	}
	return context.WithValue(ctx, controlSessionContextKey{}, session)
}

func controlSessionFromContext(ctx context.Context) *ControlSession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(controlSessionContextKey{}).(*ControlSession)
	return session
}

type bufferedAgentRoutePush struct {
	action  string
	route   models.AgentRoute
	version int64
}

type bufferedAgentPush struct {
	action  string
	agent   models.Agent
	version int64
}

type agentSyncBuilder struct {
	agents      []models.Agent
	pushes      []bufferedAgentPush
	baseVersion int64
	failed      error
	cancel      context.CancelFunc
	session     *ControlSession
}

type agentRouteSyncBuilder struct {
	routes      []*models.AgentRoute
	pushes      []bufferedAgentRoutePush
	baseVersion int64
	failed      error
	cancel      context.CancelFunc
	session     *ControlSession
}

type Syncer struct {
	Store            *Store
	Bus              app.EventBus
	Logger           *zap.Logger
	FullSyncInterval time.Duration
	mu               sync.Mutex // first in lock order; protects controlSession
	controlSession   *ControlSession
	nextGeneration   uint64

	agentRouteSyncMu  sync.Mutex
	agentRouteStateMu sync.Mutex
	agentRouteBuilder *agentRouteSyncBuilder
	agentRouteDirty   atomic.Bool

	agentSyncMu  sync.Mutex
	agentStateMu sync.Mutex
	agentBuilder *agentSyncBuilder
	agentsDirty  atomic.Bool

	requestMu        sync.Mutex
	fullSyncRequests chan *ControlSession
}

func NewSyncer(store *Store, client app.WSClient, bus app.EventBus, logger *zap.Logger, interval time.Duration) *Syncer {
	s := &Syncer{
		Store:            store,
		Bus:              bus,
		Logger:           logger,
		FullSyncInterval: interval,
		fullSyncRequests: make(chan *ControlSession, 1),
	}
	if client != nil {
		s.BeginControlSession(client)
	}
	return s
}

// SetClient replaces the WS client (e.g., after reconnection)
func (s *Syncer) SetClient(client app.WSClient) {
	s.BeginControlSession(client)
}

// BeginControlSession installs a new client lease and cancels the previous one.
func (s *Syncer) BeginControlSession(client app.WSClient) *ControlSession {
	if s == nil {
		return nil
	}
	var previous *ControlSession
	s.mu.Lock()
	previous = s.controlSession
	if client == nil {
		s.controlSession = nil
		s.mu.Unlock()
		if previous != nil {
			previous.cancel(ErrControlSessionChanged)
		}
		return nil
	}
	s.nextGeneration++
	if s.nextGeneration == 0 {
		s.nextGeneration++
	}
	sessionCtx, sessionCancel := context.WithCancelCause(context.Background())
	session := &ControlSession{
		generation: s.nextGeneration,
		client:     client,
		ctx:        sessionCtx,
		cancel:     sessionCancel,
	}
	s.controlSession = session
	s.mu.Unlock()
	if previous != nil {
		previous.cancel(ErrControlSessionChanged)
	}
	return session
}

// EndControlSession clears only the expected lease; stale cleanup cannot clear
// a replacement connection.
func (s *Syncer) EndControlSession(expected *ControlSession) bool {
	if s == nil || expected == nil {
		return false
	}
	s.mu.Lock()
	if s.controlSession != expected {
		s.mu.Unlock()
		return false
	}
	s.controlSession = nil
	s.mu.Unlock()
	expected.cancel(ErrControlSessionChanged)
	return true
}

func (s *Syncer) CurrentControlSession() *ControlSession {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlSession
}

func (s *Syncer) controlSessionContext(
	ctx context.Context,
	expected *ControlSession,
) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("full sync: nil context")
	}
	if expected == nil {
		return nil, nil, ErrControlSessionChanged
	}
	s.mu.Lock()
	current := s.controlSession == expected
	s.mu.Unlock()
	if !current {
		return nil, nil, ErrControlSessionChanged
	}

	callCtx, cancel := context.WithCancelCause(ctx)
	stopSession := context.AfterFunc(expected.ctx, func() {
		cause := context.Cause(expected.ctx)
		if cause == nil {
			cause = ErrControlSessionChanged
		}
		cancel(cause)
	})
	cleanup := func() {
		stopSession()
		cancel(context.Canceled)
	}
	return callCtx, cleanup, nil
}

// withCurrentControlSession linearizes a local cache commit with connection
// replacement. The callback must remain memory-only and must not perform I/O.
func (s *Syncer) withCurrentControlSession(expected *ControlSession, apply func() error) error {
	if s == nil || expected == nil || apply == nil {
		return ErrControlSessionChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controlSession != expected || context.Cause(expected.ctx) != nil {
		return ErrControlSessionChanged
	}
	return apply()
}

// FullSync pulls all data from master.
//
// LRU 模式实体（token / user / private_channel）不参与 FullSync——它们靠 push
// invalidate + miss 时 RPC 拉取保持最新。全量重拉违背 LRU 容量目标，会让 agent
// 内存重新涨回基线。
func (s *Syncer) FullSync(ctx context.Context) error {
	return s.FullSyncForSession(ctx, s.CurrentControlSession())
}

// FullSyncForSession runs and commits one pass only while expected remains the
// current control connection.
func (s *Syncer) FullSyncForSession(ctx context.Context, expected *ControlSession) error {
	syncCtx, cleanup, err := s.controlSessionContext(ctx, expected)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.fullSyncForSession(syncCtx, expected)
}

func (s *Syncer) fullSyncForSession(ctx context.Context, expected *ControlSession) error {
	s.markAgentRoutesDirty()
	if err := s.fullSyncEntityForSession(ctx, expected, "user_group"); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync user_group failed", zap.Error(err))
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "channel"); err != nil {
		return err
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "model_config"); err != nil {
		return err
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "setting"); err != nil {
		return err
	}
	if err := s.fullSyncAgentsForSession(ctx, expected); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync agent failed", zap.Error(err))
	}
	var agentRouteErr error
	if err := s.fullSyncAgentRoutesForSession(ctx, expected); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync agent_route failed", zap.Error(err))
		agentRouteErr = err
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "request_limiter"); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync request_limiter failed", zap.Error(err))
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "limiter_binding"); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync limiter_binding failed", zap.Error(err))
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "model_routing"); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync model_routing failed", zap.Error(err))
	}
	if err := s.fullSyncEntityForSession(ctx, expected, "script"); err != nil {
		if errors.Is(err, ErrControlSessionChanged) {
			return err
		}
		s.Logger.Error("full sync script failed", zap.Error(err))
	}
	if err := s.withCurrentControlSession(expected, func() error {
		s.Store.RebuildModelIndex()
		return nil
	}); err != nil {
		return err
	}
	if agentRouteErr != nil {
		return agentRouteErr
	}
	s.Logger.Info("full sync complete (LRU entities skipped)",
		zap.Int("user_groups", s.Store.UserGroupCount()),
		zap.Int("channels", s.Store.ChannelCount()),
		zap.Int("models", s.Store.ModelConfigCount()),
		zap.Int("global_routings", s.Store.GlobalRoutingCount()),
		zap.Int64("version", s.Store.Version()),
	)
	return nil
}

func (s *Syncer) fullSyncEntity(ctx context.Context, entity string) error {
	expected := s.CurrentControlSession()
	syncCtx, cleanup, err := s.controlSessionContext(ctx, expected)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.fullSyncEntityForSession(syncCtx, expected, entity)
}

func (s *Syncer) fullSyncEntityForSession(ctx context.Context, expected *ControlSession, entity string) error {
	if entity == events.EntityAgentRoute {
		return s.fullSyncAgentRoutesForSession(ctx, expected)
	}
	if entity == events.EntityAgent {
		return s.fullSyncAgentsForSession(ctx, expected)
	}
	if expected == nil || expected.client == nil {
		return fmt.Errorf("no ws client")
	}
	page := 1
	for {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := expected.client.Call(callCtx, consts.RPCSyncFullSync, protocol.FullSyncRequest{
			Entity:   entity,
			Page:     page,
			PageSize: protocol.FullSyncMaxPageSize,
		})
		cancel()
		if err != nil {
			return err
		}

		var resp protocol.FullSyncResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return err
		}

		if err := s.commitFullSyncPage(expected, entity, resp); err != nil {
			return err
		}

		if !resp.HasMore {
			break
		}
		page++
	}
	return nil
}

func (s *Syncer) commitFullSyncPage(expected *ControlSession, entity string, resp protocol.FullSyncResponse) error {
	var commit func()
	switch entity {
	case events.EntityUserGroup:
		var items []models.UserGroup
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LoadUserGroups(items) }
	case events.EntityChannel:
		var items []models.Channel
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LoadChannels(items) }
	case events.EntityModelV1:
		var items []models.ModelConfig
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LoadModelConfigs(items) }
	case events.EntitySetting:
		var items []models.Setting
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LoadSettings(items) }
	case events.EntityRequestLimiter:
		var items []models.RequestLimiter
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LimiterIndex.LoadLimiters(items) }
	case events.EntityLimiterBinding:
		var items []models.LimiterBinding
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LimiterIndex.LoadBindings(items) }
	case events.EntityModelRouting:
		var items []models.ModelRouting
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LoadGlobalRoutings(items) }
	case events.EntityScript:
		var items []models.AdminScript
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		commit = func() { s.Store.LoadScripts(items) }
	default:
		return fmt.Errorf("unsupported full sync entity %q", entity)
	}
	return s.withCurrentControlSession(expected, func() error {
		commit()
		s.Store.AdvanceVersion(resp.Version)
		return nil
	})
}

func (s *Syncer) fullSyncAgents(ctx context.Context) error {
	expected := s.CurrentControlSession()
	syncCtx, cleanup, err := s.controlSessionContext(ctx, expected)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.fullSyncAgentsForSession(syncCtx, expected)
}

func (s *Syncer) fullSyncAgentsForSession(ctx context.Context, expected *ControlSession) error {
	s.agentSyncMu.Lock()
	defer s.agentSyncMu.Unlock()
	s.agentsDirty.Store(true)

	if expected == nil || expected.client == nil {
		return fmt.Errorf("no ws client")
	}

	pullCtx, pullCancel := context.WithCancel(ctx)
	builder := &agentSyncBuilder{
		pushes:  make([]bufferedAgentPush, 0, maxAgentPushBuffer),
		cancel:  pullCancel,
		session: expected,
	}
	if err := s.withCurrentControlSession(expected, func() error {
		s.agentStateMu.Lock()
		s.agentBuilder = builder
		s.agentStateMu.Unlock()
		return nil
	}); err != nil {
		pullCancel()
		return err
	}
	defer pullCancel()
	defer s.clearAgentBuilder(builder)

	req := protocol.FullSyncRequest{
		Entity:   events.EntityAgent,
		PageSize: protocol.FullSyncMaxPageSize,
	}
	var snapshotMaxID uint
	var baseVersion int64
	snapshotSet := false
	var maxResponseVersion int64
	for {
		callCtx, callCancel := context.WithTimeout(pullCtx, 30*time.Second)
		result, err := expected.client.Call(callCtx, consts.RPCSyncFullSync, req)
		callCancel()
		if builderErr := s.agentBuilderError(builder); builderErr != nil {
			return builderErr
		}
		if err != nil {
			return err
		}
		if cause := context.Cause(pullCtx); cause != nil {
			return cause
		}

		var resp protocol.FullSyncResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return err
		}
		if builderErr := s.agentBuilderError(builder); builderErr != nil {
			return builderErr
		}
		if resp.SnapshotContract != protocol.AgentFullSyncSnapshotContractV1 {
			return fmt.Errorf("agent full sync unsupported snapshot contract %q", resp.SnapshotContract)
		}
		if !resp.Keyset {
			return errors.New("agent full sync requires keyset snapshot")
		}
		if !snapshotSet {
			snapshotMaxID = resp.SnapshotMaxID
			baseVersion = resp.BaseVersion
			snapshotSet = true
			s.agentStateMu.Lock()
			builder.baseVersion = baseVersion
			s.agentStateMu.Unlock()
		} else if resp.SnapshotMaxID != snapshotMaxID || resp.BaseVersion != baseVersion {
			return errors.New("agent full sync keyset snapshot changed between pages")
		}
		if resp.Version > maxResponseVersion {
			maxResponseVersion = resp.Version
		}

		var items []models.Agent
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		builder.agents = append(builder.agents, items...)
		if !resp.HasMore {
			break
		}
		if resp.LastID <= req.AfterID || resp.LastID > snapshotMaxID {
			return fmt.Errorf(
				"agent full sync keyset cursor made no valid progress: after=%d last=%d max=%d",
				req.AfterID,
				resp.LastID,
				snapshotMaxID,
			)
		}
		req = protocol.FullSyncRequest{
			Entity:        events.EntityAgent,
			PageSize:      protocol.FullSyncMaxPageSize,
			AfterID:       resp.LastID,
			SnapshotMaxID: snapshotMaxID,
			BaseVersion:   baseVersion,
		}
	}

	return s.finalizeAgentBuilder(pullCtx, builder, maxResponseVersion)
}

func (s *Syncer) finalizeAgentBuilder(ctx context.Context, builder *agentSyncBuilder, maxResponseVersion int64) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return s.withCurrentControlSession(builder.session, func() error {
		s.agentStateMu.Lock()
		defer s.agentStateMu.Unlock()
		if s.agentBuilder != builder {
			return errors.New("agent full sync builder is no longer active")
		}
		if builder.failed != nil {
			return builder.failed
		}
		finalAgents := buildFinalAgents(builder.agents, builder.pushes, builder.baseVersion)
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		s.Store.LoadAgents(finalAgents)
		s.agentsDirty.Store(false)
		s.Store.AdvanceVersion(maxResponseVersion)
		s.agentBuilder = nil
		return nil
	})
}

func buildFinalAgents(base []models.Agent, pushes []bufferedAgentPush, baseVersion int64) []models.Agent {
	agents := make(map[string]models.Agent, len(base)+len(pushes))
	for i := range base {
		agents[base[i].AgentID] = base[i]
	}
	orderedPushes := append([]bufferedAgentPush(nil), pushes...)
	sort.SliceStable(orderedPushes, func(i, j int) bool {
		return orderedPushes[i].version < orderedPushes[j].version
	})
	for i := range orderedPushes {
		push := &orderedPushes[i]
		if push.version <= baseVersion {
			continue
		}
		switch push.action {
		case events.ActionCreate, events.ActionUpdate:
			agents[push.agent.AgentID] = push.agent
		case events.ActionDelete:
			delete(agents, push.agent.AgentID)
		}
	}

	agentIDs := make([]string, 0, len(agents))
	for agentID := range agents {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	finalAgents := make([]models.Agent, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		finalAgents = append(finalAgents, agents[agentID])
	}
	return finalAgents
}

func (s *Syncer) clearAgentBuilder(builder *agentSyncBuilder) {
	s.agentStateMu.Lock()
	if s.agentBuilder == builder {
		s.agentBuilder = nil
	}
	s.agentStateMu.Unlock()
}

func (s *Syncer) agentBuilderError(builder *agentSyncBuilder) error {
	s.agentStateMu.Lock()
	defer s.agentStateMu.Unlock()
	if s.agentBuilder != builder {
		return errors.New("agent full sync builder is no longer active")
	}
	return builder.failed
}

func (s *Syncer) failAgentSync(err error) {
	if s == nil || err == nil {
		return
	}
	var cancel context.CancelFunc
	s.agentStateMu.Lock()
	s.agentsDirty.Store(true)
	if builder := s.agentBuilder; builder != nil && builder.failed == nil {
		builder.failed = err
		cancel = builder.cancel
	}
	s.agentStateMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// fullSyncAgentRoutes builds every page off-index, then atomically publishes
// the complete snapshot and replays route pushes newer than its frozen base.
func (s *Syncer) fullSyncAgentRoutes(ctx context.Context) error {
	expected := s.CurrentControlSession()
	syncCtx, cleanup, err := s.controlSessionContext(ctx, expected)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.fullSyncAgentRoutesForSession(syncCtx, expected)
}

func (s *Syncer) fullSyncAgentRoutesForSession(ctx context.Context, expected *ControlSession) error {
	s.agentRouteSyncMu.Lock()
	defer s.agentRouteSyncMu.Unlock()
	s.markAgentRoutesDirty()

	if expected == nil || expected.client == nil {
		return fmt.Errorf("no ws client")
	}

	pullCtx, pullCancel := context.WithCancel(ctx)
	builder := &agentRouteSyncBuilder{
		pushes:  make([]bufferedAgentRoutePush, 0, maxAgentRoutePushBuffer),
		cancel:  pullCancel,
		session: expected,
	}
	if err := s.withCurrentControlSession(expected, func() error {
		s.agentRouteStateMu.Lock()
		s.agentRouteBuilder = builder
		s.agentRouteStateMu.Unlock()
		return nil
	}); err != nil {
		pullCancel()
		return err
	}
	defer pullCancel()
	defer s.clearAgentRouteBuilder(builder)

	req := protocol.FullSyncRequest{
		Entity:   events.EntityAgentRoute,
		PageSize: protocol.FullSyncMaxPageSize,
	}
	modeKnown := false
	keyset := false
	var snapshotMaxID uint
	var baseVersion int64
	var maxResponseVersion int64

	for {
		callCtx, callCancel := context.WithTimeout(pullCtx, 30*time.Second)
		result, err := expected.client.Call(callCtx, consts.RPCSyncFullSync, req)
		callCancel()
		if builderErr := s.agentRouteBuilderError(builder); builderErr != nil {
			return builderErr
		}
		if err != nil {
			return err
		}
		if cause := context.Cause(pullCtx); cause != nil {
			return cause
		}

		var resp protocol.FullSyncResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return err
		}
		if builderErr := s.agentRouteBuilderError(builder); builderErr != nil {
			return builderErr
		}
		if resp.Version > maxResponseVersion {
			maxResponseVersion = resp.Version
		}

		if !modeKnown {
			modeKnown = true
			keyset = resp.Keyset
			if keyset {
				snapshotMaxID = resp.SnapshotMaxID
				baseVersion = resp.BaseVersion
			} else {
				// Old Masters normalize the Page=0 handshake to legacy page one.
				baseVersion = resp.Version
			}
			s.agentRouteStateMu.Lock()
			builder.baseVersion = baseVersion
			s.agentRouteStateMu.Unlock()
		} else if keyset {
			if !resp.Keyset {
				return errors.New("agent route full sync changed from keyset to legacy pagination")
			}
			if resp.SnapshotMaxID != snapshotMaxID || resp.BaseVersion != baseVersion {
				return errors.New("agent route full sync keyset snapshot changed between pages")
			}
		} else if resp.Keyset {
			return errors.New("agent route full sync changed from legacy to keyset pagination")
		}

		var items []*models.AgentRoute
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return err
		}
		builder.routes = append(builder.routes, items...)

		if !resp.HasMore {
			break
		}
		if keyset {
			if resp.LastID <= req.AfterID || resp.LastID > snapshotMaxID {
				return fmt.Errorf("agent route full sync keyset cursor made no valid progress: after=%d last=%d max=%d", req.AfterID, resp.LastID, snapshotMaxID)
			}
			req = protocol.FullSyncRequest{
				Entity:        events.EntityAgentRoute,
				PageSize:      protocol.FullSyncMaxPageSize,
				AfterID:       resp.LastID,
				SnapshotMaxID: snapshotMaxID,
				BaseVersion:   baseVersion,
			}
			continue
		}

		page := resp.Page
		if page <= 0 {
			page = req.Page
			if page <= 0 {
				page = 1
			}
		}
		req = protocol.FullSyncRequest{
			Entity:   events.EntityAgentRoute,
			Page:     page + 1,
			PageSize: protocol.FullSyncMaxPageSize,
		}
	}
	return s.finalizeAgentRouteBuilder(pullCtx, builder, maxResponseVersion)
}

func (s *Syncer) finalizeAgentRouteBuilder(ctx context.Context, builder *agentRouteSyncBuilder, maxResponseVersion int64) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return s.withCurrentControlSession(builder.session, func() error {
		s.agentRouteStateMu.Lock()
		defer s.agentRouteStateMu.Unlock()
		if s.agentRouteBuilder != builder {
			return errors.New("agent route full sync builder is no longer active")
		}
		if builder.failed != nil {
			return builder.failed
		}
		finalRoutes := buildFinalAgentRoutes(builder.routes, builder.pushes, builder.baseVersion)
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		s.Store.RouteIndex.Replace(finalRoutes)
		s.agentRouteDirty.Store(false)
		s.Store.AdvanceVersion(maxResponseVersion)
		s.agentRouteBuilder = nil
		return nil
	})
}

func buildFinalAgentRoutes(base []*models.AgentRoute, pushes []bufferedAgentRoutePush, baseVersion int64) []*models.AgentRoute {
	routes := make(map[uint]models.AgentRoute, len(base)+len(pushes))
	for _, route := range base {
		if route != nil {
			routes[route.ID] = *route
		}
	}

	orderedPushes := append([]bufferedAgentRoutePush(nil), pushes...)
	sort.SliceStable(orderedPushes, func(i, j int) bool {
		return orderedPushes[i].version < orderedPushes[j].version
	})
	for i := range orderedPushes {
		push := &orderedPushes[i]
		if push.version <= baseVersion {
			continue
		}
		switch push.action {
		case events.ActionCreate, events.ActionUpdate:
			routes[push.route.ID] = push.route
		case events.ActionDelete:
			delete(routes, push.route.ID)
		}
	}

	ids := make([]uint, 0, len(routes))
	for id := range routes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	finalRoutes := make([]*models.AgentRoute, 0, len(ids))
	for _, id := range ids {
		route := routes[id]
		finalRoutes = append(finalRoutes, &route)
	}
	return finalRoutes
}

func (s *Syncer) clearAgentRouteBuilder(builder *agentRouteSyncBuilder) {
	s.agentRouteStateMu.Lock()
	if s.agentRouteBuilder == builder {
		s.agentRouteBuilder = nil
	}
	s.agentRouteStateMu.Unlock()
}

func (s *Syncer) agentRouteBuilderError(builder *agentRouteSyncBuilder) error {
	s.agentRouteStateMu.Lock()
	defer s.agentRouteStateMu.Unlock()
	if s.agentRouteBuilder != builder {
		return errors.New("agent route full sync builder is no longer active")
	}
	return builder.failed
}

func (s *Syncer) applySyncPush(params protocol.SyncPushParams) error {
	if s == nil {
		return ErrControlSessionChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applySyncPushLocked(s.controlSession, params)
}

// ApplySyncPushForSession atomically checks the connection lease and applies
// one ordered push. Stale handlers are ignored without touching dirty state.
func (s *Syncer) ApplySyncPushForSession(
	expected *ControlSession,
	params protocol.SyncPushParams,
) (bool, error) {
	if expected == nil {
		return false, nil
	}
	var applyErr error
	err := s.withCurrentControlSession(expected, func() error {
		applyErr = s.applySyncPushLocked(expected, params)
		return nil
	})
	if errors.Is(err, ErrControlSessionChanged) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, applyErr
}

func (s *Syncer) applySyncPushLocked(expected *ControlSession, params protocol.SyncPushParams) error {
	if params.Entity == events.EntityAgent {
		switch params.Action {
		case events.ActionCreate, events.ActionUpdate, events.ActionDelete:
		default:
			err := fmt.Errorf("unknown agent push action %q", params.Action)
			s.failAgentSync(err)
			return err
		}
		var agent models.Agent
		if err := json.Unmarshal(params.Data, &agent); err != nil {
			decodeErr := fmt.Errorf("decode agent push: %w", err)
			s.failAgentSync(decodeErr)
			return decodeErr
		}
		var cancel context.CancelFunc
		overflow := false
		s.agentStateMu.Lock()
		s.Store.HandleSyncEvent(params.Entity, params.Action, params.Data)
		s.Store.AdvanceVersion(params.Version)
		if builder := s.agentBuilder; builder != nil && builder.session == expected && builder.failed == nil {
			if len(builder.pushes) >= maxAgentPushBuffer {
				builder.failed = errAgentPushBufferOverflow
				s.agentsDirty.Store(true)
				cancel = builder.cancel
				overflow = true
			} else {
				builder.pushes = append(builder.pushes, bufferedAgentPush{
					action: params.Action, agent: agent, version: params.Version,
				})
			}
		}
		s.agentStateMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if overflow {
			return errAgentPushBufferOverflow
		}
		return nil
	}
	if params.Entity != events.EntityAgentRoute {
		s.Store.HandleSyncEvent(params.Entity, params.Action, params.Data)
		s.Store.AdvanceVersion(params.Version)
		return nil
	}
	switch params.Action {
	case events.ActionCreate, events.ActionUpdate, events.ActionDelete:
	default:
		err := fmt.Errorf("unknown agent route push action %q", params.Action)
		s.failAgentRouteSync(err)
		return err
	}

	var route models.AgentRoute
	if err := json.Unmarshal(params.Data, &route); err != nil {
		decodeErr := fmt.Errorf("decode agent route push: %w", err)
		s.failAgentRouteSync(decodeErr)
		return decodeErr
	}

	var cancel context.CancelFunc
	overflow := false
	s.agentRouteStateMu.Lock()
	s.Store.RouteIndex.Apply(params.Action, &route)
	s.Store.AdvanceVersion(params.Version)
	if builder := s.agentRouteBuilder; builder != nil && builder.session == expected && builder.failed == nil {
		if len(builder.pushes) >= maxAgentRoutePushBuffer {
			builder.failed = errAgentRoutePushBufferOverflow
			s.markAgentRoutesDirty()
			cancel = builder.cancel
			overflow = true
		} else {
			builder.pushes = append(builder.pushes, bufferedAgentRoutePush{
				action:  params.Action,
				route:   route,
				version: params.Version,
			})
		}
	}
	s.agentRouteStateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if overflow {
		return errAgentRoutePushBufferOverflow
	}
	return nil
}

func (s *Syncer) markAgentRoutesDirty() {
	if s != nil {
		s.agentRouteDirty.Store(true)
	}
}

func (s *Syncer) failAgentRouteSync(err error) {
	if s == nil || err == nil {
		return
	}
	var cancel context.CancelFunc
	s.agentRouteStateMu.Lock()
	s.agentRouteDirty.Store(true)
	if builder := s.agentRouteBuilder; builder != nil && builder.failed == nil {
		builder.failed = err
		cancel = builder.cancel
	}
	s.agentRouteStateMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RequestFullSync queues at most one pass behind the currently running pass.
func (s *Syncer) RequestFullSync() bool {
	return s.RequestFullSyncForSession(s.CurrentControlSession())
}

func (s *Syncer) RequestFullSyncForSession(expected *ControlSession) bool {
	if expected == nil {
		return false
	}
	requests := s.requestedFullSyncChannel()
	select {
	case requests <- expected:
		return true
	default:
		return false
	}
}

// RunRequestedFullSyncs owns the capacity-one request signal and executes
// requested passes serially until its server-derived context is cancelled.
func (s *Syncer) RunRequestedFullSyncs(ctx context.Context) {
	requests := s.requestedFullSyncChannel()
	for {
		select {
		case <-ctx.Done():
			return
		case expected := <-requests:
			if err := s.FullSyncForSession(ctx, expected); err != nil &&
				!errors.Is(err, ErrControlSessionChanged) && ctx.Err() == nil && s.Logger != nil {
				s.Logger.Error("full sync (requested) failed", zap.Error(err))
			}
		}
	}
}

func (s *Syncer) requestedFullSyncChannel() chan *ControlSession {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if s.fullSyncRequests == nil {
		s.fullSyncRequests = make(chan *ControlSession, 1)
	}
	return s.fullSyncRequests
}

// SubscribeEvents subscribes to local EventBus for sync events from WSBridge
func (s *Syncer) SubscribeEvents() error {
	patterns := []events.Pattern[protocol.SyncPushParams]{
		events.SyncTokenAllPattern,
		events.SyncChannelAllPattern,
		events.SyncModelAllPattern,
		events.SyncModelConfigAllPattern,
		events.SyncSettingAllPattern,
		events.SyncAgentAllPattern,
		events.SyncAgentRouteAllPattern,
		events.SyncRequestLimiterAllPattern,
		events.SyncLimiterBindingAllPattern,
		events.SyncUserGroupAllPattern,
		events.SyncUserAllPattern,
		events.SyncModelRoutingAllPattern,
		events.SyncPrivateChannelAllPattern,
		events.SyncScriptAllPattern,
	}

	var subscriptions []eventbus.Subscription
	for _, pattern := range patterns {
		sub, err := events.SubscribeSyncPushPattern(s.Bus, pattern, func(ctx context.Context, params protocol.SyncPushParams) error {
			expected := controlSessionFromContext(ctx)
			var err error
			if expected == nil {
				err = s.applySyncPush(params)
			} else {
				_, err = s.ApplySyncPushForSession(expected, params)
			}
			if err != nil {
				return err
			}
			s.Logger.Debug("sync event applied",
				zap.String("entity", params.Entity),
				zap.String("action", params.Action),
				zap.Int64("version", params.Version),
			)
			return nil
		})
		if err != nil {
			var rollbackErrors []error
			for _, subscription := range subscriptions {
				rollbackErrors = append(rollbackErrors, subscription.Unsubscribe())
			}
			s.Logger.Error("subscribe sync pattern failed",
				zap.String("pattern", pattern.Value()),
				zap.Error(err),
			)
			return errors.Join(fmt.Errorf("subscribe sync pattern %s: %w", pattern.Value(), err), errors.Join(rollbackErrors...))
		}
		subscriptions = append(subscriptions, sub)
	}
	return nil
}

// StartPeriodicCheck starts periodic version comparison
func (s *Syncer) StartPeriodicCheck(ctx context.Context) {
	go s.RunPeriodicCheck(ctx)
}

func (s *Syncer) RunPeriodicCheck(ctx context.Context) {
	ticker := time.NewTicker(s.FullSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkVersion(ctx)
		}
	}
}

func (s *Syncer) checkVersion(ctx context.Context) {
	expected := s.CurrentControlSession()
	if expected == nil || expected.client == nil {
		return
	}
	sessionCtx, cleanup, err := s.controlSessionContext(ctx, expected)
	if err != nil {
		return
	}
	defer cleanup()
	callCtx, cancel := context.WithTimeout(sessionCtx, 30*time.Second)
	defer cancel()
	result, err := expected.client.Call(callCtx, consts.RPCSyncGetVersion, nil)
	if err != nil {
		if !errors.Is(context.Cause(sessionCtx), ErrControlSessionChanged) {
			s.Logger.Error("version check failed", zap.Error(err))
		}
		return
	}
	var resp protocol.GetVersionResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		s.Logger.Error("version check response invalid", zap.Error(err))
		return
	}

	routesDirty := s.agentRouteDirty.Load()
	agentsDirty := s.agentsDirty.Load()
	if resp.Version != s.Store.Version() || routesDirty || agentsDirty {
		s.Logger.Info("cache divergence, triggering full sync",
			zap.Int64("local", s.Store.Version()),
			zap.Int64("remote", resp.Version),
			zap.Bool("agents_dirty", agentsDirty),
			zap.Bool("agent_routes_dirty", routesDirty),
		)
		_ = s.FullSyncForSession(ctx, expected)
	}
}
