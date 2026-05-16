package sync

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

var _ app.Publisher = (*Publisher)(nil)

type Publisher struct {
	hub     *Hub
	bus     app.EventBus
	version *atomic.Int64
	logger  *zap.Logger
}

func NewPublisher(hub *Hub, bus app.EventBus, version *atomic.Int64, logger *zap.Logger) *Publisher {
	return &Publisher{hub: hub, bus: bus, version: version, logger: logger}
}

func (p *Publisher) Start() {
	subscribeTopic(p, events.EntityToken, events.ActionCreate, events.TokenCreateTopic)
	subscribeTopic(p, events.EntityToken, events.ActionUpdate, events.TokenUpdateTopic)
	subscribeTopic(p, events.EntityToken, events.ActionDelete, events.TokenDeleteTopic)

	subscribeTopic(p, events.EntityChannel, events.ActionCreate, events.ChannelCreateTopic)
	subscribeTopic(p, events.EntityChannel, events.ActionUpdate, events.ChannelUpdateTopic)
	subscribeTopic(p, events.EntityChannel, events.ActionDelete, events.ChannelDeleteTopic)

	subscribeTopic(p, events.EntityModel, events.ActionCreate, events.ModelCreateTopic)
	subscribeTopic(p, events.EntityModel, events.ActionUpdate, events.ModelUpdateTopic)
	subscribeTopic(p, events.EntityModel, events.ActionDelete, events.ModelDeleteTopic)

	subscribeTopic(p, events.EntitySetting, events.ActionUpdate, events.SettingUpdateTopic)

	subscribeTopic(p, events.EntityAgent, events.ActionCreate, events.AgentCreateTopic)
	subscribeTopic(p, events.EntityAgent, events.ActionUpdate, events.AgentUpdateTopic)
	subscribeTopic(p, events.EntityAgent, events.ActionDelete, events.AgentDeleteTopic)

	subscribeTopic(p, events.EntityAgentRoute, events.ActionCreate, events.AgentRouteCreateTopic)
	subscribeTopic(p, events.EntityAgentRoute, events.ActionUpdate, events.AgentRouteUpdateTopic)
	subscribeTopic(p, events.EntityAgentRoute, events.ActionDelete, events.AgentRouteDeleteTopic)

	subscribeTopic(p, events.EntityModelRouting, events.ActionCreate, events.ModelRoutingCreateTopic)
	subscribeTopic(p, events.EntityModelRouting, events.ActionUpdate, events.ModelRoutingUpdateTopic)
	subscribeTopic(p, events.EntityModelRouting, events.ActionDelete, events.ModelRoutingDeleteTopic)

	subscribeTopic(p, events.EntityUserGroup, events.ActionCreate, events.UserGroupCreateTopic)
	subscribeTopic(p, events.EntityUserGroup, events.ActionUpdate, events.UserGroupUpdateTopic)
	subscribeTopic(p, events.EntityUserGroup, events.ActionDelete, events.UserGroupDeleteTopic)

	subscribeTopic(p, events.EntityUser, events.ActionUpdate, events.UserSyncUpdateTopic)
	subscribeTopic(p, events.EntityUser, events.ActionDelete, events.UserSyncDeleteTopic)
}

func subscribeTopic[T any](p *Publisher, entity, action string, topic events.Topic[T]) {
	_, err := events.Subscribe(p.bus, topic, func(_ context.Context, payload T) error {
		data, err := json.Marshal(payload)
		if err != nil {
			p.logger.Error("marshal sync payload failed", zap.Error(err))
			return nil
		}

		newVersion := p.version.Add(1)
		push := protocol.SyncPushParams{
			Entity:  entity,
			Action:  action,
			Data:    data,
			Version: newVersion,
		}
		p.hub.Broadcast(consts.RPCSyncPush, push)
		p.logger.Info("sync.push broadcast",
			zap.String("entity", entity),
			zap.String("action", action),
			zap.Int64("version", newVersion),
		)
		return nil
	})
	if err != nil {
		p.logger.Error("subscribe sync source failed",
			zap.String("entity", entity),
			zap.String("action", action),
			zap.Error(err),
		)
	}
}
