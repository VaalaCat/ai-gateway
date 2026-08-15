package genericapi

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

type UserFactFinder interface {
	FindUser(context.Context, uint) (*protocol.SyncedUser, bool, error)
}

type SettingsFinder interface {
	Settings() settings.AgentSettings
}

type QuotaGate struct {
	users    UserFactFinder
	settings SettingsFinder
}

func NewQuotaGate(users UserFactFinder, settings SettingsFinder) *QuotaGate {
	return &QuotaGate{users: users, settings: settings}
}

func (g *QuotaGate) Allow(ctx context.Context, userID uint, service protocol.SyncedAPIService) error {
	if !service.ConsumesQuota || userID == 0 {
		return nil
	}
	if g == nil || g.users == nil || g.settings == nil {
		return ErrQuotaFactsUnavailable
	}
	user, found, err := g.users.FindUser(ctx, userID)
	if err != nil || !found || user == nil {
		return ErrQuotaFactsUnavailable
	}
	if user.Quota <= g.settings.Settings().MinQuotaReserve {
		return ErrInsufficientQuota
	}
	return nil
}
