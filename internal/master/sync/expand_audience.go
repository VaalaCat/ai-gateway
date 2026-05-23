package sync

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
)

// ExpandPrivateChannelAudience returns the deduplicated set of user IDs whose
// visiblePrivateChannels cache must be invalidated when a private channel is
// created/updated/deleted/shared:
//   - the owner
//   - users directly targeted by share rows (target_type='user')
//   - members of groups targeted by share rows (target_type='group')
//
// Called by master CRUD handlers before publishing the invalidate event.
// channelID may not exist (e.g., for pre-delete computation); in that case
// only the owner is returned.
func ExpandPrivateChannelAudience(q dao.AdminQuery, channelID uint, ownerID uint) ([]uint, error) {
	seen := map[uint]struct{}{ownerID: {}}

	shares, err := q.PrivateChannelShare().ListSharesByChannel(channelID)
	if err != nil {
		return nil, err
	}
	var groupIDs []uint
	for _, s := range shares {
		switch s.TargetType {
		case "user":
			seen[s.TargetID] = struct{}{}
		case "group":
			groupIDs = append(groupIDs, s.TargetID)
		}
	}

	if len(groupIDs) > 0 {
		users, err := q.User().ListByGroupIDs(groupIDs)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			seen[u.ID] = struct{}{}
		}
	}

	out := make([]uint, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out, nil
}
