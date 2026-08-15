package apiusage

import (
	"context"
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type APIServiceFinder interface {
	FindByID(context.Context, uint) (*models.APIService, error)
}

type APIRequestLogQueue interface {
	Enqueue(logqueue.LogBatch) deliveryqueue.EnqueueResult
}

type APISettlement struct {
	RequestID     string
	Quota         int64
	SourceAgentID string
}

type APIUsageSettler struct {
	services APIServiceFinder
	app      dao.AppProvider
	bus      app.EventBus
	logger   *zap.Logger
	logs     APIRequestLogQueue
}

func NewAPIUsageSettler(services APIServiceFinder) *APIUsageSettler {
	return &APIUsageSettler{services: services, logger: zap.NewNop()}
}

func NewMasterAPIUsageSettler(application dao.AppProvider, bus app.EventBus, logger *zap.Logger, logs APIRequestLogQueue) *APIUsageSettler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &APIUsageSettler{services: apiServiceFinder{app: application}, app: application, bus: bus, logger: logger, logs: logs}
}

func (s *APIUsageSettler) Settle(ctx context.Context, entry protocol.APIUsageEntry) (APISettlement, error) {
	settlement := APISettlement{RequestID: entry.RequestID, SourceAgentID: entry.SourceAgentID}
	service, missing, err := s.findService(ctx, entry.APIServiceID)
	if err != nil {
		return settlement, err
	}
	if service != nil && entry.ProviderDispatchKnown && entry.ProviderDispatched {
		settlement.Quota = service.PricePerCall
	}
	if s.app == nil {
		return settlement, nil
	}
	inserted, depleted, err := s.apply(ctx, entry, settlement)
	if err != nil || !inserted {
		return settlement, err
	}
	s.enqueueLog(entry, settlement, service, missing)
	s.publish(ctx, entry, settlement, depleted)
	return settlement, nil
}

func (s *APIUsageSettler) findService(ctx context.Context, id uint) (*models.APIService, bool, error) {
	if s.services == nil {
		return nil, false, fmt.Errorf("api service finder is unavailable")
	}
	service, err := s.services.FindByID(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, true, nil
	}
	return service, false, err
}

func (s *APIUsageSettler) apply(ctx context.Context, entry protocol.APIUsageEntry, settlement APISettlement) (bool, bool, error) {
	var inserted, depleted bool
	daoCtx := dao.NewContextWithContext(s.app, ctx)
	err := dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		inserted = true
		if settlement.Quota <= 0 || entry.UserID == 0 {
			return nil
		}
		remaining, err := dao.NewAdminMutation(txCtx).User().DeductQuota(entry.UserID, settlement.Quota)
		depleted = remaining < 0
		return err
	})
	return inserted, depleted, err
}

func (s *APIUsageSettler) enqueueLog(entry protocol.APIUsageEntry, settlement APISettlement, service *models.APIService, serviceMissing bool) {
	if s.logs == nil {
		return
	}
	batch, err := BuildLogBatch(entry, settlement, service)
	if err != nil {
		s.logger.Warn("build api request log batch failed", zap.String("request_id", entry.RequestID), zap.Error(err))
		return
	}
	batch.APIRequest.ServiceMissingAtSettlement = serviceMissing
	if result := s.logs.Enqueue(batch); !result.Accepted {
		s.logger.Warn("api request log delivery not accepted", zap.String("request_id", entry.RequestID), zap.Bool("dropped", result.Dropped), zap.String("reason", result.Error))
	}
}

func (s *APIUsageSettler) publish(ctx context.Context, entry protocol.APIUsageEntry, settlement APISettlement, depleted bool) {
	if s.bus == nil || entry.UserID == 0 {
		return
	}
	var user models.User
	if err := s.app.GetCoreDB().WithContext(ctx).First(&user, entry.UserID).Error; err == nil {
		groupID := user.GroupID
		if groupID == 0 {
			groupID = 1
		}
		if err := events.PublishUserQuotaSync(ctx, s.bus, protocol.UserQuotaSync{AgentID: settlement.SourceAgentID, Users: []protocol.SyncedUser{{ID: user.ID, GroupID: groupID, Quota: user.Quota}}}); err != nil {
			s.logger.Warn("publish api user.quota_synced failed", zap.Error(err))
		}
	}
	if depleted {
		if err := events.PublishUserQuotaDepleted(ctx, s.bus, models.User{ID: entry.UserID}); err != nil {
			s.logger.Warn("publish api user.quota_depleted failed", zap.Error(err))
		}
	}
}

type apiServiceFinder struct{ app dao.AppProvider }

func (f apiServiceFinder) FindByID(ctx context.Context, id uint) (*models.APIService, error) {
	return dao.NewAdminQuery(dao.NewContextWithContext(f.app, ctx)).APIService().GetByID(id)
}
