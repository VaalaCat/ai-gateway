package user_group

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserGroupUpdatePublishesCommittedPositiveRoleSet(t *testing.T) {
	handler, ctx, db := setupBYOKTest(t)
	group := models.UserGroup{Name: "before"}
	require.NoError(t, db.Create(&group).Error)
	called := 0
	_, err := events.Subscribe(ctx.GetBus(), events.UserGroupAPIRolesSyncedTopic,
		func(_ context.Context, payload protocol.APIRoleSetFetchResult) error {
			called++
			require.Equal(t, group.ID, payload.PrincipalID)
			require.True(t, payload.Exists)
			require.NotNil(t, payload.RoleSet.RoleIDs)
			var committed models.UserGroup
			require.NoError(t, db.First(&committed, group.ID).Error)
			require.Equal(t, "after", committed.Name)
			return nil
		})
	require.NoError(t, err)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(group.ID), 10)}
	req.SetBodyMap(map[string]any{"name": "after"})
	_, err = handler.Update(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, called)
}

func TestUserGroupMutationFailurePublishesNoRoleSetEvent(t *testing.T) {
	handler, ctx, db := setupBYOKTest(t)
	group := models.UserGroup{Name: "unchanged"}
	require.NoError(t, db.Create(&group).Error)
	eventsSeen := 0
	_, err := events.Subscribe(ctx.GetBus(), events.UserGroupAPIRolesSyncedTopic,
		func(_ context.Context, _ protocol.APIRoleSetFetchResult) error {
			eventsSeen++
			return nil
		})
	require.NoError(t, err)
	callbackName := "test:fail_user_group_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_groups" {
			tx.AddError(errors.New("forced user group mutation failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	req := UpdateRequest{ID: strconv.FormatUint(uint64(group.ID), 10)}
	req.SetBodyMap(map[string]any{"name": "must-not-commit"})
	_, err = handler.Update(ctx, req)
	require.Error(t, err)
	require.Zero(t, eventsSeen)
	var reloaded models.UserGroup
	require.NoError(t, db.First(&reloaded, group.ID).Error)
	require.Equal(t, "unchanged", reloaded.Name)
}

func TestUserGroupDeleteInvalidatesMembersAndDeletesGroupRoleSet(t *testing.T) {
	handler, ctx, db := setupBYOKTest(t)
	group := models.UserGroup{Name: "delete-role-group"}
	require.NoError(t, db.Create(&group).Error)
	users := []models.User{
		{Username: "delete-member-a", Password: "x", GroupID: group.ID, Status: 1},
		{Username: "delete-member-b", Password: "x", GroupID: group.ID, Status: 1},
	}
	for index := range users {
		require.NoError(t, db.Create(&users[index]).Error)
	}
	var invalidated []uint
	_, err := events.Subscribe(ctx.GetBus(), events.UserAPIRolesSyncedTopic,
		func(_ context.Context, payload protocol.APIRoleSetInvalidate) error {
			invalidated = append(invalidated, payload.PrincipalID)
			return nil
		})
	require.NoError(t, err)
	var deleted protocol.APIRoleSetFetchResult
	_, err = events.Subscribe(ctx.GetBus(), events.UserGroupAPIRolesDeletedTopic,
		func(_ context.Context, payload protocol.APIRoleSetFetchResult) error {
			deleted = payload
			return nil
		})
	require.NoError(t, err)

	_, err = handler.Delete(ctx, DeleteRequest{ID: strconv.FormatUint(uint64(group.ID), 10)})
	require.NoError(t, err)
	require.Equal(t, []uint{users[0].ID, users[1].ID}, invalidated)
	require.Equal(t, group.ID, deleted.PrincipalID)
	require.False(t, deleted.Exists)
}

func TestUserGroupDeleteMutationFailurePublishesNoSyncEvents(t *testing.T) {
	handler, ctx, db := setupBYOKTest(t)
	group := models.UserGroup{Name: "failed-delete-group"}
	require.NoError(t, db.Create(&group).Error)
	user := models.User{Username: "failed-delete-member", Password: "x", GroupID: group.ID, Status: 1}
	require.NoError(t, db.Create(&user).Error)
	eventsSeen := 0
	for _, subscribe := range []func() error{
		func() error {
			_, err := events.Subscribe(ctx.GetBus(), events.UserAPIRolesSyncedTopic,
				func(_ context.Context, _ protocol.APIRoleSetInvalidate) error { eventsSeen++; return nil })
			return err
		},
		func() error {
			_, err := events.Subscribe(ctx.GetBus(), events.UserGroupAPIRolesDeletedTopic,
				func(_ context.Context, _ protocol.APIRoleSetFetchResult) error { eventsSeen++; return nil })
			return err
		},
	} {
		require.NoError(t, subscribe())
	}
	callbackName := "test:fail_user_group_delete"
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_groups" {
			tx.AddError(errors.New("forced user group delete failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(callbackName) })

	_, err := handler.Delete(ctx, DeleteRequest{ID: strconv.FormatUint(uint64(group.ID), 10)})
	require.Error(t, err)
	require.Zero(t, eventsSeen)
	var reloaded models.User
	require.NoError(t, db.First(&reloaded, user.ID).Error)
	require.Equal(t, group.ID, reloaded.GroupID)
}
