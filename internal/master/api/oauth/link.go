package oauth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Handler) HandleLink(c *gin.Context) {
	providerName := c.Param("provider")
	rawTicket := c.Query("ticket")
	claims, err := ParseLinkTicket(h.JWTSecret, rawTicket)
	if err != nil {
		h.redirectError(c, "/profile", "ticket_invalid")
		return
	}
	matched, ok := h.Allowlist.Match(resolveRequestOrigin(c.Request))
	if !ok {
		h.redirectLoginError(c, ErrUntrustedOrigin)
		return
	}
	p, err := h.lookupEnabledProvider(providerName)
	if err != nil {
		h.redirectLoginError(c, ErrUnknownProvider)
		return
	}
	if !providerConfigComplete(p) {
		h.redirectLoginError(c, ErrProviderMisconfigured)
		return
	}
	state, err := newRandomState()
	if err != nil {
		h.redirectLoginError(c, ErrInvalidState)
		return
	}
	h.StateStore.Put(state, &StateEntry{
		ProviderID: p.ID,
		Kind:       "link",
		UserID:     claims.UserID,
		ReturnTo:   "/profile",
		ExpiresAt:  time.Now().Unix() + stateTTL,
	})
	writeStateCookie(c, state, matched)
	c.Redirect(http.StatusFound, buildAuthorizeURL(p, state, matched))
}
