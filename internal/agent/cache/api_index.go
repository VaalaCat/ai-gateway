package cache

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

var (
	ErrAPICacheNotReady   = errors.New("api cache not ready")
	ErrAPIServiceNotFound = errors.New("api service not found")
	ErrAPIRouteNotFound   = errors.New("api route not found")
)

var apiFullCacheEntities = [...]string{
	events.EntityAPIService,
	events.EntityAPIRoute,
	events.EntityAPIUpstream,
	events.EntityAPIRole,
	events.EntityUserGroupAPIRoleSet,
}

type ServiceRoute struct {
	Service protocol.SyncedAPIService
	Route   protocol.SyncedAPIRoute
}

type apiRouteKey struct {
	serviceID uint
	slug      string
}

type apiSnapshot struct {
	servicesBySlug         map[string]protocol.SyncedAPIService
	servicesByID           map[uint]protocol.SyncedAPIService
	routesByKey            map[apiRouteKey]protocol.SyncedAPIRoute
	routesByID             map[uint]protocol.SyncedAPIRoute
	upstreamsByBackendID   map[uint][]protocol.SyncedAPIUpstream
	rolesByID              map[uint]protocol.SyncedAPIRole
	groupRoleSetsByGroupID map[uint]protocol.APIRoleSetFetchResult
	readyEntities          map[string]bool
}

type APIIndex struct {
	mu       sync.Mutex
	snapshot atomic.Pointer[apiSnapshot]
}

func NewAPIIndex() *APIIndex {
	index := &APIIndex{}
	index.snapshot.Store(newAPISnapshot())
	return index
}

func newAPISnapshot() *apiSnapshot {
	return &apiSnapshot{
		servicesBySlug:         make(map[string]protocol.SyncedAPIService),
		servicesByID:           make(map[uint]protocol.SyncedAPIService),
		routesByKey:            make(map[apiRouteKey]protocol.SyncedAPIRoute),
		routesByID:             make(map[uint]protocol.SyncedAPIRoute),
		upstreamsByBackendID:   make(map[uint][]protocol.SyncedAPIUpstream),
		rolesByID:              make(map[uint]protocol.SyncedAPIRole),
		groupRoleSetsByGroupID: make(map[uint]protocol.APIRoleSetFetchResult),
		readyEntities:          make(map[string]bool, len(apiFullCacheEntities)),
	}
}

func (i *APIIndex) RequireReady() error {
	return requireAPISnapshotReady(i.load())
}

func (i *APIIndex) AllReady() bool {
	return requireAPISnapshotReady(i.load()) == nil
}

func (i *APIIndex) AnyDirty() bool {
	return !i.AllReady()
}

func requireAPISnapshotReady(snapshot *apiSnapshot) error {
	for _, entity := range apiFullCacheEntities {
		if !snapshot.readyEntities[entity] {
			return ErrAPICacheNotReady
		}
	}
	return nil
}

func (i *APIIndex) ResetReadiness() {
	i.update(func(next *apiSnapshot) error {
		next.readyEntities = make(map[string]bool, len(apiFullCacheEntities))
		return nil
	})
}

func (i *APIIndex) MarkDirty(entity string) {
	i.update(func(next *apiSnapshot) error {
		delete(next.readyEntities, entity)
		return nil
	})
}

func (i *APIIndex) FindServiceRoute(serviceSlug, routeSlug string) (ServiceRoute, error) {
	snapshot := i.load()
	if err := requireAPISnapshotReady(snapshot); err != nil {
		return ServiceRoute{}, err
	}
	service, ok := snapshot.servicesBySlug[serviceSlug]
	if !ok {
		return ServiceRoute{}, ErrAPIServiceNotFound
	}
	route, ok := snapshot.routesByKey[apiRouteKey{serviceID: service.ID, slug: routeSlug}]
	if !ok {
		return ServiceRoute{}, ErrAPIRouteNotFound
	}
	return ServiceRoute{Service: service, Route: cloneAPIRoute(route)}, nil
}

// FindServiceRouteByID returns the immutable execution projection named by a
// Source-frozen service/route pair. It does not perform authorization.
func (i *APIIndex) FindServiceRouteByID(serviceID, routeID uint) (ServiceRoute, error) {
	snapshot := i.load()
	if err := requireAPISnapshotReady(snapshot); err != nil {
		return ServiceRoute{}, err
	}
	service, ok := snapshot.servicesByID[serviceID]
	if !ok {
		return ServiceRoute{}, ErrAPIServiceNotFound
	}
	route, ok := snapshot.routesByID[routeID]
	if !ok || route.ServiceID != serviceID {
		return ServiceRoute{}, ErrAPIRouteNotFound
	}
	return ServiceRoute{Service: service, Route: cloneAPIRoute(route)}, nil
}

func (i *APIIndex) UpstreamsForBackend(backendID uint) []protocol.SyncedAPIUpstream {
	snapshot := i.load()
	if requireAPISnapshotReady(snapshot) != nil {
		return nil
	}
	return cloneAPIUpstreams(snapshot.upstreamsByBackendID[backendID])
}

func (i *APIIndex) UserGroupRoleSet(groupID uint) protocol.APIRoleSetFetchResult {
	return cloneAPIRoleSetResult(i.load().groupRoleSetsByGroupID[groupID])
}

func (i *APIIndex) AllowsInvoke(roleIDs []uint, serviceID, routeID uint) bool {
	return apiSnapshotAllowsInvoke(i.load(), roleIDs, serviceID, routeID)
}

func (i *APIIndex) CheckInvoke(roleIDs []uint, serviceID, routeID uint) (bool, error) {
	snapshot := i.load()
	if err := requireAPISnapshotReady(snapshot); err != nil {
		return false, err
	}
	return apiSnapshotAllowsInvoke(snapshot, roleIDs, serviceID, routeID), nil
}

func apiSnapshotAllowsInvoke(snapshot *apiSnapshot, roleIDs []uint, serviceID, routeID uint) bool {
	roles := snapshot.rolesByID
	for _, roleID := range roleIDs {
		role, ok := roles[roleID]
		if !ok {
			continue
		}
		for _, grant := range role.Permissions {
			if grant.Action != "invoke" {
				continue
			}
			if grant.Resource == "api_service" && (grant.ResourceID == 0 || grant.ResourceID == serviceID) {
				return true
			}
			if grant.Resource == "api_route" && grant.ResourceID != 0 && grant.ResourceID == routeID {
				return true
			}
		}
	}
	return false
}

func (i *APIIndex) ReplaceServices(values []protocol.SyncedAPIService) error {
	return i.update(func(next *apiSnapshot) error {
		bySlug := make(map[string]protocol.SyncedAPIService, len(values))
		byID := make(map[uint]protocol.SyncedAPIService, len(values))
		for _, value := range values {
			if _, exists := bySlug[value.Slug]; exists {
				return fmt.Errorf("duplicate API service slug %q", value.Slug)
			}
			if _, exists := byID[value.ID]; exists {
				return fmt.Errorf("duplicate API service id %d", value.ID)
			}
			bySlug[value.Slug] = value
			byID[value.ID] = value
		}
		next.servicesBySlug = bySlug
		next.servicesByID = byID
		next.readyEntities[events.EntityAPIService] = true
		return nil
	})
}

func (i *APIIndex) ReplaceRoutes(values []protocol.SyncedAPIRoute) error {
	return i.update(func(next *apiSnapshot) error {
		byKey := make(map[apiRouteKey]protocol.SyncedAPIRoute, len(values))
		byID := make(map[uint]protocol.SyncedAPIRoute, len(values))
		for _, value := range values {
			if _, ok := next.servicesByID[value.ServiceID]; !ok {
				return fmt.Errorf("API route %d references missing service %d", value.ID, value.ServiceID)
			}
			key := apiRouteKey{serviceID: value.ServiceID, slug: value.Slug}
			if _, exists := byKey[key]; exists {
				return fmt.Errorf("duplicate API route slug %q for service %d", value.Slug, value.ServiceID)
			}
			value = cloneAPIRoute(value)
			byKey[key] = value
			byID[value.ID] = value
		}
		next.routesByKey = byKey
		next.routesByID = byID
		next.readyEntities[events.EntityAPIRoute] = true
		return nil
	})
}

func (i *APIIndex) ReplaceUpstreams(values []protocol.SyncedAPIUpstream) error {
	return i.update(func(next *apiSnapshot) error {
		byBackend := make(map[uint][]protocol.SyncedAPIUpstream)
		seen := make(map[uint]struct{}, len(values))
		for _, value := range values {
			if value.BackendID == 0 {
				return fmt.Errorf("API upstream %d references missing backend", value.ID)
			}
			if _, duplicate := seen[value.ID]; duplicate {
				return fmt.Errorf("duplicate API upstream id %d", value.ID)
			}
			seen[value.ID] = struct{}{}
			byBackend[value.BackendID] = append(byBackend[value.BackendID], cloneAPIUpstream(value))
		}
		for backendID := range byBackend {
			sort.Slice(byBackend[backendID], func(a, b int) bool { return byBackend[backendID][a].ID < byBackend[backendID][b].ID })
		}
		next.upstreamsByBackendID = byBackend
		next.readyEntities[events.EntityAPIUpstream] = true
		return nil
	})
}

func (i *APIIndex) ReplaceRoles(values []protocol.SyncedAPIRole) error {
	return i.update(func(next *apiSnapshot) error {
		roles := make(map[uint]protocol.SyncedAPIRole, len(values))
		for _, value := range values {
			roles[value.ID] = cloneAPIRole(value)
		}
		next.rolesByID = roles
		next.readyEntities[events.EntityAPIRole] = true
		return nil
	})
}

func (i *APIIndex) ReplaceUserGroupRoleSets(values []protocol.APIRoleSetFetchResult) error {
	return i.update(func(next *apiSnapshot) error {
		roleSets := make(map[uint]protocol.APIRoleSetFetchResult, len(values))
		for _, value := range values {
			roleSets[value.PrincipalID] = cloneAPIRoleSetResult(value)
		}
		next.groupRoleSetsByGroupID = roleSets
		next.readyEntities[events.EntityUserGroupAPIRoleSet] = true
		return nil
	})
}

func (i *APIIndex) ApplyService(action string, value protocol.SyncedAPIService) error {
	return i.update(func(next *apiSnapshot) error {
		if err := validateAPIAction(action); err != nil {
			return err
		}
		if old, ok := next.servicesByID[value.ID]; ok {
			delete(next.servicesBySlug, old.Slug)
		}
		if action == events.ActionDelete {
			delete(next.servicesByID, value.ID)
			delete(next.servicesBySlug, value.Slug)
			return nil
		}
		next.servicesByID[value.ID] = value
		next.servicesBySlug[value.Slug] = value
		return nil
	})
}

func (i *APIIndex) ApplyRoute(action string, value protocol.SyncedAPIRoute) error {
	return i.update(func(next *apiSnapshot) error {
		if err := validateAPIAction(action); err != nil {
			return err
		}
		if old, ok := next.routesByID[value.ID]; ok {
			delete(next.routesByKey, apiRouteKey{serviceID: old.ServiceID, slug: old.Slug})
		}
		if action == events.ActionDelete {
			delete(next.routesByID, value.ID)
			delete(next.routesByKey, apiRouteKey{serviceID: value.ServiceID, slug: value.Slug})
			return nil
		}
		value = cloneAPIRoute(value)
		next.routesByID[value.ID] = value
		next.routesByKey[apiRouteKey{serviceID: value.ServiceID, slug: value.Slug}] = value
		return nil
	})
}

func (i *APIIndex) ApplyUpstream(action string, value protocol.SyncedAPIUpstream) error {
	return i.update(func(next *apiSnapshot) error {
		if err := validateAPIAction(action); err != nil {
			return err
		}
		if action != events.ActionDelete && value.BackendID == 0 {
			return fmt.Errorf("API upstream %d references missing backend", value.ID)
		}
		for backendID, values := range next.upstreamsByBackendID {
			filtered := values[:0]
			for _, existing := range values {
				if existing.ID != value.ID {
					filtered = append(filtered, existing)
				}
			}
			next.upstreamsByBackendID[backendID] = filtered
		}
		if action != events.ActionDelete {
			next.upstreamsByBackendID[value.BackendID] = append(next.upstreamsByBackendID[value.BackendID], cloneAPIUpstream(value))
		}
		return nil
	})
}

func (i *APIIndex) ApplyRole(action string, value protocol.SyncedAPIRole) error {
	return i.update(func(next *apiSnapshot) error {
		if err := validateAPIAction(action); err != nil {
			return err
		}
		if action == events.ActionDelete {
			delete(next.rolesByID, value.ID)
		} else {
			next.rolesByID[value.ID] = cloneAPIRole(value)
		}
		return nil
	})
}

func (i *APIIndex) ApplyUserGroupRoleSet(action string, value protocol.APIRoleSetFetchResult) error {
	return i.update(func(next *apiSnapshot) error {
		if action != events.ActionUpdate && action != events.ActionDelete {
			return fmt.Errorf("unknown API role set push action %q", action)
		}
		if action == events.ActionDelete || !value.Exists {
			delete(next.groupRoleSetsByGroupID, value.PrincipalID)
		} else {
			next.groupRoleSetsByGroupID[value.PrincipalID] = cloneAPIRoleSetResult(value)
		}
		return nil
	})
}

func validateAPIAction(action string) error {
	if action != events.ActionCreate && action != events.ActionUpdate && action != events.ActionDelete {
		return fmt.Errorf("unknown API push action %q", action)
	}
	return nil
}

func (i *APIIndex) update(change func(*apiSnapshot) error) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	next := cloneAPISnapshot(i.load())
	if err := change(next); err != nil {
		return err
	}
	i.snapshot.Store(next)
	return nil
}

func (i *APIIndex) load() *apiSnapshot {
	if snapshot := i.snapshot.Load(); snapshot != nil {
		return snapshot
	}
	return newAPISnapshot()
}

func cloneAPISnapshot(source *apiSnapshot) *apiSnapshot {
	next := newAPISnapshot()
	for key, value := range source.servicesBySlug {
		next.servicesBySlug[key] = value
	}
	for key, value := range source.servicesByID {
		next.servicesByID[key] = value
	}
	for key, value := range source.routesByKey {
		next.routesByKey[key] = cloneAPIRoute(value)
	}
	for key, value := range source.routesByID {
		next.routesByID[key] = cloneAPIRoute(value)
	}
	for key, values := range source.upstreamsByBackendID {
		next.upstreamsByBackendID[key] = cloneAPIUpstreams(values)
	}
	for key, value := range source.rolesByID {
		next.rolesByID[key] = cloneAPIRole(value)
	}
	for key, value := range source.groupRoleSetsByGroupID {
		next.groupRoleSetsByGroupID[key] = cloneAPIRoleSetResult(value)
	}
	for key, value := range source.readyEntities {
		next.readyEntities[key] = value
	}
	return next
}

func cloneAPIRoute(value protocol.SyncedAPIRoute) protocol.SyncedAPIRoute {
	value.Protocols = append([]string(nil), value.Protocols...)
	value.AllowedMethods = append([]string(nil), value.AllowedMethods...)
	value.WebSocketSubprotocols = append([]string(nil), value.WebSocketSubprotocols...)
	return value
}

func cloneAPIUpstream(value protocol.SyncedAPIUpstream) protocol.SyncedAPIUpstream {
	if value.HeaderOverride != nil {
		headers := make(map[string]string, len(value.HeaderOverride))
		for key, header := range value.HeaderOverride {
			headers[key] = header
		}
		value.HeaderOverride = headers
	}
	return value
}

func cloneAPIUpstreams(values []protocol.SyncedAPIUpstream) []protocol.SyncedAPIUpstream {
	cloned := make([]protocol.SyncedAPIUpstream, len(values))
	for index, value := range values {
		cloned[index] = cloneAPIUpstream(value)
	}
	return cloned
}

func cloneAPIRole(value protocol.SyncedAPIRole) protocol.SyncedAPIRole {
	value.Permissions = append([]protocol.APIPermissionGrant(nil), value.Permissions...)
	return value
}

func cloneAPIRoleSetResult(value protocol.APIRoleSetFetchResult) protocol.APIRoleSetFetchResult {
	value.RoleSet.RoleIDs = append([]uint{}, value.RoleSet.RoleIDs...)
	return value
}
