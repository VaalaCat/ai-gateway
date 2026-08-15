package user

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserRoleFactsPublishAfterCommittedRoleOrGroupChange(t *testing.T) {
	db := setupUserTestDB(t)
	require.NoError(t, models.SeedDefaultUserGroup(db))
	group := models.UserGroup{Name: "role-group"}
	require.NoError(t, db.Create(&group).Error)
	user := models.User{Username: "role-user", Password: "x", GroupID: 1, Role: 1, Status: 1}
	require.NoError(t, db.Create(&user).Error)

	ctx := newUserTestContext(t, db)
	bus := eventbus.NewMemoryBus()
	ctx.App.SetEventBus(bus)
	handler := &Handler{Bus: bus}
	called := 0
	_, err := events.Subscribe(bus, events.UserAPIRolesSyncedTopic,
		func(_ context.Context, payload protocol.APIRoleSetInvalidate) error {
			called++
			require.Equal(t, user.ID, payload.PrincipalID)
			var committed models.User
			require.NoError(t, db.First(&committed, user.ID).Error)
			require.Equal(t, group.ID, committed.GroupID)
			return nil
		})
	require.NoError(t, err)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(user.ID), 10)}
	req.SetBodyMap(map[string]any{"group_id": float64(group.ID)})
	_, err = handler.Update(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, called)

	noRoleChange := UpdateRequest{ID: req.ID}
	noRoleChange.SetBodyMap(map[string]any{"username": "role-user-renamed"})
	_, err = handler.Update(ctx, noRoleChange)
	require.NoError(t, err)
	require.Equal(t, 1, called)
}

func TestUserRoleMutationFailurePublishesNoRoleSetEvent(t *testing.T) {
	db := setupUserTestDB(t)
	user := models.User{Username: "failed-role-user", Password: "x", GroupID: 1, Role: 1, Status: 1}
	require.NoError(t, db.Create(&user).Error)
	ctx := newUserTestContext(t, db)
	bus := eventbus.NewMemoryBus()
	ctx.App.SetEventBus(bus)
	handler := &Handler{Bus: bus}
	eventsSeen := 0
	_, err := events.Subscribe(bus, events.UserAPIRolesSyncedTopic,
		func(_ context.Context, _ protocol.APIRoleSetInvalidate) error {
			eventsSeen++
			return nil
		})
	require.NoError(t, err)
	callbackName := "test:fail_user_role_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("forced user mutation failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	req := UpdateRequest{ID: strconv.FormatUint(uint64(user.ID), 10)}
	req.SetBodyMap(map[string]any{"role": float64(2)})
	_, err = handler.Update(ctx, req)
	require.Error(t, err)
	require.Zero(t, eventsSeen)
	var reloaded models.User
	require.NoError(t, db.First(&reloaded, user.ID).Error)
	require.Equal(t, 1, reloaded.Role)
}
