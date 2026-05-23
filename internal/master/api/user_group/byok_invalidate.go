package user_group

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

// fanoutBYOKInvalidateForGroup looks up every user belonging to groupID and
// publishes a PrivateChannelInvalidate event covering all of them. Callers
// invoke this after a UserGroup.Update flips byok_enabled so agent
// visiblePrivateChannels LRU caches drop their entries immediately rather than
// waiting for natural TTL expiry. Empty groups are a no-op. Bus may be nil
// (handler not wired to an event bus) — callers should still treat that as
// success since there is no audience to notify.
func fanoutBYOKInvalidateForGroup(ctx context.Context, q dao.AdminQuery, bus app.EventBus, groupID uint) error {
	users, err := q.User().ListByGroupIDs([]uint{groupID})
	if err != nil {
		return err
	}
	if len(users) == 0 || bus == nil {
		return nil
	}
	ids := make([]uint, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	return events.PublishPrivateChannelInvalidate(ctx, bus, ids)
}
