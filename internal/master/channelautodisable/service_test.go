package channelautodisable

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type fixedAutoBanSettings struct{ value settings.AgentSettings }

func (s fixedAutoBanSettings) Settings() settings.AgentSettings { return s.value }

func newServiceFixture(t *testing.T, bus app.EventBus) (app.Application, *gorm.DB, *Service) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, models.AutoMigrate(db))

	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetLogDB(db)
	if bus == nil {
		bus = eventbus.NewMemoryBus()
	}
	return application, db, New(application, bus, zap.NewNop())
}

func trigger(source attemptproxy.ChannelSource, id uint, revision uint64) attemptproxy.ChannelAutoDisableTrigger {
	return attemptproxy.ChannelAutoDisableTrigger{
		Source: source, ChannelID: id, Revision: revision,
		Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
	}
}

func TestServiceRejectsInvalidTrigger(t *testing.T) {
	_, _, service := newServiceFixture(t, nil)
	require.NoError(t, service.DisableFromTriggers(t.Context(), nil))
	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{}))
	tests := []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourceAdmin, 0, 0),
		trigger(attemptproxy.ChannelSource("unknown"), 1, 0),
		{Source: attemptproxy.SourceAdmin, ChannelID: 1, Reason: "unknown"},
	}
	for _, invalid := range tests {
		require.Error(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{invalid}))
	}
}

func TestServiceDisablesAdminAndPublishesUpdate(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	_, db, service := newServiceFixture(t, bus)
	channel := &models.Channel{ChannelCore: models.ChannelCore{
		Name: "admin", Type: 1, Status: 1, AutoBan: 1, AutoBanRevision: 4,
	}}
	require.NoError(t, db.Create(channel).Error)

	var published []models.Channel
	_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, got models.Channel) error {
		published = append(published, got)
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourceAdmin, channel.ID, 4),
	}))

	require.Len(t, published, 1)
	require.Zero(t, published[0].Status)
	require.True(t, published[0].AutoBanState.Data().Tripped)
	require.Equal(t, attemptproxy.ChannelAutoDisableReasonConsecutiveErrors, published[0].AutoBanState.Data().Reason)
	require.False(t, published[0].AutoBanState.Data().AutoRecover)
	require.NotZero(t, published[0].AutoBanState.Data().TrippedAt)
	require.Equal(t, uint64(4), published[0].AutoBanRevision)
}

func TestServiceDisablesPrivateAndInvalidatesFullAudience(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	_, db, service := newServiceFixture(t, bus)
	users := []models.User{
		{Username: "owner", Password: "x", Role: 1, Status: 1, GroupID: 1},
		{Username: "direct", Password: "x", Role: 1, Status: 1, GroupID: 1},
		{Username: "member", Password: "x", Role: 1, Status: 1, GroupID: 9},
	}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
	}
	channel := &models.PrivateChannel{ChannelCore: models.ChannelCore{
		Name: "private", Type: 1, Status: 1, AutoBan: 1, AutoBanRevision: 2,
	}, OwnerID: users[0].ID}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&models.PrivateChannelShare{ChannelID: channel.ID, TargetType: "user", TargetID: users[1].ID}).Error)
	require.NoError(t, db.Create(&models.PrivateChannelShare{ChannelID: channel.ID, TargetType: "group", TargetID: 9}).Error)

	var invalidations []protocol.PrivateChannelInvalidatePayload
	_, err := events.Subscribe(bus, events.PrivateChannelInvalidateTopic, func(_ context.Context, got protocol.PrivateChannelInvalidatePayload) error {
		invalidations = append(invalidations, got)
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourcePrivate, channel.ID, 2),
	}))

	require.Len(t, invalidations, 1)
	gotIDs := invalidations[0].AffectedUserIDs
	sort.Slice(gotIDs, func(i, j int) bool { return gotIDs[i] < gotIDs[j] })
	wantIDs := []uint{users[0].ID, users[1].ID, users[2].ID}
	sort.Slice(wantIDs, func(i, j int) bool { return wantIDs[i] < wantIDs[j] })
	require.Equal(t, wantIDs, gotIDs)

	var got models.PrivateChannel
	require.NoError(t, db.First(&got, channel.ID).Error)
	require.Zero(t, got.Status)
	require.True(t, got.AutoBanState.Data().Tripped)
}

func TestAgentAndMasterAgreeOnBinaryAutoBanPredicate(t *testing.T) {
	for _, source := range []state.ChannelSource{state.SourceAdmin, state.SourcePrivate} {
		for _, autoBan := range []int{-1, 0, 1, 2} {
			t.Run(string(source)+"/"+strconv.Itoa(autoBan), func(t *testing.T) {
				_, db, service := newServiceFixture(t, nil)
				revision := uint64(4)
				var channelID uint
				switch source {
				case state.SourceAdmin:
					row := &models.Channel{ChannelCore: models.ChannelCore{
						Name: "admin", Type: 1, Status: 1, AutoBan: autoBan, AutoBanRevision: revision,
					}}
					require.NoError(t, db.Create(row).Error)
					channelID = row.ID
				case state.SourcePrivate:
					row := &models.PrivateChannel{ChannelCore: models.ChannelCore{
						Name: "private", Type: 1, Status: 1, AutoBan: autoBan, AutoBanRevision: revision,
					}, OwnerID: 42}
					require.NoError(t, db.Create(row).Error)
					channelID = row.ID
				}

				runner := resilience.Runner{
					Settings: fixedAutoBanSettings{value: settings.AgentSettings{
						MaxRetriesPerChannel: 0, RetryBackoffBaseMs: 1, RetryBackoffMaxMs: 2,
						BreakerThreshold: 1, BreakerCooldownMs: 1, BreakerEnabled: 0,
					}},
					Breakers: resilience.NewRegistry(), AutoBan: resilience.NewAutoBanTracker(),
				}
				rctx := &state.RelayContext{State: &state.RelayState{}}
				runtimeChannel := &models.Channel{ChannelCore: models.ChannelCore{
					ID: channelID, AutoBan: autoBan, AutoBanRevision: revision,
				}}
				runner.Run(rctx, state.Attempt{Channel: runtimeChannel, Source: source, SourceID: channelID}, func() attemptexec.DispatchResult {
					return attemptexec.DispatchResult{
						Outcome: state.AttemptResult{Err: &common.UpstreamError{Status: 503}}, ProviderDispatched: true,
					}
				})
				require.NoError(t, service.DisableFromTriggers(t.Context(), rctx.State.AutoDisableTriggers))

				wantDisabled := autoBan == 1
				require.Equal(t, wantDisabled, len(rctx.State.AutoDisableTriggers) == 1)
				var status int
				if source == state.SourceAdmin {
					var row models.Channel
					require.NoError(t, db.First(&row, channelID).Error)
					status = row.Status
				} else {
					var row models.PrivateChannel
					require.NoError(t, db.First(&row, channelID).Error)
					status = row.Status
				}
				require.Equal(t, wantDisabled, status == 0)
			})
		}
	}
}

func TestServiceTreatsStaleAndDuplicateAsNoop(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	_, db, service := newServiceFixture(t, bus)
	channel := &models.Channel{ChannelCore: models.ChannelCore{
		Name: "admin", Type: 1, Status: 1, AutoBan: 1, AutoBanRevision: 4,
	}}
	require.NoError(t, db.Create(channel).Error)

	updates := 0
	_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(context.Context, models.Channel) error {
		updates++
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourceAdmin, channel.ID, 3),
		trigger(attemptproxy.SourceAdmin, channel.ID, 4),
		trigger(attemptproxy.SourceAdmin, channel.ID, 4),
	}))
	require.Equal(t, 1, updates)
	var got models.Channel
	require.NoError(t, db.First(&got, channel.ID).Error)
	require.Zero(t, got.Status)
	require.Equal(t, uint64(4), got.AutoBanRevision)
	require.True(t, got.AutoBanState.Data().Tripped)
}

func TestServiceDeletedChannelDoesNotPoisonUpload(t *testing.T) {
	_, _, service := newServiceFixture(t, nil)
	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourceAdmin, 9999, 0),
		trigger(attemptproxy.SourcePrivate, 9999, 0),
	}))
}

func TestServiceReturnsDatabaseFailureForRetry(t *testing.T) {
	_, db, service := newServiceFixture(t, nil)
	require.NoError(t, db.Migrator().DropTable(&models.Channel{}))
	require.Error(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourceAdmin, 1, 0),
	}))
}

type publishFailBus struct{ *eventbus.MemoryBus }

func (publishFailBus) Publish(context.Context, eventbus.Event) error {
	return errors.New("publish unavailable")
}

func TestServiceCommittedUpdateSurvivesPublishFailure(t *testing.T) {
	bus := publishFailBus{MemoryBus: eventbus.NewMemoryBus()}
	_, db, service := newServiceFixture(t, bus)
	channel := &models.Channel{ChannelCore: models.ChannelCore{
		Name: "admin", Type: 1, Status: 1, AutoBan: 1, AutoBanRevision: 4,
	}}
	require.NoError(t, db.Create(channel).Error)

	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourceAdmin, channel.ID, 4),
	}))
	var got models.Channel
	require.NoError(t, db.First(&got, channel.ID).Error)
	require.Zero(t, got.Status)
	require.True(t, got.AutoBanState.Data().Tripped)
}

func TestServicePrivateCommittedUpdateSurvivesAudienceFailure(t *testing.T) {
	_, db, service := newServiceFixture(t, nil)
	channel := &models.PrivateChannel{ChannelCore: models.ChannelCore{
		Name: "private", Type: 1, Status: 1, AutoBan: 1, AutoBanRevision: 4,
	}, OwnerID: 42}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Migrator().DropTable(&models.PrivateChannelShare{}))

	require.NoError(t, service.DisableFromTriggers(t.Context(), []attemptproxy.ChannelAutoDisableTrigger{
		trigger(attemptproxy.SourcePrivate, channel.ID, 4),
	}))
	var got models.PrivateChannel
	require.NoError(t, db.First(&got, channel.ID).Error)
	require.Zero(t, got.Status)
	require.True(t, got.AutoBanState.Data().Tripped)
}
