package channel

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.Channel], error) {
	input, err := buildAdminChannelCreateInput(req)
	if err != nil {
		return api.Created[models.Channel]{}, err
	}
	channels, err := createAdminChannels(c, []AdminChannelCreateInput{input})
	if err != nil {
		return api.Created[models.Channel]{}, err
	}
	return api.Created[models.Channel]{Value: channels[0]}, nil
}
