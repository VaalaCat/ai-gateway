package channelautodisable

import (
	"context"
	"errors"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *Service) adminHandler(application app.Application, bus app.EventBus) sourceHandler {
	return func(ctx context.Context, trigger attemptproxy.ChannelAutoDisableTrigger) error {
		daoCtx := dao.NewContextWithContext(application, ctx)
		result, err := dao.NewAdminMutation(daoCtx).Channel().AutoDisable(
			trigger.ChannelID, trigger.Revision, disableState(time.Now()),
		)
		if err != nil {
			return err
		}
		query := dao.NewAdminQuery(daoCtx).Channel()
		channel, err := query.GetByID(trigger.ChannelID)
		if !result.Updated {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				s.logger.Debug("channel auto-disable target no longer exists", triggerLogFields(trigger)...)
				return nil
			}
			if err != nil {
				return err
			}
			s.logger.Debug("channel auto-disable trigger is stale or already applied", triggerLogFields(trigger)...)
			return nil
		}
		if err != nil {
			return err
		}

		s.logger.Info("channel auto disabled", append(triggerLogFields(trigger), zap.String("event", "channel_auto_disabled"))...)
		if err := events.PublishChannelUpdate(ctx, bus, *channel); err != nil {
			s.logger.Warn("publish auto-disabled channel update failed", append(triggerLogFields(trigger), zap.Error(err))...)
		}
		return nil
	}
}
