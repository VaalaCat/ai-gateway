package private_channel

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[DetailResponse], error) {
	if c.UserInfo == nil {
		return api.Created[DetailResponse]{}, api.UnauthorizedError("not authenticated")
	}
	rows, err := createPrivateChannels(c, h.Provider, PrivateChannelOwner{
		UserID:  c.UserInfo.UserID,
		GroupID: c.UserInfo.GroupID,
	}, []PrivateChannelCreateInput{buildPrivateChannelCreateInput(req)})
	if err != nil {
		return api.Created[DetailResponse]{}, err
	}
	return api.Created[DetailResponse]{Value: toDetailResponse(&rows[0])}, nil
}
