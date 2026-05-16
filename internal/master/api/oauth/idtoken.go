package oauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// IDTokenVerifier 校验 OIDC id_token 签名、过期、issuer 和 audience。
// 内部维护一个按 jwksURI 索引的 TTL 缓存，避免每次登录都请求 IdP。
type IDTokenVerifier struct {
	mu     sync.Mutex
	caches map[string]jwk.Set   // jwksURI → 公钥集合
	expiry map[string]time.Time // jwksURI → 缓存过期时间
	ttl    time.Duration
}

func NewIDTokenVerifier() *IDTokenVerifier {
	return &IDTokenVerifier{
		caches: make(map[string]jwk.Set),
		expiry: make(map[string]time.Time),
		ttl:    10 * time.Minute,
	}
}

// Verify 校验 id_token。
//   - jwksURI 必填：用于获取 IdP 公钥集合。
//   - issuer 非空时强制比对 iss claim。
//   - clientID 必须出现在 aud claim 中。
func (v *IDTokenVerifier) Verify(ctx context.Context, raw, jwksURI, issuer, clientID string) error {
	if jwksURI == "" {
		return errors.New("jwks_uri required for id_token verification")
	}
	set, err := v.fetchSet(ctx, jwksURI)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	opts := []jwt.ParseOption{
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
	}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	tok, err := jwt.ParseString(raw, opts...)
	if err != nil {
		return fmt.Errorf("parse id_token: %w", err)
	}
	auds := tok.Audience()
	for _, a := range auds {
		if a == clientID {
			return nil
		}
	}
	return fmt.Errorf("id_token audience does not include client_id %q", clientID)
}

func (v *IDTokenVerifier) fetchSet(ctx context.Context, jwksURI string) (jwk.Set, error) {
	v.mu.Lock()
	if exp, ok := v.expiry[jwksURI]; ok && time.Now().Before(exp) {
		set := v.caches[jwksURI]
		v.mu.Unlock()
		return set, nil
	}
	v.mu.Unlock()

	set, err := jwk.Fetch(ctx, jwksURI)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.caches[jwksURI] = set
	v.expiry[jwksURI] = time.Now().Add(v.ttl)
	v.mu.Unlock()
	return set, nil
}
