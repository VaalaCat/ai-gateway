package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

var _ app.Syncer = (*Syncer)(nil)

type Syncer struct {
	Store            *Store
	Client           app.WSClient
	Bus              app.EventBus
	Logger           *zap.Logger
	FullSyncInterval time.Duration
	mu               sync.Mutex
}

func NewSyncer(store *Store, client app.WSClient, bus app.EventBus, logger *zap.Logger, interval time.Duration) *Syncer {
	return &Syncer{
		Store:            store,
		Client:           client,
		Bus:              bus,
		Logger:           logger,
		FullSyncInterval: interval,
	}
}

// SetClient replaces the WS client (e.g., after reconnection)
func (s *Syncer) SetClient(client app.WSClient) {
	s.mu.Lock()
	s.Client = client
	s.mu.Unlock()
}

func (s *Syncer) getClient() app.WSClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Client
}

// FullSync pulls all data from master.
//
// LRU 模式实体（token / user / private_channel）不参与 FullSync——它们靠 push
// invalidate + miss 时 RPC 拉取保持最新。全量重拉违背 LRU 容量目标，会让 agent
// 内存重新涨回基线。
func (s *Syncer) FullSync(ctx context.Context) error {
	if err := s.fullSyncEntity(ctx, "user_group"); err != nil {
		s.Logger.Error("full sync user_group failed", zap.Error(err))
	}
	if err := s.fullSyncEntity(ctx, "channel"); err != nil {
		return err
	}
	if err := s.fullSyncEntity(ctx, "model_config"); err != nil {
		return err
	}
	if err := s.fullSyncEntity(ctx, "setting"); err != nil {
		return err
	}
	if err := s.fullSyncEntity(ctx, "agent"); err != nil {
		s.Logger.Error("full sync agent failed", zap.Error(err))
	}
	if err := s.fullSyncEntity(ctx, "agent_route"); err != nil {
		s.Logger.Error("full sync agent_route failed", zap.Error(err))
	}
	if err := s.fullSyncEntity(ctx, "model_routing"); err != nil {
		s.Logger.Error("full sync model_routing failed", zap.Error(err))
	}
	s.Store.RebuildModelIndex()
	s.Logger.Info("full sync complete (LRU entities skipped)",
		zap.Int("user_groups", s.Store.UserGroupCount()),
		zap.Int("channels", s.Store.ChannelCount()),
		zap.Int("models", s.Store.ModelConfigCount()),
		zap.Int("global_routings", s.Store.GlobalRoutingCount()),
		zap.Int64("version", s.Store.Version()),
	)
	return nil
}

func (s *Syncer) fullSyncEntity(ctx context.Context, entity string) error {
	client := s.getClient()
	if client == nil {
		return fmt.Errorf("no ws client")
	}
	page := 1
	for {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := client.Call(callCtx, consts.RPCSyncFullSync, protocol.FullSyncRequest{
			Entity:   entity,
			Page:     page,
			PageSize: 500,
		})
		cancel()
		if err != nil {
			return err
		}

		var resp protocol.FullSyncResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return err
		}

		switch entity {
		case "user_group":
			var items []models.UserGroup
			json.Unmarshal(resp.Items, &items)
			s.Store.LoadUserGroups(items)
		case "channel":
			var items []models.Channel
			json.Unmarshal(resp.Items, &items)
			s.Store.LoadChannels(items)
		case "model_config":
			var items []models.ModelConfig
			json.Unmarshal(resp.Items, &items)
			s.Store.LoadModelConfigs(items)
		case "setting":
			var items []models.Setting
			json.Unmarshal(resp.Items, &items)
			s.Store.LoadSettings(items)
		case "agent":
			var items []models.Agent
			json.Unmarshal(resp.Items, &items)
			s.Store.LoadAgents(items)
		case "agent_route":
			var items []*models.AgentRoute
			json.Unmarshal(resp.Items, &items)
			s.Store.RouteIndex.Load(items)
		case "model_routing":
			var items []models.ModelRouting
			json.Unmarshal(resp.Items, &items)
			s.Store.LoadGlobalRoutings(items)
		}

		s.Store.SetVersion(resp.Version)

		if !resp.HasMore {
			break
		}
		page++
	}
	return nil
}

// SubscribeEvents subscribes to local EventBus for sync events from WSBridge
func (s *Syncer) SubscribeEvents() {
	patterns := []events.Pattern[protocol.SyncPushParams]{
		events.SyncTokenAllPattern,
		events.SyncChannelAllPattern,
		events.SyncModelAllPattern,
		events.SyncModelConfigAllPattern,
		events.SyncSettingAllPattern,
		events.SyncAgentAllPattern,
		events.SyncAgentRouteAllPattern,
		events.SyncUserGroupAllPattern,
		events.SyncUserAllPattern,
		events.SyncModelRoutingAllPattern,
	}

	for _, pattern := range patterns {
		_, err := events.SubscribeSyncPushPattern(s.Bus, pattern, func(ctx context.Context, params protocol.SyncPushParams) error {
			s.Store.HandleSyncEvent(params.Entity, params.Action, params.Data)
			s.Store.SetVersion(params.Version)
			s.Logger.Debug("sync event applied",
				zap.String("entity", params.Entity),
				zap.String("action", params.Action),
				zap.Int64("version", params.Version),
			)
			return nil
		})
		if err != nil {
			s.Logger.Error("subscribe sync pattern failed",
				zap.String("pattern", pattern.Value()),
				zap.Error(err),
			)
		}
	}
}

// StartPeriodicCheck starts periodic version comparison
func (s *Syncer) StartPeriodicCheck(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.FullSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.checkVersion(ctx)
			}
		}
	}()
}

func (s *Syncer) checkVersion(ctx context.Context) {
	client := s.getClient()
	if client == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := client.Call(callCtx, consts.RPCSyncGetVersion, nil)
	if err != nil {
		s.Logger.Error("version check failed", zap.Error(err))
		return
	}
	var resp protocol.GetVersionResponse
	json.Unmarshal(result, &resp)

	if resp.Version != s.Store.Version() {
		s.Logger.Info("version mismatch, triggering full sync",
			zap.Int64("local", s.Store.Version()),
			zap.Int64("remote", resp.Version),
		)
		s.FullSync(ctx)
	}
}
