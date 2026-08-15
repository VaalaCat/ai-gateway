package sync

import (
	"context"
	"fmt"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type APISyncActions struct {
	bus       app.EventBus
	projector *APIProjector
}

func NewAPISyncActions(bus app.EventBus, cipher *byokcrypto.Cipher) *APISyncActions {
	return &APISyncActions{bus: bus, projector: NewAPIProjector(cipher)}
}

func (a *APISyncActions) PublishService(ctx context.Context, action string, service models.APIService) error {
	return publishAPIAction(ctx, a.bus, action, a.projector.ProjectService(service), map[string]events.Topic[protocol.SyncedAPIService]{
		events.ActionCreate: events.APIServiceCreateTopic,
		events.ActionUpdate: events.APIServiceUpdateTopic,
		events.ActionDelete: events.APIServiceDeleteTopic,
	})
}

func (a *APISyncActions) PublishRoute(ctx context.Context, action string, route models.APIRoute) error {
	return publishAPIAction(ctx, a.bus, action, a.projector.ProjectRoute(route), map[string]events.Topic[protocol.SyncedAPIRoute]{
		events.ActionCreate: events.APIRouteCreateTopic,
		events.ActionUpdate: events.APIRouteUpdateTopic,
		events.ActionDelete: events.APIRouteDeleteTopic,
	})
}

func (a *APISyncActions) PublishUpstream(ctx context.Context, action string, upstream models.APIUpstream) error {
	projection, err := a.projector.ProjectUpstream(upstream)
	if err != nil {
		return err
	}
	return publishAPIAction(ctx, a.bus, action, projection, map[string]events.Topic[protocol.SyncedAPIUpstream]{
		events.ActionCreate: events.APIUpstreamCreateTopic,
		events.ActionUpdate: events.APIUpstreamUpdateTopic,
		events.ActionDelete: events.APIUpstreamDeleteTopic,
	})
}

func (a *APISyncActions) PublishRole(
	ctx context.Context,
	q dao.AdminQuery,
	action string,
	roleID uint,
) error {
	if roleID == 0 {
		return fmt.Errorf("API role id must not be zero")
	}
	projection := protocol.SyncedAPIRole{ID: roleID}
	if action != events.ActionDelete {
		roles, err := apirbac.NewRoleCompiler(q.APIRBAC()).CompileAPIRolesKeyset(ctx, roleID-1, roleID, 1)
		if err != nil {
			return err
		}
		if len(roles) != 1 || roles[0].ID != roleID {
			return fmt.Errorf("enabled API role %d is unavailable", roleID)
		}
		projection = roles[0]
	}
	return publishAPIAction(ctx, a.bus, action, projection, map[string]events.Topic[protocol.SyncedAPIRole]{
		events.ActionCreate: events.APIRoleCreateTopic,
		events.ActionUpdate: events.APIRoleUpdateTopic,
		events.ActionDelete: events.APIRoleDeleteTopic,
	})
}

func (a *APISyncActions) InvalidateUserRoleSet(ctx context.Context, principalID uint) error {
	return events.Publish(ctx, a.bus, events.UserAPIRolesSyncedTopic, protocol.APIRoleSetInvalidate{PrincipalID: principalID})
}

func (a *APISyncActions) InvalidateTokenRoleSet(ctx context.Context, principalID uint) error {
	return events.Publish(ctx, a.bus, events.TokenAPIRolesSyncedTopic, protocol.APIRoleSetInvalidate{PrincipalID: principalID})
}

func (a *APISyncActions) PublishUserGroupRoleSet(ctx context.Context, roleSet protocol.APIRoleSetFetchResult) error {
	roleSet.Exists = true
	roleSet.RoleSet.RoleIDs = append([]uint{}, roleSet.RoleSet.RoleIDs...)
	return events.Publish(ctx, a.bus, events.UserGroupAPIRolesSyncedTopic, roleSet)
}

func (a *APISyncActions) DeleteUserGroupRoleSet(ctx context.Context, principalID uint) error {
	if principalID == 0 {
		return fmt.Errorf("user group API role set principal id must not be zero")
	}
	return events.Publish(ctx, a.bus, events.UserGroupAPIRolesDeletedTopic,
		protocol.APIRoleSetFetchResult{PrincipalID: principalID})
}

func (a *APISyncActions) PublishRoleBindingChange(
	ctx context.Context,
	q dao.AdminQuery,
	principalType models.APIPrincipalType,
	principalID uint,
) error {
	publishers := map[models.APIPrincipalType]func() error{
		models.APIPrincipalUser:  func() error { return a.InvalidateUserRoleSet(ctx, principalID) },
		models.APIPrincipalToken: func() error { return a.InvalidateTokenRoleSet(ctx, principalID) },
		models.APIPrincipalUserGroup: func() error {
			return a.publishUserGroupRoleBindingChange(ctx, q, principalID)
		},
	}
	publish, ok := publishers[principalType]
	if !ok {
		return fmt.Errorf("unsupported API principal type %q", principalType)
	}
	return publish()
}

func (a *APISyncActions) publishUserGroupRoleBindingChange(
	ctx context.Context,
	q dao.AdminQuery,
	principalID uint,
) error {
	result, err := apirbac.NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUserGroup(ctx, principalID)
	if err != nil {
		return err
	}
	groupIDs := []uint{principalID}
	if principalID == models.DefaultUserGroupID {
		groupIDs = []uint{0, models.DefaultUserGroupID}
	}
	members, err := q.User().ListByGroupIDs(groupIDs)
	if err != nil {
		return fmt.Errorf("list API role group members: %w", err)
	}
	memberIDs := make([]uint, 0, len(members))
	seen := make(map[uint]struct{}, len(members))
	for _, member := range members {
		if _, exists := seen[member.ID]; exists {
			continue
		}
		seen[member.ID] = struct{}{}
		memberIDs = append(memberIDs, member.ID)
	}
	sort.Slice(memberIDs, func(i, j int) bool { return memberIDs[i] < memberIDs[j] })
	if err := a.PublishUserGroupRoleSet(ctx, protocol.APIRoleSetFetchResult{
		PrincipalID: principalID, Exists: true, RoleSet: result.APIRoleSet(),
	}); err != nil {
		return err
	}
	// behavior change: effective User RoleSets include group grants, so every
	// member cache must be invalidated after the group snapshot is published.
	for _, memberID := range memberIDs {
		if err := a.InvalidateUserRoleSet(ctx, memberID); err != nil {
			return err
		}
	}
	return nil
}

func publishAPIAction[T any](
	ctx context.Context,
	bus app.EventBus,
	action string,
	payload T,
	topics map[string]events.Topic[T],
) error {
	topic, ok := topics[action]
	if !ok {
		return fmt.Errorf("unsupported API sync action %q", action)
	}
	return events.Publish(ctx, bus, topic, payload)
}
