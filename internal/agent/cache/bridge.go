package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

var _ app.WSBridge = (*WSBridge)(nil)

type WSBridge struct {
	Client app.WSClient
	Store  *Store
	Bus    app.EventBus
	Logger *zap.Logger
	Syncer *Syncer
}

func NewWSBridge(client app.WSClient, store *Store, bus app.EventBus, logger *zap.Logger) *WSBridge {
	return &WSBridge{Client: client, Store: store, Bus: bus, Logger: logger}
}

// Start registers WS notification handlers that bridge to local EventBus
func (b *WSBridge) Start() {
	b.Client.OnNotification(consts.RPCSyncPush, func(ctx context.Context, params json.RawMessage) (any, error) {
		var push protocol.SyncPushParams
		if err := json.Unmarshal(params, &push); err != nil {
			b.Logger.Error("invalid sync.push", zap.Error(err))
			return nil, nil
		}
		// Re-publish to local EventBus as sync event
		if err := events.PublishSyncEvent(ctx, b.Bus, push.Entity, push.Action, push); err != nil {
			b.Logger.Error("publish sync event failed", zap.Error(err))
		}
		return nil, nil
	})

	b.Client.OnNotification(consts.RPCSyncRequestFullSync, func(ctx context.Context, params json.RawMessage) (any, error) {
		if err := events.PublishSyncFullSyncRequested(ctx, b.Bus); err != nil {
			b.Logger.Error("publish sync.full_sync_requested failed", zap.Error(err))
		}
		return nil, nil
	})

	b.Client.OnNotification(consts.RPCSyncAutoAddrUpdate, func(ctx context.Context, params json.RawMessage) (any, error) {
		if err := b.handleAutoAddrUpdate(params); err != nil {
			b.Logger.Error("invalid sync.autoAddrUpdate", zap.Error(err))
		}
		return nil, nil
	})

	b.Client.OnNotification(consts.RPCSyncUserQuota, func(ctx context.Context, params json.RawMessage) (any, error) {
		var users []protocol.SyncedUser
		if err := json.Unmarshal(params, &users); err != nil {
			b.Logger.Error("invalid sync.userQuota", zap.Error(err))
			return nil, nil
		}
		for _, u := range users {
			b.Store.SetUserQuota(u.ID, u.Quota)
		}
		return nil, nil
	})

	b.Client.OnNotification(consts.RPCSyncForceFullSync, func(ctx context.Context, params json.RawMessage) (any, error) {
		if b.Syncer == nil {
			return nil, fmt.Errorf("syncer not initialized")
		}
		start := time.Now()
		if err := b.Syncer.FullSync(ctx); err != nil {
			return nil, err
		}
		return protocol.ForceFullSyncResponse{
			Version:    b.Store.Version(),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	})
}

type autoAddrUpdate struct {
	AgentID       string               `json:"agent_id"`
	HTTPAddresses []agentproxy.Address `json:"http_addresses"`
}

func (b *WSBridge) handleAutoAddrUpdate(params json.RawMessage) error {
	var update autoAddrUpdate
	if err := json.Unmarshal(params, &update); err != nil {
		return err
	}
	if update.AgentID == "" || b.Store == nil {
		return nil
	}
	b.Store.UpdateAgentAutoAddresses(update.AgentID, update.HTTPAddresses)
	return nil
}
