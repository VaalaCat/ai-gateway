package oauth

import (
	"testing"
	"time"
)

const testSecret = "test-secret"

func TestBindTicketRoundTrip(t *testing.T) {
	tk, err := SignBindTicket(testSecret, BindTicketClaims{
		ProviderID:        1,
		Subject:           "sub-1",
		Email:             "a@b",
		DisplayName:       "Alice",
		SuggestedUsername: "alice",
		ExpiresAt:         time.Now().Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := ParseBindTicket(testSecret, tk)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.ProviderID != 1 || got.Subject != "sub-1" || got.SuggestedUsername != "alice" {
		t.Fatalf("got: %+v", got)
	}
}

func TestBindTicket_RejectExpired(t *testing.T) {
	tk, _ := SignBindTicket(testSecret, BindTicketClaims{ExpiresAt: time.Now().Add(-1 * time.Second).Unix()})
	if _, err := ParseBindTicket(testSecret, tk); err == nil {
		t.Fatal("expected expired error")
	}
}

func TestBindTicket_RejectWrongKind(t *testing.T) {
	tk, _ := SignLinkTicket(testSecret, LinkTicketClaims{UserID: 1, ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if _, err := ParseBindTicket(testSecret, tk); err == nil {
		t.Fatal("expected wrong-kind error")
	}
}

func TestLinkTicketRoundTrip(t *testing.T) {
	tk, err := SignLinkTicket(testSecret, LinkTicketClaims{UserID: 7, ExpiresAt: time.Now().Add(2 * time.Minute).Unix()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := ParseLinkTicket(testSecret, tk)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.UserID != 7 {
		t.Fatalf("got %d", got.UserID)
	}
}
