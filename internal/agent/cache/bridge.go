package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

var _ app.WSBridge = (*WSBridge)(nil)

type WSBridge struct {
	Client               app.WSClient
	Store                *Store
	Bus                  app.EventBus
	Logger               *zap.Logger
	Syncer               *Syncer
	ControlSession       *ControlSession
	SetAgentCapabilities func(string, []string)
	ApplyDirectAddresses func(protocol.AgentDirectAddressesUpdate) bool
}

type inlineNotificationRegistrar interface {
	OnNotificationInline(method string, handler app.NotificationHandler)
}

func NewWSBridge(client app.WSClient, store *Store, bus app.EventBus, logger *zap.Logger) *WSBridge {
	bridge := &WSBridge{Client: client, Store: store, Bus: bus, Logger: logger}
	if store != nil {
		bridge.SetAgentCapabilities = store.SetAgentCapabilities
		bridge.ApplyDirectAddresses = store.ApplyDirectAddressesUpdate
	}
	return bridge
}

// Start registers WS notification handlers that bridge to local EventBus
func (b *WSBridge) Start() {
	controlSession := b.ControlSession
	if controlSession == nil && b.Syncer != nil {
		controlSession = b.Syncer.CurrentControlSession()
	}
	syncPushHandler := func(ctx context.Context, params json.RawMessage) (any, error) {
		var push protocol.SyncPushParams
		if err := json.Unmarshal(params, &push); err != nil {
			decodeErr := fmt.Errorf("decode sync.push: %w", err)
			if b.failSessionSync(controlSession, decodeErr) {
				b.Logger.Error("invalid sync.push", zap.Error(decodeErr))
			}
			return nil, decodeErr
		}
		if push.Entity == events.EntityAgent || push.Entity == events.EntityAgentRoute {
			if b.Syncer == nil {
				return nil, fmt.Errorf("syncer not initialized")
			}
			applied, err := b.Syncer.ApplySyncPushForSession(controlSession, push)
			if !applied {
				return nil, nil
			}
			if err != nil {
				b.Logger.Error("apply ordered sync.push failed", zap.Error(err))
				return nil, err
			}
			return nil, nil
		}
		// Re-publish to local EventBus as sync event
		sessionCtx := contextWithControlSession(ctx, controlSession)
		if err := events.PublishSyncEvent(sessionCtx, b.Bus, push.Entity, push.Action, push); err != nil {
			b.Logger.Error("publish sync event failed", zap.Error(err))
		}
		return nil, nil
	}
	// Route configuration frames are bounded control messages. Handling them on
	// the reader preserves wire order with the full-sync response they precede.
	if registrar, ok := b.Client.(inlineNotificationRegistrar); ok {
		registrar.OnNotificationInline(consts.RPCSyncPush, syncPushHandler)
	} else {
		b.Client.OnNotification(consts.RPCSyncPush, syncPushHandler)
	}

	requestedFullSyncHandler := func(_ context.Context, _ json.RawMessage) (any, error) {
		if b.Syncer == nil {
			return nil, fmt.Errorf("syncer not initialized")
		}
		b.Syncer.RequestFullSyncForSession(controlSession)
		return nil, nil
	}
	if registrar, ok := b.Client.(inlineNotificationRegistrar); ok {
		registrar.OnNotificationInline(consts.RPCSyncRequestFullSync, requestedFullSyncHandler)
	} else {
		b.Client.OnNotification(consts.RPCSyncRequestFullSync, requestedFullSyncHandler)
	}

	agentCapabilitiesHandler := func(_ context.Context, params json.RawMessage) (any, error) {
		b.handleAgentCapabilities(params)
		return nil, nil
	}
	if registrar, ok := b.Client.(inlineNotificationRegistrar); ok {
		registrar.OnNotificationInline(consts.RPCSyncAgentCapabilities, agentCapabilitiesHandler)
	} else {
		b.Client.OnNotification(consts.RPCSyncAgentCapabilities, agentCapabilitiesHandler)
	}

	directAddressesHandler := func(_ context.Context, params json.RawMessage) (any, error) {
		if err := b.handleAutoAddrUpdate(params); err != nil {
			b.Logger.Error("invalid sync.autoAddrUpdate", zap.Error(err))
		}
		return nil, nil
	}
	if registrar, ok := b.Client.(inlineNotificationRegistrar); ok {
		registrar.OnNotificationInline(consts.RPCSyncAutoAddrUpdate, directAddressesHandler)
	} else {
		b.Client.OnNotification(consts.RPCSyncAutoAddrUpdate, directAddressesHandler)
	}

	b.Client.OnNotification(consts.RPCSyncUserQuota, func(ctx context.Context, params json.RawMessage) (any, error) {
		var users []protocol.SyncedUser
		if err := json.Unmarshal(params, &users); err != nil {
			b.Logger.Error("invalid sync.userQuota", zap.Error(err))
			return nil, nil
		}
		apply := func() {
			for _, u := range users {
				b.Store.SetUserQuota(u.ID, u.Quota)
			}
		}
		if b.Syncer == nil || controlSession == nil {
			apply()
			return nil, nil
		}
		_ = b.Syncer.withCurrentControlSession(controlSession, func() error {
			apply()
			return nil
		})
		return nil, nil
	})

	b.Client.OnNotification(consts.RPCSyncForceFullSync, func(ctx context.Context, params json.RawMessage) (any, error) {
		if b.Syncer == nil {
			return nil, fmt.Errorf("syncer not initialized")
		}
		start := time.Now()
		if err := b.Syncer.FullSyncForSession(ctx, controlSession); err != nil {
			return nil, err
		}
		return protocol.ForceFullSyncResponse{
			Version:    b.Store.Version(),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	})
}

func (b *WSBridge) handleAgentCapabilities(params json.RawMessage) {
	if b.SetAgentCapabilities == nil {
		return
	}
	var update protocol.AgentCapabilitiesUpdate
	if err := json.Unmarshal(params, &update); err != nil {
		return
	}
	b.SetAgentCapabilities(update.AgentID, update.Capabilities)
}

func (b *WSBridge) failSessionSync(expected *ControlSession, err error) bool {
	if b.Syncer == nil || err == nil {
		return false
	}
	return b.Syncer.withCurrentControlSession(expected, func() error {
		b.Syncer.failAgentSync(err)
		b.Syncer.failAgentRouteSync(err)
		return nil
	}) == nil
}

func (b *WSBridge) handleAutoAddrUpdate(params json.RawMessage) error {
	var update protocol.AgentDirectAddressesUpdate
	if err := json.Unmarshal(params, &update); err != nil {
		return err
	}
	if b.ApplyDirectAddresses == nil {
		return nil
	}
	b.ApplyDirectAddresses(update)
	return nil
}
