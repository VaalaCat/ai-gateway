package modelview

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"testing"
)

func TestRealCandidatesDoNotShadowModelsOrBypassWhitelists(t *testing.T) {
	s := &stubStore{adminModels: []string{"same", "blocked"}, globalRouting: []string{"same", "alias"}, channelsByMdl: map[string][]*models.Channel{
		"same":    {{ChannelCore: models.ChannelCore{ID: 1, Status: 1}}},
		"blocked": {{ChannelCore: models.ChannelCore{ID: 2, Status: 1}}},
	}}
	ui := &app.UserInfo{UserID: 1, AllowedChannelIDs: []uint{1}, TokenModels: []string{"same", "alias"}}
	mustEqual(t, asNamesAndOwners(ListVisibleRealModels(t.Context(), s, ui)), [][2]string{{"same", OwnedByAdmin}})
	mustEqual(t, asNamesAndOwners(ListVisibleModels(t.Context(), s, ui)), [][2]string{{"same", OwnedByRouting}, {"alias", OwnedByRouting}})
}
