package oauth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	ticketKindBind = "oauth_bind"
	ticketKindLink = "oauth_link"
)

type BindTicketClaims struct {
	ProviderID        uint
	Subject           string
	Email             string
	DisplayName       string
	Picture           string
	SuggestedUsername string
	ExpiresAt         int64
}

type LinkTicketClaims struct {
	UserID    uint
	ExpiresAt int64
}

func SignBindTicket(secret string, c BindTicketClaims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"kind":               ticketKindBind,
		"provider_id":        c.ProviderID,
		"sub":                c.Subject,
		"email":              c.Email,
		"display_name":       c.DisplayName,
		"picture":            c.Picture,
		"suggested_username": c.SuggestedUsername,
		"exp":                c.ExpiresAt,
	})
	return t.SignedString([]byte(secret))
}

func SignLinkTicket(secret string, c LinkTicketClaims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"kind":    ticketKindLink,
		"user_id": c.UserID,
		"exp":     c.ExpiresAt,
	})
	return t.SignedString([]byte(secret))
}

func ParseBindTicket(secret, raw string) (BindTicketClaims, error) {
	claims, err := parseTicket(secret, raw, ticketKindBind)
	if err != nil {
		return BindTicketClaims{}, err
	}
	return BindTicketClaims{
		ProviderID:        uintFromClaim(claims["provider_id"]),
		Subject:           strFromClaim(claims["sub"]),
		Email:             strFromClaim(claims["email"]),
		DisplayName:       strFromClaim(claims["display_name"]),
		Picture:           strFromClaim(claims["picture"]),
		SuggestedUsername: strFromClaim(claims["suggested_username"]),
		ExpiresAt:         intFromClaim(claims["exp"]),
	}, nil
}

func ParseLinkTicket(secret, raw string) (LinkTicketClaims, error) {
	claims, err := parseTicket(secret, raw, ticketKindLink)
	if err != nil {
		return LinkTicketClaims{}, err
	}
	return LinkTicketClaims{
		UserID:    uintFromClaim(claims["user_id"]),
		ExpiresAt: intFromClaim(claims["exp"]),
	}, nil
}

func parseTicket(secret, raw, expectedKind string) (jwt.MapClaims, error) {
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})
	if err != nil || !tok.Valid {
		return nil, errors.New("ticket invalid")
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("ticket claims invalid")
	}
	if kind, _ := claims["kind"].(string); kind != expectedKind {
		return nil, errors.New("ticket kind mismatch")
	}
	if exp := intFromClaim(claims["exp"]); exp > 0 && time.Now().Unix() > exp {
		return nil, errors.New("ticket expired")
	}
	return claims, nil
}

func uintFromClaim(v any) uint {
	switch n := v.(type) {
	case float64:
		return uint(n)
	case int:
		return uint(n)
	case int64:
		return uint(n)
	}
	return 0
}

func intFromClaim(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func strFromClaim(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
