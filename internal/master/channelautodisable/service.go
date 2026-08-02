package channelautodisable

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"go.uber.org/zap"
)

var _ billing.AutoDisableTriggerHandler = (*Service)(nil)

type sourceHandler func(context.Context, attemptproxy.ChannelAutoDisableTrigger) error

type Service struct {
	handlers map[attemptproxy.ChannelSource]sourceHandler
	logger   *zap.Logger
}

func New(application app.Application, bus app.EventBus, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	service := &Service{logger: logger}
	service.handlers = map[attemptproxy.ChannelSource]sourceHandler{
		attemptproxy.SourceAdmin:   service.adminHandler(application, bus),
		attemptproxy.SourcePrivate: service.privateHandler(application, bus),
	}
	return service
}

func (s *Service) DisableFromTriggers(ctx context.Context, triggers []attemptproxy.ChannelAutoDisableTrigger) error {
	var errs []error
	for _, trigger := range dedupeTriggers(triggers) {
		handler, err := s.handlerFor(trigger)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := handler(ctx, trigger); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) handlerFor(trigger attemptproxy.ChannelAutoDisableTrigger) (sourceHandler, error) {
	if err := trigger.Validate(); err != nil {
		return nil, fmt.Errorf("invalid channel auto-disable trigger: %w", err)
	}
	handler, ok := s.handlers[trigger.Source]
	if !ok {
		return nil, fmt.Errorf("unsupported channel auto-disable source %q", trigger.Source)
	}
	return handler, nil
}

func disableState(now time.Time) models.ChannelDisableState {
	return models.ChannelDisableState{
		Tripped:     true,
		Reason:      attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
		AutoRecover: false,
		TrippedAt:   now.Unix(),
	}
}

func dedupeTriggers(triggers []attemptproxy.ChannelAutoDisableTrigger) []attemptproxy.ChannelAutoDisableTrigger {
	if len(triggers) < 2 {
		return triggers
	}
	seen := make(map[attemptproxy.ChannelAutoDisableTrigger]struct{}, len(triggers))
	result := make([]attemptproxy.ChannelAutoDisableTrigger, 0, len(triggers))
	for _, trigger := range triggers {
		if _, ok := seen[trigger]; ok {
			continue
		}
		seen[trigger] = struct{}{}
		result = append(result, trigger)
	}
	return result
}

func triggerLogFields(trigger attemptproxy.ChannelAutoDisableTrigger) []zap.Field {
	return []zap.Field{
		zap.String("source", string(trigger.Source)),
		zap.Uint("channel_id", trigger.ChannelID),
		zap.Uint64("revision", trigger.Revision),
		zap.String("reason", trigger.Reason),
	}
}
