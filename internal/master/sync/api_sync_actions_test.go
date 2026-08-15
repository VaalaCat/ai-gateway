package sync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPriceTransitionPushesConsumesQuota(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	actions := NewAPISyncActions(bus, nil)
	var received []protocol.SyncedAPIService
	_, err := events.Subscribe(bus, events.APIServiceUpdateTopic, func(_ context.Context, item protocol.SyncedAPIService) error {
		received = append(received, item)
		return nil
	})
	require.NoError(t, err)

	service := models.APIService{ID: 8, Slug: "paid", Name: "Paid", PricePerCall: 1, Status: 1}
	require.NoError(t, actions.PublishService(context.Background(), events.ActionUpdate, service))
	service.PricePerCall = 0
	require.NoError(t, actions.PublishService(context.Background(), events.ActionUpdate, service))

	require.Len(t, received, 2)
	require.True(t, received[0].ConsumesQuota)
	require.False(t, received[1].ConsumesQuota)
}

func TestAPISyncActionsPublishPositiveEmptyGroupAndPrincipalInvalidations(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	actions := NewAPISyncActions(bus, nil)
	var group protocol.APIRoleSetFetchResult
	var user, token protocol.APIRoleSetInvalidate
	_, err := events.Subscribe(bus, events.UserGroupAPIRolesSyncedTopic, func(_ context.Context, value protocol.APIRoleSetFetchResult) error {
		group = value
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, value protocol.APIRoleSetInvalidate) error {
		user = value
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.TokenAPIRolesSyncedTopic, func(_ context.Context, value protocol.APIRoleSetInvalidate) error {
		token = value
		return nil
	})
	require.NoError(t, err)

	require.NoError(t, actions.PublishUserGroupRoleSet(context.Background(), protocol.APIRoleSetFetchResult{
		PrincipalID: 3, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{}},
	}))
	require.NoError(t, actions.InvalidateUserRoleSet(context.Background(), 4))
	require.NoError(t, actions.InvalidateTokenRoleSet(context.Background(), 5))
	require.Equal(t, uint(3), group.PrincipalID)
	require.True(t, group.Exists)
	require.NotNil(t, group.RoleSet.RoleIDs)
	require.Equal(t, uint(4), user.PrincipalID)
	require.Equal(t, uint(5), token.PrincipalID)
}

func TestAPISyncActionsPublishesTypedUserGroupRoleSetDelete(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	var received protocol.APIRoleSetFetchResult
	_, err := events.Subscribe(bus, events.UserGroupAPIRolesDeletedTopic,
		func(_ context.Context, value protocol.APIRoleSetFetchResult) error {
			received = value
			return nil
		})
	require.NoError(t, err)

	require.NoError(t, NewAPISyncActions(bus, nil).DeleteUserGroupRoleSet(context.Background(), 9))
	require.Equal(t, uint(9), received.PrincipalID)
	require.False(t, received.Exists)
}

func TestAPISyncActionsRoleAndBindingImpactRules(t *testing.T) {
	q, m := setupSyncDB(t)
	bus := eventbus.NewMemoryBus()
	actions := NewAPISyncActions(bus, nil)
	role := models.Role{Key: "reader", Name: "Reader", Status: 1}
	require.NoError(t, m.APIRBAC().CreateRole(&role))
	require.NoError(t, m.APIRBAC().CreateRoleBinding(&models.RoleBinding{
		PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: role.ID,
	}))
	counts := map[string]int{}
	_, err := events.Subscribe(bus, events.APIRoleUpdateTopic, func(_ context.Context, _ protocol.SyncedAPIRole) error {
		counts["role"]++
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, _ protocol.APIRoleSetInvalidate) error {
		counts["user"]++
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.TokenAPIRolesSyncedTopic, func(_ context.Context, _ protocol.APIRoleSetInvalidate) error {
		counts["token"]++
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.UserGroupAPIRolesSyncedTopic, func(_ context.Context, got protocol.APIRoleSetFetchResult) error {
		counts["group"]++
		require.Equal(t, []uint{role.ID}, got.RoleSet.RoleIDs)
		return nil
	})
	require.NoError(t, err)

	require.NoError(t, actions.PublishRole(context.Background(), q, events.ActionUpdate, role.ID))
	require.Equal(t, map[string]int{"role": 1}, counts)
	require.NoError(t, actions.PublishRoleBindingChange(context.Background(), q, models.APIPrincipalUser, 10))
	require.NoError(t, actions.PublishRoleBindingChange(context.Background(), q, models.APIPrincipalToken, 11))
	require.NoError(t, actions.PublishRoleBindingChange(context.Background(), q, models.APIPrincipalUserGroup, 1))
	require.Equal(t, map[string]int{"role": 1, "user": 1, "token": 1, "group": 1}, counts)
}

func TestPublishRoleBindingChangeAfterLastGroupBindingDeletionPushesPositiveEmpty(t *testing.T) {
	q, m := setupSyncDB(t)
	role := models.Role{Key: "temporary", Name: "Temporary", Status: 1}
	require.NoError(t, m.APIRBAC().CreateRole(&role))
	binding := models.RoleBinding{
		PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: role.ID,
	}
	require.NoError(t, m.APIRBAC().CreateRoleBinding(&binding))
	require.NoError(t, m.APIRBAC().DeleteRoleBinding(binding.ID))

	bus := eventbus.NewMemoryBus()
	var received protocol.APIRoleSetFetchResult
	_, err := events.Subscribe(bus, events.UserGroupAPIRolesSyncedTopic,
		func(_ context.Context, payload protocol.APIRoleSetFetchResult) error {
			received = payload
			return nil
		})
	require.NoError(t, err)
	require.NoError(t, NewAPISyncActions(bus, nil).PublishRoleBindingChange(
		context.Background(), q, models.APIPrincipalUserGroup, 1,
	))
	require.Equal(t, uint(1), received.PrincipalID)
	require.True(t, received.Exists)
	require.NotNil(t, received.RoleSet.RoleIDs)
	require.Empty(t, received.RoleSet.RoleIDs)
}

func TestPublishDefaultGroupRoleBindingChangeInvalidatesLegacyAndDefaultMembersInOrder(t *testing.T) {
	q, _, db := setupSyncDBWithDatabase(t)
	users := []models.User{
		{Username: "default-member", Password: "x", GroupID: models.DefaultUserGroupID, Status: 1},
		{Username: "legacy-member", Password: "x", GroupID: 0, Status: 1},
		{Username: "other-member", Password: "x", GroupID: 2, Status: 1},
	}
	for index := range users {
		require.NoError(t, db.Create(&users[index]).Error)
	}

	bus := eventbus.NewMemoryBus()
	var received []string
	_, err := events.Subscribe(bus, events.UserGroupAPIRolesSyncedTopic,
		func(_ context.Context, value protocol.APIRoleSetFetchResult) error {
			received = append(received, fmt.Sprintf("group:%d", value.PrincipalID))
			return nil
		})
	require.NoError(t, err)
	_, err = events.Subscribe(bus, events.UserAPIRolesSyncedTopic,
		func(_ context.Context, value protocol.APIRoleSetInvalidate) error {
			received = append(received, fmt.Sprintf("user:%d", value.PrincipalID))
			return nil
		})
	require.NoError(t, err)

	require.NoError(t, NewAPISyncActions(bus, nil).PublishRoleBindingChange(
		context.Background(), q, models.APIPrincipalUserGroup, models.DefaultUserGroupID,
	))
	require.Equal(t, []string{
		fmt.Sprintf("group:%d", models.DefaultUserGroupID),
		fmt.Sprintf("user:%d", users[0].ID),
		fmt.Sprintf("user:%d", users[1].ID),
	}, received)
}

func TestPublishGroupRoleBindingChangeQueryFailurePublishesNothing(t *testing.T) {
	q, _, db := setupSyncDBWithDatabase(t)
	bus := eventbus.NewMemoryBus()
	eventsSeen := 0
	_, err := events.Subscribe(bus, events.UserGroupAPIRolesSyncedTopic,
		func(_ context.Context, _ protocol.APIRoleSetFetchResult) error {
			eventsSeen++
			return nil
		})
	require.NoError(t, err)
	callbackName := "test:fail_group_member_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("forced member query failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	err = NewAPISyncActions(bus, nil).PublishRoleBindingChange(
		context.Background(), q, models.APIPrincipalUserGroup, models.DefaultUserGroupID,
	)
	require.ErrorContains(t, err, "forced member query failure")
	require.Zero(t, eventsSeen)
}
