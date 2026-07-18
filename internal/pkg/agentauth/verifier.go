package agentauth

import (
	"crypto/ed25519"
	"errors"
	"reflect"

	"github.com/golang-jwt/jwt/v5"
)

const (
	relayTicketAudience     = "agent-relay"
	forwardTicketAudience   = "agent-forward"
	forwardTicketCapability = "agent_forward_ticket_v1"
)

var (
	errInvalidTicket        = errors.New("agentauth: invalid ticket")
	errInvalidRelayClaims   = errors.New("agentauth: invalid relay claims")
	errInvalidForwardClaims = errors.New("agentauth: invalid forward claims")
	errInvalidWelcomeClaims = errors.New("agentauth: invalid welcome proof claims")
)

type KeySource interface {
	LookupKey(keyID string) (ed25519.PublicKey, bool)
}

type Verifier struct {
	keys KeySource
}

func NewVerifier(keys KeySource) *Verifier {
	return &Verifier{keys: keys}
}

func (v *Verifier) VerifyRelay(
	raw RelayTicket,
	expectedAgentID string,
	expectedMasterID string,
	desiredGeneration uint64,
) (*RelayClaims, error) {
	claims := &RelayClaims{}
	if err := v.parse(string(raw), claims,
		jwt.WithAudience(relayTicketAudience),
		jwt.WithExpirationRequired(),
	); err != nil {
		return nil, err
	}
	if !hasExactAudience(claims.Audience, relayTicketAudience) ||
		claims.AgentID == "" ||
		claims.MasterInstanceID == "" ||
		claims.AgentID != expectedAgentID ||
		claims.MasterInstanceID != expectedMasterID ||
		claims.DesiredGeneration != desiredGeneration {
		return nil, errInvalidRelayClaims
	}
	return claims, nil
}

func (v *Verifier) VerifyForward(raw ForwardTicket) (*ForwardClaims, error) {
	claims := &ForwardClaims{}
	if err := v.parse(string(raw), claims,
		jwt.WithAudience(forwardTicketAudience),
		jwt.WithExpirationRequired(),
	); err != nil {
		return nil, err
	}
	if !hasExactAudience(claims.Audience, forwardTicketAudience) ||
		claims.SourceAgentID == "" ||
		claims.Capability != forwardTicketCapability {
		return nil, errInvalidForwardClaims
	}
	return claims, nil
}

func (v *Verifier) VerifyWelcome(raw string, expected WelcomeProofClaims) error {
	claims := &WelcomeProofClaims{}
	if err := v.parse(raw, claims); err != nil {
		return err
	}
	// behavior change: an audience-bearing token is not a welcome proof.
	if len(claims.Audience) != 0 ||
		claims.AgentID == "" ||
		claims.Nonce == "" ||
		claims.MasterInstanceID == "" ||
		claims.AgentID != expected.AgentID ||
		claims.Nonce != expected.Nonce ||
		claims.MasterInstanceID != expected.MasterInstanceID ||
		claims.SessionGeneration != expected.SessionGeneration ||
		claims.DesiredGeneration != expected.DesiredGeneration {
		return errInvalidWelcomeClaims
	}
	return nil
}

func (v *Verifier) parse(raw string, claims jwt.Claims, options ...jwt.ParserOption) error {
	if v == nil || isNilKeySource(v.keys) || raw == "" {
		return errInvalidTicket
	}
	parserOptions := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuedAt(),
	}
	parserOptions = append(parserOptions, options...)
	token, err := jwt.ParseWithClaims(raw, claims, v.lookupKey, parserOptions...)
	if err != nil || token == nil || !token.Valid {
		return errInvalidTicket
	}
	return nil
}

func (v *Verifier) lookupKey(token *jwt.Token) (any, error) {
	if token == nil || token.Method == nil || token.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
		return nil, errInvalidTicket
	}
	keyID, ok := token.Header["kid"].(string)
	if !ok || keyID == "" {
		return nil, errInvalidTicket
	}
	key, ok := v.keys.LookupKey(keyID)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, errInvalidTicket
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

func hasExactAudience(audience jwt.ClaimStrings, expected string) bool {
	return len(audience) == 1 && audience[0] == expected
}

func isNilKeySource(keys KeySource) bool {
	if keys == nil {
		return true
	}
	value := reflect.ValueOf(keys)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
