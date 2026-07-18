package agentproxy

import (
	"crypto/ed25519"
	"errors"
	"slices"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

var errForwardTicketInvalid = errors.New("forward_ticket_invalid")

type ForwardAuthSnapshot struct {
	Capabilities []string
	SigningKeys  []agentauth.PublicKey
}

func (s ForwardAuthSnapshot) SupportsForwardTickets() bool {
	return slices.Contains(s.Capabilities, protocol.AgentCapabilityForwardV1)
}

func VerifyForwardTicket(snapshot ForwardAuthSnapshot, raw agentauth.ForwardTicket) (*agentauth.ForwardClaims, error) {
	if !snapshot.SupportsForwardTickets() || len(snapshot.SigningKeys) == 0 || raw == "" {
		return nil, errForwardTicketInvalid
	}
	claims, err := agentauth.NewVerifier(forwardTicketKeySource(snapshot.SigningKeys)).VerifyForward(raw)
	if err != nil {
		return nil, errForwardTicketInvalid
	}
	return claims, nil
}

type forwardTicketKeySource []agentauth.PublicKey

func (s forwardTicketKeySource) LookupKey(keyID string) (ed25519.PublicKey, bool) {
	for _, key := range s {
		if key.KeyID == keyID && key.Algorithm == protocol.AgentAuthAlgorithmEdDSA && len(key.Key) == ed25519.PublicKeySize {
			return append(ed25519.PublicKey(nil), key.Key...), true
		}
	}
	return nil, false
}
