package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const maxAPIPushBuffer = 4096

var errAPIPushBufferOverflow = errors.New("API full sync push buffer overflow")

type bufferedAPIPush struct {
	action  string
	value   any
	version int64
}

type apiObjectVersion struct {
	version   int64
	tombstone bool
}

type apiEntityVersionState struct {
	floor   int64
	objects map[uint]apiObjectVersion
}

type apiEntitySyncBuilder struct {
	items       any
	pushes      []bufferedAPIPush
	baseVersion int64
	baseSet     bool
	failed      error
	cancel      context.CancelFunc
	session     *ControlSession
}

type apiEntitySyncHandler interface {
	decodeItems(json.RawMessage) (any, error)
	decodePush(json.RawMessage) (any, error)
	objectID(any) (uint, error)
	pageIDs(any) ([]uint, error)
	append(any, any) any
	apply(*APIIndex, string, any) error
	replace(*APIIndex, any) error
	merge(any, []bufferedAPIPush, int64) (any, []bufferedAPIPush, error)
}

type typedAPIEntitySyncHandler[T any] struct {
	id        func(T) uint
	applyFn   func(*APIIndex, string, T) error
	replaceFn func(*APIIndex, []T) error
}

func (h typedAPIEntitySyncHandler[T]) decodeItems(raw json.RawMessage) (any, error) {
	var values []T
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (h typedAPIEntitySyncHandler[T]) decodePush(raw json.RawMessage) (any, error) {
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (h typedAPIEntitySyncHandler[T]) objectID(value any) (uint, error) {
	typed, ok := value.(T)
	if !ok {
		return 0, fmt.Errorf("invalid API push value %T", value)
	}
	return h.id(typed), nil
}

func (h typedAPIEntitySyncHandler[T]) pageIDs(values any) ([]uint, error) {
	typed, ok := values.([]T)
	if !ok {
		return nil, fmt.Errorf("invalid API snapshot page %T", values)
	}
	ids := make([]uint, 0, len(typed))
	for _, value := range typed {
		ids = append(ids, h.id(value))
	}
	return ids, nil
}

func (h typedAPIEntitySyncHandler[T]) append(base, page any) any {
	prior, _ := base.([]T)
	return append(prior, page.([]T)...)
}

func (h typedAPIEntitySyncHandler[T]) apply(index *APIIndex, action string, value any) error {
	typed, ok := value.(T)
	if !ok {
		return fmt.Errorf("invalid API push value %T", value)
	}
	return h.applyFn(index, action, typed)
}

func (h typedAPIEntitySyncHandler[T]) replace(index *APIIndex, values any) error {
	typed, ok := values.([]T)
	if !ok {
		return fmt.Errorf("invalid API snapshot value %T", values)
	}
	return h.replaceFn(index, typed)
}

func (h typedAPIEntitySyncHandler[T]) merge(
	base any,
	pushes []bufferedAPIPush,
	baseVersion int64,
) (any, []bufferedAPIPush, error) {
	values, ok := base.([]T)
	if !ok {
		return nil, nil, fmt.Errorf("invalid API snapshot value %T", base)
	}
	byID := make(map[uint]T, len(values)+len(pushes))
	for _, value := range values {
		byID[h.id(value)] = value
	}
	ordered := append([]bufferedAPIPush(nil), pushes...)
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].version < ordered[b].version })
	accepted := make([]bufferedAPIPush, 0, len(ordered))
	latest := make(map[uint]int64, len(ordered))
	for _, push := range ordered {
		if push.version <= baseVersion {
			continue
		}
		value, ok := push.value.(T)
		if !ok {
			return nil, nil, fmt.Errorf("invalid API push value %T", push.value)
		}
		id := h.id(value)
		if version, exists := latest[id]; exists && push.version <= version {
			continue
		}
		if push.action == events.ActionDelete {
			delete(byID, id)
		} else {
			byID[id] = value
		}
		latest[id] = push.version
		accepted = append(accepted, push)
	}
	ids := make([]uint, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	result := make([]T, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result, accepted, nil
}

type apiSyncCoordinator struct {
	index    *APIIndex
	handlers map[string]apiEntitySyncHandler
	syncMu   sync.Mutex
	stateMu  sync.Mutex
	builders map[string]*apiEntitySyncBuilder
	versions map[string]apiEntityVersionState
}

func newAPISyncCoordinator(index *APIIndex) *apiSyncCoordinator {
	coordinator := &apiSyncCoordinator{
		index:    index,
		builders: make(map[string]*apiEntitySyncBuilder),
		versions: make(map[string]apiEntityVersionState),
	}
	coordinator.handlers = map[string]apiEntitySyncHandler{
		events.EntityAPIService: typedAPIEntitySyncHandler[protocol.SyncedAPIService]{
			id: func(value protocol.SyncedAPIService) uint { return value.ID },
			applyFn: func(index *APIIndex, action string, value protocol.SyncedAPIService) error {
				return index.ApplyService(action, value)
			},
			replaceFn: func(index *APIIndex, values []protocol.SyncedAPIService) error { return index.ReplaceServices(values) },
		},
		events.EntityAPIRoute: typedAPIEntitySyncHandler[protocol.SyncedAPIRoute]{
			id: func(value protocol.SyncedAPIRoute) uint { return value.ID },
			applyFn: func(index *APIIndex, action string, value protocol.SyncedAPIRoute) error {
				return index.ApplyRoute(action, value)
			},
			replaceFn: func(index *APIIndex, values []protocol.SyncedAPIRoute) error { return index.ReplaceRoutes(values) },
		},
		events.EntityAPIUpstream: typedAPIEntitySyncHandler[protocol.SyncedAPIUpstream]{
			id: func(value protocol.SyncedAPIUpstream) uint { return value.ID },
			applyFn: func(index *APIIndex, action string, value protocol.SyncedAPIUpstream) error {
				return index.ApplyUpstream(action, value)
			},
			replaceFn: func(index *APIIndex, values []protocol.SyncedAPIUpstream) error {
				return index.ReplaceUpstreams(values)
			},
		},
		events.EntityAPIRole: typedAPIEntitySyncHandler[protocol.SyncedAPIRole]{
			id: func(value protocol.SyncedAPIRole) uint { return value.ID },
			applyFn: func(index *APIIndex, action string, value protocol.SyncedAPIRole) error {
				return index.ApplyRole(action, value)
			},
			replaceFn: func(index *APIIndex, values []protocol.SyncedAPIRole) error { return index.ReplaceRoles(values) },
		},
		events.EntityUserGroupAPIRoleSet: typedAPIEntitySyncHandler[protocol.APIRoleSetFetchResult]{
			id: func(value protocol.APIRoleSetFetchResult) uint { return value.PrincipalID },
			applyFn: func(index *APIIndex, action string, value protocol.APIRoleSetFetchResult) error {
				return index.ApplyUserGroupRoleSet(action, value)
			},
			replaceFn: func(index *APIIndex, values []protocol.APIRoleSetFetchResult) error {
				return index.ReplaceUserGroupRoleSets(values)
			},
		},
	}
	return coordinator
}

func (c *apiSyncCoordinator) resolve(entity string) (apiEntitySyncHandler, bool) {
	handler, ok := c.handlers[entity]
	return handler, ok
}

func (c *apiSyncCoordinator) AllReady() bool {
	return c != nil && c.index != nil && c.index.AllReady()
}

func (c *apiSyncCoordinator) AnyDirty() bool {
	return !c.AllReady()
}

func (c *apiSyncCoordinator) fullSync(
	ctx context.Context,
	syncer *Syncer,
	session *ControlSession,
	entity string,
) error {
	handler, ok := c.resolve(entity)
	if !ok {
		return fmt.Errorf("unsupported API full sync entity %q", entity)
	}
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.index.MarkDirty(entity)
	pullCtx, builder, cancel, err := c.beginBuilder(ctx, syncer, session, entity)
	if err != nil {
		return err
	}
	defer cancel()
	defer c.clearBuilder(entity, builder)
	items, maxVersion, err := c.pullSnapshot(pullCtx, session, entity, handler, builder)
	if err != nil {
		return err
	}
	builder.items = items
	return c.finalize(pullCtx, syncer, entity, handler, builder, maxVersion)
}

func (c *apiSyncCoordinator) beginBuilder(
	ctx context.Context,
	syncer *Syncer,
	session *ControlSession,
	entity string,
) (context.Context, *apiEntitySyncBuilder, context.CancelFunc, error) {
	if session == nil || session.client == nil {
		return nil, nil, nil, errors.New("no ws client")
	}
	pullCtx, cancel := context.WithCancel(ctx)
	builder := &apiEntitySyncBuilder{pushes: make([]bufferedAPIPush, 0, maxAPIPushBuffer), cancel: cancel, session: session}
	err := syncer.withCurrentControlSession(session, func() error {
		c.stateMu.Lock()
		c.builders[entity] = builder
		c.stateMu.Unlock()
		return nil
	})
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	return pullCtx, builder, cancel, nil
}

func (c *apiSyncCoordinator) pullSnapshot(
	ctx context.Context,
	session *ControlSession,
	entity string,
	handler apiEntitySyncHandler,
	builder *apiEntitySyncBuilder,
) (any, int64, error) {
	request := protocol.FullSyncRequest{Entity: entity, PageSize: protocol.FullSyncMaxPageSize}
	var snapshotMaxID uint
	var baseVersion int64
	var maxResponseVersion int64
	snapshotSet := false
	var allItems any
	for {
		callCtx, callCancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := session.client.Call(callCtx, consts.RPCSyncFullSync, request)
		callCancel()
		if builderErr := c.builderError(entity, builder); builderErr != nil {
			return nil, 0, builderErr
		}
		if err != nil {
			return nil, 0, err
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, 0, cause
		}
		var response protocol.FullSyncResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return nil, 0, err
		}
		if response.SnapshotContract != protocol.APIFullSyncSnapshotContractV1 {
			return nil, 0, fmt.Errorf("API full sync unsupported snapshot contract %q", response.SnapshotContract)
		}
		if !response.Keyset {
			return nil, 0, errors.New("API full sync requires keyset snapshot")
		}
		if !snapshotSet {
			snapshotSet = true
			snapshotMaxID = response.SnapshotMaxID
			baseVersion = response.BaseVersion
			c.stateMu.Lock()
			builder.baseVersion = baseVersion
			builder.baseSet = true
			c.stateMu.Unlock()
		} else if response.SnapshotMaxID != snapshotMaxID || response.BaseVersion != baseVersion {
			return nil, 0, errors.New("API full sync keyset snapshot changed between pages")
		}
		if response.Version > maxResponseVersion {
			maxResponseVersion = response.Version
		}
		items, err := handler.decodeItems(response.Items)
		if err != nil {
			return nil, 0, err
		}
		if err := validateAPISnapshotPage(handler, items, request.AfterID, response); err != nil {
			return nil, 0, err
		}
		allItems = handler.append(allItems, items)
		if !response.HasMore {
			break
		}
		request = protocol.FullSyncRequest{
			Entity: entity, PageSize: protocol.FullSyncMaxPageSize, AfterID: response.LastID,
			SnapshotMaxID: snapshotMaxID, BaseVersion: baseVersion,
		}
	}
	return allItems, maxResponseVersion, nil
}

func validateAPISnapshotPage(
	handler apiEntitySyncHandler,
	items any,
	afterID uint,
	response protocol.FullSyncResponse,
) error {
	ids, err := handler.pageIDs(items)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		if response.HasMore {
			return errors.New("API full sync empty page cannot have more pages")
		}
		if response.LastID != 0 {
			return fmt.Errorf("API full sync empty final page last id must be zero: got=%d", response.LastID)
		}
		return nil
	}
	if response.LastID > response.SnapshotMaxID {
		return fmt.Errorf(
			"API full sync last id exceeds snapshot max: last=%d max=%d",
			response.LastID,
			response.SnapshotMaxID,
		)
	}
	previous := afterID
	for index, id := range ids {
		if index == 0 && id <= afterID {
			return fmt.Errorf("API full sync first item id must be after cursor: after=%d id=%d", afterID, id)
		}
		if index > 0 && id <= previous {
			return fmt.Errorf("API full sync page ids must be strictly increasing: previous=%d id=%d", previous, id)
		}
		if id > response.SnapshotMaxID {
			return fmt.Errorf(
				"API full sync item id exceeds snapshot window: id=%d max=%d",
				id,
				response.SnapshotMaxID,
			)
		}
		previous = id
	}
	if response.LastID != ids[len(ids)-1] {
		return fmt.Errorf(
			"API full sync last id must equal page tail: last=%d tail=%d",
			response.LastID,
			ids[len(ids)-1],
		)
	}
	return nil
}

func (c *apiSyncCoordinator) finalize(
	ctx context.Context,
	syncer *Syncer,
	entity string,
	handler apiEntitySyncHandler,
	builder *apiEntitySyncBuilder,
	maxResponseVersion int64,
) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return syncer.withCurrentControlSession(builder.session, func() error {
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
		if c.builders[entity] != builder {
			return errors.New("API full sync builder is no longer active")
		}
		if builder.failed != nil {
			return builder.failed
		}
		finalItems, replayed, err := handler.merge(builder.items, builder.pushes, builder.baseVersion)
		if err != nil {
			return err
		}
		state := apiEntityVersionState{floor: builder.baseVersion, objects: make(map[uint]apiObjectVersion)}
		for _, push := range replayed {
			id, err := handler.objectID(push.value)
			if err != nil {
				return err
			}
			state.objects[id] = apiObjectVersion{
				version: push.version, tombstone: push.action == events.ActionDelete,
			}
		}
		if err := handler.replace(c.index, finalItems); err != nil {
			return err
		}
		// behavior change: FullSync publishes the snapshot and its per-object
		// version floor atomically under stateMu.
		c.versions[entity] = state
		syncer.Store.AdvanceVersion(maxResponseVersion)
		delete(c.builders, entity)
		return nil
	})
}

func (c *apiSyncCoordinator) applyPush(
	syncer *Syncer,
	session *ControlSession,
	params protocol.SyncPushParams,
) error {
	handler, ok := c.resolve(params.Entity)
	if !ok {
		return fmt.Errorf("unsupported API push entity %q", params.Entity)
	}
	c.stateMu.Lock()
	builder := c.builders[params.Entity]
	building := builder != nil && builder.session == session
	// An active FullSync builder buffers accepted pushes without publishing them.
	if building {
		if builder.failed != nil {
			failure := builder.failed
			c.stateMu.Unlock()
			return failure
		}
		if builder.baseSet && params.Version <= builder.baseVersion {
			c.stateMu.Unlock()
			return nil
		}
	} else if state, exists := c.versions[params.Entity]; exists && params.Version <= state.floor {
		c.stateMu.Unlock()
		return nil
	}

	value, err := handler.decodePush(params.Data)
	if err != nil {
		c.stateMu.Unlock()
		return c.fail(params.Entity, fmt.Errorf("decode %s push: %w", params.Entity, err))
	}
	if building {
		if err := validateAPIAction(params.Action); err != nil {
			c.stateMu.Unlock()
			return c.fail(params.Entity, err)
		}
		if len(builder.pushes) >= maxAPIPushBuffer {
			builder.failed = errAPIPushBufferOverflow
			cancel := builder.cancel
			c.stateMu.Unlock()
			c.index.MarkDirty(params.Entity)
			cancel()
			return errAPIPushBufferOverflow
		}
		builder.pushes = append(builder.pushes, bufferedAPIPush{
			action: params.Action, value: value, version: params.Version,
		})
		syncer.Store.AdvanceVersion(params.Version)
		c.stateMu.Unlock()
		return nil
	}
	objectID, err := handler.objectID(value)
	if err != nil {
		c.stateMu.Unlock()
		return c.fail(params.Entity, err)
	}
	state := c.versions[params.Entity]
	if object, exists := state.objects[objectID]; exists && params.Version <= object.version {
		c.stateMu.Unlock()
		return nil
	}

	if err := handler.apply(c.index, params.Action, value); err != nil {
		c.stateMu.Unlock()
		return c.fail(params.Entity, err)
	}
	if state.objects == nil {
		state.objects = make(map[uint]apiObjectVersion)
	}
	// behavior change: API push staleness is tracked per entity/object, not by
	// the Store's global observational version.
	state.objects[objectID] = apiObjectVersion{
		version: params.Version, tombstone: params.Action == events.ActionDelete,
	}
	c.versions[params.Entity] = state
	syncer.Store.AdvanceVersion(params.Version)
	c.stateMu.Unlock()
	return nil
}

func (c *apiSyncCoordinator) fail(entity string, failure error) error {
	var cancel context.CancelFunc
	c.stateMu.Lock()
	if builder := c.builders[entity]; builder != nil && builder.failed == nil {
		builder.failed = failure
		cancel = builder.cancel
	}
	c.stateMu.Unlock()
	c.index.MarkDirty(entity)
	if cancel != nil {
		cancel()
	}
	return failure
}

func (c *apiSyncCoordinator) markAllDirty() {
	for _, entity := range apiFullCacheEntities {
		c.fail(entity, ErrControlSessionChanged)
	}
}

func (c *apiSyncCoordinator) clearBuilder(entity string, builder *apiEntitySyncBuilder) {
	c.stateMu.Lock()
	if c.builders[entity] == builder {
		delete(c.builders, entity)
	}
	c.stateMu.Unlock()
}

func (c *apiSyncCoordinator) builderError(entity string, builder *apiEntitySyncBuilder) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.builders[entity] != builder {
		return errors.New("API full sync builder is no longer active")
	}
	return builder.failed
}
