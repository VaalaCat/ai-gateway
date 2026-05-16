package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
)

func (h *Handler) HandleAuthorize(c *gin.Context) {
	providerName := c.Param("provider")
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
		Kind:       "login",
		ReturnTo:   "/dashboard",
		ExpiresAt:  time.Now().Unix() + stateTTL,
	})
	writeStateCookie(c, state, matched)
	c.Redirect(http.StatusFound, buildAuthorizeURL(p, state, matched))
}

func (h *Handler) lookupEnabledProvider(name string) (*models.OAuthProvider, error) {
	q := dao.NewAdminQuery(dao.NewContext(h.App)).OAuthProvider()
	p, err := q.GetByName(name)
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		return nil, errors.New("provider disabled")
	}
	return p, nil
}

func providerConfigComplete(p *models.OAuthProvider) bool {
	return p.AuthorizationEndpoint != "" && p.TokenEndpoint != "" && p.UserinfoEndpoint != "" && p.ClientID != ""
}

func buildAuthorizeURL(p *models.OAuthProvider, state, origin string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", buildRedirectURI(origin, p.Name))
	if p.Scopes != "" {
		q.Set("scope", p.Scopes)
	}
	q.Set("state", state)
	sep := "?"
	if strings.Contains(p.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return p.AuthorizationEndpoint + sep + q.Encode()
}

func buildRedirectURI(origin, providerName string) string {
	return strings.TrimRight(origin, "/") + "/api/oauth/" + providerName + "/callback"
}

func writeStateCookie(c *gin.Context, state, origin string) {
	secure := strings.HasPrefix(origin, "https://")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieState,
		Value:    state,
		Path:     "/api/oauth/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) redirectLoginError(c *gin.Context, code string) {
	c.Redirect(http.StatusFound, "/login?oauth_error="+code)
}

func newRandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
