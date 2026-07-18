package oauth

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) HandleCallback(c *gin.Context) {
	providerName := c.Param("provider")

	// IdP 主动报错（access_denied 等）
	if idpErr := c.Query("error"); idpErr != "" {
		h.redirectLoginError(c, ErrIdPError)
		return
	}

	queryState := c.Query("state")
	cookie, err := c.Cookie(cookieState)
	if err != nil || cookie == "" || cookie != queryState {
		h.redirectLoginError(c, ErrInvalidState)
		return
	}
	entry, ok := h.StateStore.Take(queryState)
	if !ok {
		h.redirectLoginError(c, ErrInvalidState)
		return
	}

	matched, ok := h.Allowlist.Match(resolveRequestOrigin(c.Request))
	if !ok {
		h.redirectLoginError(c, ErrUntrustedOrigin)
		return
	}

	p, err := h.lookupEnabledProvider(c.Request.Context(), providerName)
	if err != nil || p.ID != entry.ProviderID {
		h.redirectLoginError(c, ErrUnknownProvider)
		return
	}

	ctx := c.Request.Context()
	adapter := h.IDP.For(p)
	tok, err := adapter.ExchangeCode(ctx, p, c.Query("code"), buildRedirectURI(matched, p.Name))
	if err != nil {
		h.redirectLoginError(c, ErrTokenExchangeFailed)
		return
	}
	if tok.IDToken != "" {
		if err := h.Verifier.Verify(ctx, tok.IDToken, p.JWKSURI, p.Issuer, p.ClientID); err != nil {
			h.redirectLoginError(c, ErrIDTokenInvalid)
			return
		}
	}
	info, err := adapter.FetchUserinfo(ctx, p, tok.AccessToken)
	if err != nil {
		h.redirectLoginError(c, ErrUserinfoFailed)
		return
	}
	if info.Sub == "" {
		h.redirectLoginError(c, ErrMissingSub)
		return
	}

	switch entry.Kind {
	case "login":
		h.handleLoginCallback(c, p, info, entry)
	case "link":
		h.handleLinkCallback(c, p, info, entry)
	default:
		h.redirectLoginError(c, ErrInvalidState)
	}
}

func (h *Handler) handleLoginCallback(c *gin.Context, p *models.OAuthProvider, info *UserinfoPayload, entry *StateEntry) {
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.Request.Context()))
	ident, found, err := q.OAuthIdentity().GetByProviderSubject(p.ID, info.Sub)
	if err != nil {
		h.redirectLoginError(c, ErrUserinfoFailed)
		return
	}
	if found {
		if _, err := q.User().GetByID(ident.UserID); err == nil {
			h.completeLogin(c, ident.UserID, entry.ReturnTo)
			return
		}
		// orphan: 关联 user 已被删，静默清掉该 user_id 下全部 identities，
		// fallthrough 走未绑定分支让用户重新创建或绑定本地账号。
		m := dao.NewAdminMutation(dao.NewContextWithContext(h.App, c.Request.Context()))
		_ = m.OAuthIdentity().DeleteByUserID(ident.UserID)
	}

	suggested, _ := ResolveUsername(*info, func(u string) (bool, error) {
		_, err := q.User().GetByUsername(u)
		return err == nil, nil
	})
	tk, err := SignBindTicket(h.JWTSecret, BindTicketClaims{
		ProviderID:        p.ID,
		Subject:           info.Sub,
		Email:             info.Email,
		DisplayName:       info.Name,
		Picture:           info.Picture,
		SuggestedUsername: suggested,
		ExpiresAt:         time.Now().Unix() + bindTicketTTL,
	})
	if err != nil {
		h.redirectLoginError(c, ErrTokenExchangeFailed)
		return
	}

	dest := "/oauth/bind"
	if h.readAutoCreateSetting(c.Request.Context()) {
		dest = "/oauth/choose"
	}
	c.Redirect(http.StatusFound, dest+"?ticket="+url.QueryEscape(tk))
}

func (h *Handler) completeLogin(c *gin.Context, userID uint, returnTo string) {
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.Request.Context()))
	user, err := q.User().GetByID(userID)
	if err != nil {
		h.redirectLoginError(c, ErrUserinfoFailed)
		return
	}
	if user.Status != consts.StatusEnabled {
		h.redirectLoginError(c, ErrUserinfoFailed)
		return
	}
	jwtToken, err := middleware.GenerateToken(h.JWTSecret, user.ID, user.Role, user.Username, user.DisplayName, user.AvatarURL)
	if err != nil {
		h.redirectLoginError(c, ErrTokenExchangeFailed)
		return
	}
	if returnTo == "" {
		returnTo = "/dashboard"
	}
	loc := "/oauth/success?token=" + url.QueryEscape(jwtToken) + "&return_to=" + url.QueryEscape(returnTo)
	c.Redirect(http.StatusFound, loc)
}

func (h *Handler) readAutoCreateSetting(ctx context.Context) bool {
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, ctx))
	s, found, err := q.Setting().Lookup("oauth_auto_create")
	return err == nil && found && s.Value == "true"
}

func (h *Handler) createUserFromClaims(requestCtx context.Context, claims BindTicketClaims) (uint, error) {
	var newID uint
	err := dao.RunInTx[dao.Context](dao.NewContextWithContext(h.App, requestCtx), func(ctx dao.Context) error {
		q := dao.NewAdminQuery(ctx)
		info := UserinfoPayload{
			Sub:               claims.Subject,
			Email:             claims.Email,
			Name:              claims.DisplayName,
			PreferredUsername: claims.SuggestedUsername,
		}
		username, mErr := ResolveUsername(info, func(u string) (bool, error) {
			_, e := q.User().GetByUsername(u)
			return e == nil, nil
		})
		if mErr != nil {
			return mErr
		}
		email := strings.ToLower(strings.TrimSpace(claims.Email))
		if email != "" {
			if _, err := q.User().GetByEmail(email); err == nil {
				email = ""
			}
		}
		hashed, err := bcrypt.GenerateFromPassword(randomBytes(32), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		u := &models.User{
			Username:    username,
			Email:       email,
			DisplayName: claims.DisplayName,
			AvatarURL:   claims.Picture,
			Password:    string(hashed),
			PasswordSet: false,
			Role:        consts.RoleUser,
			Status:      consts.StatusEnabled,
			GroupID:     1,
		}
		m := dao.NewAdminMutation(ctx)
		if err := m.User().Create(u); err != nil {
			return err
		}
		ident := &models.OAuthIdentity{
			UserID:      u.ID,
			ProviderID:  claims.ProviderID,
			Subject:     claims.Subject,
			Email:       claims.Email,
			DisplayName: claims.DisplayName,
		}
		if err := m.OAuthIdentity().Create(ident); err != nil {
			return err
		}
		newID = u.ID
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

func (h *Handler) handleLinkCallback(c *gin.Context, p *models.OAuthProvider, info *UserinfoPayload, entry *StateEntry) {
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.Request.Context()))
	ident, found, err := q.OAuthIdentity().GetByProviderSubject(p.ID, info.Sub)
	if err != nil {
		h.redirectError(c, "/profile", ErrUserinfoFailed)
		return
	}
	if found {
		if ident.UserID == entry.UserID {
			c.Redirect(http.StatusFound, "/profile")
			return
		}
		h.redirectError(c, "/profile", ErrAlreadyLinked)
		return
	}
	m := dao.NewAdminMutation(dao.NewContextWithContext(h.App, c.Request.Context()))
	newIdent := &models.OAuthIdentity{
		UserID: entry.UserID, ProviderID: p.ID, Subject: info.Sub,
		Email: info.Email, DisplayName: info.Name,
	}
	if err := m.OAuthIdentity().Create(newIdent); err != nil {
		h.redirectError(c, "/profile", ErrAlreadyLinked)
		return
	}
	c.Redirect(http.StatusFound, "/profile?oauth_linked="+url.QueryEscape(p.Name))
}

func (h *Handler) redirectError(c *gin.Context, base, code string) {
	c.Redirect(http.StatusFound, base+"?oauth_error="+code)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("rand: %w", err))
	}
	return b
}
