package oauth

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type LinkTicketResponse struct {
	Ticket string `json:"ticket"`
}

func (h *Handler) IssueLinkTicket(c *app.Context, _ api.EmptyRequest) (LinkTicketResponse, error) {
	if c.UserInfo == nil {
		return LinkTicketResponse{}, api.UnauthorizedError("missing auth")
	}
	tk, err := SignLinkTicket(h.JWTSecret, LinkTicketClaims{
		UserID:    c.UserInfo.UserID,
		ExpiresAt: time.Now().Unix() + linkTicketTTL,
	})
	if err != nil {
		return LinkTicketResponse{}, api.InternalError("sign ticket failed", err)
	}
	return LinkTicketResponse{Ticket: tk}, nil
}
