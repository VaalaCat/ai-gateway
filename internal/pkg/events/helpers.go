package events

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func PublishEntity[T any](ctx context.Context, bus app.EventBus, entity, action string, payload T) error {
	return Publish(ctx, bus, DynamicTopic[T](entity, action), payload)
}

func PublishSyncEvent(ctx context.Context, bus app.EventBus, entity, action string, payload protocol.SyncPushParams) error {
	return Publish(ctx, bus, SyncPushTopic(entity, action), payload)
}

func PublishTokenCreate(ctx context.Context, bus app.EventBus, payload models.Token) error {
	return Publish(ctx, bus, TokenCreateTopic, payload)
}

func PublishTokenUpdate(ctx context.Context, bus app.EventBus, payload models.Token) error {
	return Publish(ctx, bus, TokenUpdateTopic, payload)
}

func PublishTokenDelete(ctx context.Context, bus app.EventBus, payload models.Token) error {
	return Publish(ctx, bus, TokenDeleteTopic, payload)
}

func PublishChannelCreate(ctx context.Context, bus app.EventBus, payload models.Channel) error {
	return Publish(ctx, bus, ChannelCreateTopic, payload)
}

func PublishChannelUpdate(ctx context.Context, bus app.EventBus, payload models.Channel) error {
	return Publish(ctx, bus, ChannelUpdateTopic, payload)
}

func PublishChannelDelete(ctx context.Context, bus app.EventBus, payload models.Channel) error {
	return Publish(ctx, bus, ChannelDeleteTopic, payload)
}

func PublishModelCreate(ctx context.Context, bus app.EventBus, payload models.ModelConfig) error {
	return Publish(ctx, bus, ModelCreateTopic, payload)
}

func PublishModelUpdate(ctx context.Context, bus app.EventBus, payload models.ModelConfig) error {
	return Publish(ctx, bus, ModelUpdateTopic, payload)
}

func PublishModelDelete(ctx context.Context, bus app.EventBus, payload models.ModelConfig) error {
	return Publish(ctx, bus, ModelDeleteTopic, payload)
}

func PublishAgentCreate(ctx context.Context, bus app.EventBus, payload models.Agent) error {
	return Publish(ctx, bus, AgentCreateTopic, payload)
}

func PublishAgentUpdate(ctx context.Context, bus app.EventBus, payload models.Agent) error {
	return Publish(ctx, bus, AgentUpdateTopic, payload)
}

func PublishAgentDelete(ctx context.Context, bus app.EventBus, payload models.Agent) error {
	return Publish(ctx, bus, AgentDeleteTopic, payload)
}

func PublishAgentRevoked(ctx context.Context, bus app.EventBus, payload models.Agent) error {
	return Publish(ctx, bus, AgentRevokedTopic, payload)
}

func PublishAgentRegistered(ctx context.Context, bus app.EventBus, payload models.Agent) error {
	return Publish(ctx, bus, AgentRegisteredTopic, payload)
}

func PublishUsageReported(ctx context.Context, bus app.EventBus, payload protocol.UsageReport) error {
	return Publish(ctx, bus, UsageReportedTopic, payload)
}

func PublishUsageCompleted(ctx context.Context, bus app.EventBus, payload protocol.UsageLogEntry) error {
	return Publish(ctx, bus, UsageCompletedTopic, payload)
}

func PublishUserQuotaDepleted(ctx context.Context, bus app.EventBus, payload models.User) error {
	return Publish(ctx, bus, UserQuotaDepletedTopic, payload)
}

func PublishAgentRouteCreate(ctx context.Context, bus app.EventBus, payload models.AgentRoute) error {
	return Publish(ctx, bus, AgentRouteCreateTopic, payload)
}

func PublishAgentRouteUpdate(ctx context.Context, bus app.EventBus, payload models.AgentRoute) error {
	return Publish(ctx, bus, AgentRouteUpdateTopic, payload)
}

func PublishAgentRouteDelete(ctx context.Context, bus app.EventBus, payload models.AgentRoute) error {
	return Publish(ctx, bus, AgentRouteDeleteTopic, payload)
}

func PublishSettingUpdate(ctx context.Context, bus app.EventBus, payload models.Setting) error {
	return Publish(ctx, bus, SettingUpdateTopic, payload)
}

func PublishSyncFullSyncRequested(ctx context.Context, bus app.EventBus) error {
	return Publish(ctx, bus, SyncFullSyncRequestedTopic, struct{}{})
}

func SubscribeUsageReported(bus app.EventBus, handler func(context.Context, protocol.UsageReport) error) (eventbus.Subscription, error) {
	return Subscribe(bus, UsageReportedTopic, handler)
}

func SubscribeUsageCompleted(bus app.EventBus, handler func(context.Context, protocol.UsageLogEntry) error) (eventbus.Subscription, error) {
	return Subscribe(bus, UsageCompletedTopic, handler)
}

func SubscribeUserQuotaDepleted(bus app.EventBus, handler func(context.Context, models.User) error) (eventbus.Subscription, error) {
	return Subscribe(bus, UserQuotaDepletedTopic, handler)
}

func SubscribeSyncFullSyncRequested(bus app.EventBus, handler func(context.Context) error) (eventbus.Subscription, error) {
	return Subscribe(bus, SyncFullSyncRequestedTopic, func(ctx context.Context, _ struct{}) error {
		return handler(ctx)
	})
}

func SubscribeSyncPushPattern(bus app.EventBus, pattern Pattern[protocol.SyncPushParams], handler func(context.Context, protocol.SyncPushParams) error) (eventbus.Subscription, error) {
	return SubscribePattern(bus, pattern, handler)
}

// PublishPrivateChannelInvalidate notifies agents to drop cached visiblePrivateChannels
// for the listed users. Callers (master CRUD/share handlers) expand
// (owner ∪ share→user ∪ share→group members) into affectedUserIDs before calling.
func PublishPrivateChannelInvalidate(ctx context.Context, bus app.EventBus, affectedUserIDs []uint) error {
	payload := protocol.PrivateChannelInvalidatePayload{
		Action:          "invalidate",
		AffectedUserIDs: affectedUserIDs,
	}
	return PublishEntity(ctx, bus, EntityPrivateChannel, "invalidate", payload)
}
