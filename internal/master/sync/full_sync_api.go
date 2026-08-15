package sync

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type APIFullSyncHandler interface {
	FullSync(context.Context, dao.AdminQuery, protocol.FullSyncRequest) (protocol.FullSyncResponse, error)
}

type APIFullSyncRegistry struct {
	handlers map[string]APIFullSyncHandler
}

func NewAPIFullSyncRegistry(cipher *byokcrypto.Cipher, getVersion func() int64) *APIFullSyncRegistry {
	projector := NewAPIProjector(cipher)
	registry := &APIFullSyncRegistry{handlers: make(map[string]APIFullSyncHandler, 5)}
	registry.Register(events.EntityAPIService, newAPIServiceFullSyncHandler(projector, getVersion))
	registry.Register(events.EntityAPIRoute, newAPIRouteFullSyncHandler(projector, getVersion))
	registry.Register(events.EntityAPIUpstream, newAPIUpstreamFullSyncHandler(projector, getVersion))
	registry.Register(events.EntityAPIRole, newAPIRoleFullSyncHandler(getVersion))
	registry.Register(events.EntityUserGroupAPIRoleSet, newUserGroupRoleSetFullSyncHandler(getVersion))
	return registry
}

func (r *APIFullSyncRegistry) Register(entity string, handler APIFullSyncHandler) {
	r.handlers[entity] = handler
}

func (r *APIFullSyncRegistry) Resolve(entity string) (APIFullSyncHandler, bool) {
	handler, ok := r.handlers[entity]
	return handler, ok
}

type apiFullSyncPage struct {
	items  any
	length int
	lastID uint
}

type registeredAPIFullSyncHandler struct {
	getVersion func() int64
	maxID      func(dao.AdminQuery) (uint, error)
	count      func(dao.AdminQuery, uint) (int64, error)
	load       func(context.Context, dao.AdminQuery, uint, uint, int) (apiFullSyncPage, error)
}

func (h *registeredAPIFullSyncHandler) FullSync(
	ctx context.Context,
	q dao.AdminQuery,
	request protocol.FullSyncRequest,
) (protocol.FullSyncResponse, error) {
	pageSize := request.PageSize
	if pageSize <= 0 || pageSize > protocol.FullSyncMaxPageSize {
		pageSize = protocol.FullSyncMaxPageSize
	}
	snapshotMaxID, baseVersion := request.SnapshotMaxID, request.BaseVersion
	if snapshotMaxID == 0 {
		baseVersion = h.getVersion()
		var err error
		snapshotMaxID, err = h.maxID(q)
		if err != nil {
			return protocol.FullSyncResponse{}, err
		}
	}
	page, err := h.load(ctx, q, request.AfterID, snapshotMaxID, pageSize)
	if err != nil {
		return protocol.FullSyncResponse{}, err
	}
	total, err := h.count(q, snapshotMaxID)
	if err != nil {
		return protocol.FullSyncResponse{}, err
	}
	items, err := json.Marshal(page.items)
	if err != nil {
		return protocol.FullSyncResponse{}, err
	}
	return protocol.FullSyncResponse{
		Items: items, Total: total, HasMore: page.length == pageSize && page.lastID < snapshotMaxID,
		Version: h.getVersion(), Keyset: true, LastID: page.lastID,
		SnapshotMaxID: snapshotMaxID, BaseVersion: baseVersion,
		SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
	}, nil
}

func newAPIServiceFullSyncHandler(projector *APIProjector, getVersion func() int64) APIFullSyncHandler {
	return &registeredAPIFullSyncHandler{
		getVersion: getVersion,
		maxID:      func(q dao.AdminQuery) (uint, error) { return q.APIService().MaxID() },
		count:      func(q dao.AdminQuery, max uint) (int64, error) { return q.APIService().CountThroughID(max) },
		load: func(_ context.Context, q dao.AdminQuery, after, max uint, limit int) (apiFullSyncPage, error) {
			rows, err := q.APIService().ListKeyset(after, max, limit)
			if err != nil {
				return apiFullSyncPage{}, err
			}
			items := make([]protocol.SyncedAPIService, len(rows))
			for i, row := range rows {
				items[i] = projector.ProjectService(row)
			}
			return apiFullSyncPage{items: items, length: len(items), lastID: lastServiceID(items)}, nil
		},
	}
}

func newAPIRouteFullSyncHandler(projector *APIProjector, getVersion func() int64) APIFullSyncHandler {
	return &registeredAPIFullSyncHandler{
		getVersion: getVersion,
		maxID:      func(q dao.AdminQuery) (uint, error) { return q.APIRoute().MaxID() },
		count:      func(q dao.AdminQuery, max uint) (int64, error) { return q.APIRoute().CountThroughID(max) },
		load: func(_ context.Context, q dao.AdminQuery, after, max uint, limit int) (apiFullSyncPage, error) {
			rows, err := q.APIRoute().ListKeyset(after, max, limit)
			if err != nil {
				return apiFullSyncPage{}, err
			}
			items := make([]protocol.SyncedAPIRoute, len(rows))
			for i, row := range rows {
				items[i] = projector.ProjectRoute(row)
			}
			lastID := uint(0)
			if len(items) > 0 {
				lastID = items[len(items)-1].ID
			}
			return apiFullSyncPage{items: items, length: len(items), lastID: lastID}, nil
		},
	}
}

func newAPIUpstreamFullSyncHandler(projector *APIProjector, getVersion func() int64) APIFullSyncHandler {
	return &registeredAPIFullSyncHandler{
		getVersion: getVersion,
		maxID:      func(q dao.AdminQuery) (uint, error) { return q.APIUpstream().MaxID() },
		count:      func(q dao.AdminQuery, max uint) (int64, error) { return q.APIUpstream().CountThroughID(max) },
		load: func(_ context.Context, q dao.AdminQuery, after, max uint, limit int) (apiFullSyncPage, error) {
			rows, err := q.APIUpstream().ListKeyset(after, max, limit)
			if err != nil {
				return apiFullSyncPage{}, err
			}
			items := make([]protocol.SyncedAPIUpstream, len(rows))
			for i, row := range rows {
				items[i], err = projector.ProjectUpstream(row)
				if err != nil {
					return apiFullSyncPage{}, err
				}
			}
			lastID := uint(0)
			if len(items) > 0 {
				lastID = items[len(items)-1].ID
			}
			return apiFullSyncPage{items: items, length: len(items), lastID: lastID}, nil
		},
	}
}

func newAPIRoleFullSyncHandler(getVersion func() int64) APIFullSyncHandler {
	return &registeredAPIFullSyncHandler{
		getVersion: getVersion,
		maxID:      func(q dao.AdminQuery) (uint, error) { return q.APIRBAC().MaxEnabledRoleID() },
		count: func(q dao.AdminQuery, max uint) (int64, error) {
			return q.APIRBAC().CountEnabledRolesThroughID(max)
		},
		load: func(ctx context.Context, q dao.AdminQuery, after, max uint, limit int) (apiFullSyncPage, error) {
			items, err := apirbac.NewRoleCompiler(q.APIRBAC()).CompileAPIRolesKeyset(ctx, after, max, limit)
			if err != nil {
				return apiFullSyncPage{}, err
			}
			lastID := uint(0)
			if len(items) > 0 {
				lastID = items[len(items)-1].ID
			}
			return apiFullSyncPage{items: items, length: len(items), lastID: lastID}, nil
		},
	}
}

func newUserGroupRoleSetFullSyncHandler(getVersion func() int64) APIFullSyncHandler {
	return &registeredAPIFullSyncHandler{
		getVersion: getVersion,
		maxID:      func(q dao.AdminQuery) (uint, error) { return q.UserGroup().MaxID() },
		count:      func(q dao.AdminQuery, max uint) (int64, error) { return q.UserGroup().CountThroughID(max) },
		load: func(_ context.Context, q dao.AdminQuery, after, max uint, limit int) (apiFullSyncPage, error) {
			groups, err := q.UserGroup().ListKeyset(after, max, limit)
			if err != nil {
				return apiFullSyncPage{}, err
			}
			bindings, err := q.APIRBAC().ListRoleSetBindingsByPrincipals(
				models.APIPrincipalUserGroup, userGroupIDs(groups),
			)
			if err != nil {
				return apiFullSyncPage{}, err
			}
			items := projectUserGroupRoleSets(groups, bindings)
			lastID := uint(0)
			if len(items) > 0 {
				lastID = items[len(items)-1].PrincipalID
			}
			return apiFullSyncPage{items: items, length: len(items), lastID: lastID}, nil
		},
	}
}

func userGroupIDs(groups []models.UserGroup) []uint {
	ids := make([]uint, len(groups))
	for i, group := range groups {
		ids[i] = group.ID
	}
	return ids
}

func projectUserGroupRoleSets(
	groups []models.UserGroup,
	bindings []models.RoleBinding,
) []protocol.APIRoleSetFetchResult {
	roleIDsByGroup := make(map[uint][]uint, len(groups))
	for _, binding := range bindings {
		roleIDsByGroup[binding.PrincipalID] = append(roleIDsByGroup[binding.PrincipalID], binding.RoleID)
	}
	items := make([]protocol.APIRoleSetFetchResult, len(groups))
	for i, group := range groups {
		roleIDs := roleIDsByGroup[group.ID]
		slices.Sort(roleIDs)
		roleIDs = slices.Compact(roleIDs)
		items[i] = protocol.APIRoleSetFetchResult{
			PrincipalID: group.ID,
			Exists:      true,
			RoleSet:     protocol.APIRoleSet{RoleIDs: append([]uint{}, roleIDs...)},
		}
	}
	return items
}

func lastServiceID(items []protocol.SyncedAPIService) uint {
	if len(items) == 0 {
		return 0
	}
	return items[len(items)-1].ID
}
