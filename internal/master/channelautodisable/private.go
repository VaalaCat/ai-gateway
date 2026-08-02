package channelautodisable

import (
	"context"
	"errors"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	mastersync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *Service) privateHandler(application app.Application, bus app.EventBus) sourceHandler {
	return func(ctx context.Context, trigger attemptproxy.ChannelAutoDisableTrigger) error {
		daoCtx := dao.NewContextWithContext(application, ctx)
		mutation := dao.NewAdminMutation(daoCtx).PrivateChannel()
		result, err := mutation.AutoDisable(trigger.ChannelID, trigger.Revision, disableState(time.Now()))
		if err != nil {
			return err
		}
		query := dao.NewAdminQuery(daoCtx)
		if !result.Updated {
			_, err := query.PrivateChannel().GetByID(trigger.ChannelID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				s.logger.Debug("private channel auto-disable target no longer exists", triggerLogFields(trigger)...)
				return nil
			}
			if err != nil {
				return err
			}
			s.logger.Debug("private channel auto-disable trigger is stale or already applied", triggerLogFields(trigger)...)
			return nil
		}

		s.logger.Info("channel auto disabled", append(triggerLogFields(trigger), zap.String("event", "channel_auto_disabled"))...)
		if err := mastersync.PublishPrivateChannelMutation(ctx, query, bus, trigger.ChannelID, result.OwnerID); err != nil {
			s.logger.Warn("publish auto-disabled private channel invalidation failed", append(triggerLogFields(trigger), zap.Error(err))...)
		}
		return nil
	}
}
