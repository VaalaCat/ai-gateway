package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func newIssuer(t *testing.T) (signer jwk.Key, jwksHandler http.Handler) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err = jwk.FromRaw(priv)
	if err != nil {
		t.Fatal(err)
	}
	signer.Set(jwk.KeyIDKey, "k1")
	signer.Set(jwk.AlgorithmKey, jwa.RS256)

	pub, err := signer.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	pub.Set(jwk.KeyIDKey, "k1")
	pub.Set(jwk.AlgorithmKey, jwa.RS256)
	set := jwk.NewSet()
	set.AddKey(pub)
	jwksHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		bs, _ := json.Marshal(set)
		w.Write(bs)
	})
	return
}

func mintIDToken(t *testing.T, signer jwk.Key, iss, aud string, exp time.Time) string {
	t.Helper()
	tok, _ := jwt.NewBuilder().
		Issuer(iss).
		Audience([]string{aud}).
		IssuedAt(time.Now()).
		Expiration(exp).
		Subject("u-1").
		Build()
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, signer))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}

func TestVerifyIDToken_OK(t *testing.T) {
	signer, h := newIssuer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	idtok := mintIDToken(t, signer, "https://issuer.example", "cid", time.Now().Add(time.Minute))

	v := NewIDTokenVerifier()
	if err := v.Verify(context.Background(), idtok, srv.URL, "https://issuer.example", "cid"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyIDToken_BadAud(t *testing.T) {
	signer, h := newIssuer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	idtok := mintIDToken(t, signer, "https://issuer.example", "wrong-aud", time.Now().Add(time.Minute))

	v := NewIDTokenVerifier()
	if err := v.Verify(context.Background(), idtok, srv.URL, "https://issuer.example", "cid"); err == nil {
		t.Fatal("expected aud mismatch error")
	}
}

func TestVerifyIDToken_Expired(t *testing.T) {
	signer, h := newIssuer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	idtok := mintIDToken(t, signer, "https://issuer.example", "cid", time.Now().Add(-1*time.Second))

	v := NewIDTokenVerifier()
	if err := v.Verify(context.Background(), idtok, srv.URL, "https://issuer.example", "cid"); err == nil {
		t.Fatal("expected expired error")
	}
}
