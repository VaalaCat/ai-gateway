package agentauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultRelayTTL       = 5 * time.Minute
	defaultForwardTTL     = 7 * 24 * time.Hour
	relayAudience         = "agent-relay"
	forwardAudience       = "agent-forward"
	forwardCapability     = "agent_forward_ticket_v1"
	activeSigningKeySlot  = uint8(1)
	signingAlgorithmEdDSA = "EdDSA"
)

var errInvalidSigningIdentity = errors.New("master agentauth: invalid signing identity")

type SigningKeyStore interface {
	LoadOrCreateActive(ctx context.Context) (*models.MasterSigningKey, error)
}

type SignerOptions struct {
	RelayTTL   time.Duration
	ForwardTTL time.Duration
	Now        func() time.Time
}

type Signer struct {
	instanceID string
	keyID      string
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	relayTTL   time.Duration
	forwardTTL time.Duration
	now        func() time.Time
}

func NewSigner(
	ctx context.Context,
	store SigningKeyStore,
	instanceID string,
	opts SignerOptions,
) (*Signer, error) {
	if ctx == nil {
		return nil, errors.New("master agentauth: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if isNilSigningKeyStore(store) {
		return nil, errors.New("master agentauth: signing key store is required")
	}
	if instanceID == "" {
		return nil, errors.New("master agentauth: instance ID is required")
	}
	if opts.RelayTTL < 0 || opts.ForwardTTL < 0 {
		return nil, errors.New("master agentauth: ticket TTL must not be negative")
	}

	relayTTL := opts.RelayTTL
	if relayTTL == 0 {
		relayTTL = defaultRelayTTL
	}
	forwardTTL := opts.ForwardTTL
	if forwardTTL == 0 {
		forwardTTL = defaultForwardTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	key, err := store.LoadOrCreateActive(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("master agentauth: load signing identity failed")
	}
	if err := validateSigningIdentity(key); err != nil {
		return nil, err
	}

	return &Signer{
		instanceID: instanceID,
		keyID:      key.KeyID,
		publicKey:  append(ed25519.PublicKey(nil), key.PublicKey...),
		privateKey: append(ed25519.PrivateKey(nil), key.PrivateKey...),
		relayTTL:   relayTTL,
		forwardTTL: forwardTTL,
		now:        now,
	}, nil
}

func (s *Signer) PublicKey() pkgagentauth.PublicKey {
	if s == nil {
		return pkgagentauth.PublicKey{}
	}
	return pkgagentauth.PublicKey{
		KeyID:     s.keyID,
		Algorithm: signingAlgorithmEdDSA,
		Key:       append([]byte(nil), s.publicKey...),
	}
}

func (s *Signer) SignRelay(agentID string, generation uint64) (pkgagentauth.RelayTicket, time.Time, error) {
	if s == nil {
		return "", time.Time{}, errors.New("master agentauth: signer is required")
	}
	if agentID == "" {
		return "", time.Time{}, errors.New("master agentauth: agent ID is required")
	}
	now := s.now()
	expiresAt := jwt.NewNumericDate(now.Add(s.relayTTL))
	claims := pkgagentauth.RelayClaims{
		AgentID:           agentID,
		MasterInstanceID:  s.instanceID,
		DesiredGeneration: generation,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{relayAudience},
			ExpiresAt: expiresAt,
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	raw, err := s.sign(&claims)
	if err != nil {
		return "", time.Time{}, err
	}
	return pkgagentauth.RelayTicket(raw), expiresAt.Time, nil
}

func (s *Signer) SignForward(sourceAgentID string) (pkgagentauth.ForwardTicket, time.Time, error) {
	if s == nil {
		return "", time.Time{}, errors.New("master agentauth: signer is required")
	}
	if sourceAgentID == "" {
		return "", time.Time{}, errors.New("master agentauth: source agent ID is required")
	}
	now := s.now()
	expiresAt := jwt.NewNumericDate(now.Add(s.forwardTTL))
	claims := pkgagentauth.ForwardClaims{
		SourceAgentID: sourceAgentID,
		Capability:    forwardCapability,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{forwardAudience},
			ExpiresAt: expiresAt,
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	raw, err := s.sign(&claims)
	if err != nil {
		return "", time.Time{}, err
	}
	return pkgagentauth.ForwardTicket(raw), expiresAt.Time, nil
}

func (s *Signer) SignWelcome(claims pkgagentauth.WelcomeProofClaims) ([]byte, error) {
	if s == nil {
		return nil, errors.New("master agentauth: signer is required")
	}
	if claims.AgentID == "" || claims.Nonce == "" || claims.MasterInstanceID == "" {
		return nil, errors.New("master agentauth: welcome proof identity fields are required")
	}
	if claims.MasterInstanceID != s.instanceID {
		return nil, errors.New("master agentauth: welcome proof master identity mismatch")
	}
	// behavior change: welcome proofs are audience-less so they cannot become relay tickets.
	if len(claims.Audience) != 0 {
		return nil, errors.New("master agentauth: welcome proof audience is not allowed")
	}
	raw, err := s.sign(&claims)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (s *Signer) sign(claims jwt.Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.keyID
	raw, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", errors.New("master agentauth: sign ticket failed")
	}
	return raw, nil
}

func validateSigningIdentity(key *models.MasterSigningKey) error {
	if key == nil ||
		key.ActiveSlot == nil ||
		*key.ActiveSlot != activeSigningKeySlot ||
		len(key.PublicKey) != ed25519.PublicKeySize ||
		len(key.PrivateKey) != ed25519.PrivateKeySize {
		return errInvalidSigningIdentity
	}
	privatePublic := ed25519.PrivateKey(key.PrivateKey).Public().(ed25519.PublicKey)
	if !bytes.Equal(privatePublic, key.PublicKey) {
		return errInvalidSigningIdentity
	}
	digest := sha256.Sum256(key.PublicKey)
	if key.KeyID != hex.EncodeToString(digest[:]) {
		return errInvalidSigningIdentity
	}
	return nil
}

func isNilSigningKeyStore(store SigningKeyStore) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
