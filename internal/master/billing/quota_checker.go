package billing

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

var _ app.QuotaChecker = (*QuotaChecker)(nil)

type QuotaChecker struct {
	App    dao.AppProvider
	Bus    app.EventBus
	Logger *zap.Logger
}

func NewQuotaChecker(application dao.AppProvider, bus app.EventBus, logger *zap.Logger) *QuotaChecker {
	return &QuotaChecker{App: application, Bus: bus, Logger: logger}
}

func (q *QuotaChecker) Start() {
	events.SubscribeUserQuotaDepleted(q.Bus, func(ctx context.Context, user models.User) error {
		q.Logger.Warn("quota depleted, disabling tokens", zap.Uint("user_id", user.ID))

		daoCtx := dao.NewContext(q.App)

		// Disable all tokens for this user
		if err := dao.NewAdminMutation(daoCtx).Token().DisableAllForUser(user.ID); err != nil {
			q.Logger.Error("disable tokens failed", zap.Error(err))
			return err
		}

		// Fetch tokens to publish update events
		userID := user.ID
		tokens, _, err := dao.NewAdminQuery(daoCtx).Token().List(
			dao.ListOptions{Page: 1, PageSize: 10000},
			dao.TokenListFilter{UserID: &userID},
		)
		if err != nil {
			q.Logger.Error("list tokens failed", zap.Error(err))
			return err
		}
		for _, token := range tokens {
			if err := events.PublishTokenUpdate(ctx, q.Bus, token); err != nil {
				q.Logger.Error("publish token.update failed", zap.Error(err))
			}
		}
		return nil
	})
}
