package api_upstream

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestAPIUpstreamCredentialRoundTripAndNoResponseLeak(t *testing.T) {
	cipher, err := byokcrypto.NewFromConfig("", "credential-test-jwt-secret-at-least-32-bytes")
	require.NoError(t, err)

	tests := []struct {
		name       string
		authType   models.APIUpstreamAuthType
		credential APIUpstreamCredential
	}{
		{name: "bearer", authType: models.APIUpstreamAuthBearer, credential: APIUpstreamCredential{BearerToken: "bearer-secret"}},
		{name: "header", authType: models.APIUpstreamAuthHeader, credential: APIUpstreamCredential{HeaderName: "X-API-Key", HeaderValue: "header-secret"}},
		{name: "query", authType: models.APIUpstreamAuthQuery, credential: APIUpstreamCredential{QueryName: "key", QueryValue: "query-secret"}},
		{name: "basic", authType: models.APIUpstreamAuthBasic, credential: APIUpstreamCredential{BasicUsername: "user", BasicPassword: "basic-secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := EncryptAPIUpstreamCredential(cipher, 41, tt.authType, tt.credential)
			require.NoError(t, err)
			require.NotContains(t, ciphertext, "secret")
			got, err := DecryptAPIUpstreamCredential(cipher, 41, tt.authType, ciphertext)
			require.NoError(t, err)
			require.Equal(t, tt.credential, got)

			upstream := models.APIUpstream{ID: 41, BackendID: 7, Name: "primary", CredentialCiphertext: ciphertext, ProxyURLCiphertext: "proxy-ciphertext", HeaderOverride: datatypes.NewJSONType(map[string]string{"Authorization": "Bearer provider-secret"})}
			response := NewAPIUpstreamManagementResponse(upstream)
			body, err := json.Marshal(response)
			require.NoError(t, err)
			for _, leaked := range []string{"secret", ciphertext, "proxy-ciphertext", "credential_ciphertext", "proxy_url_ciphertext"} {
				require.NotContains(t, string(body), leaked)
			}
			require.Contains(t, string(body), "credential_configured")
			require.Contains(t, string(body), "proxy_url_configured")
			require.NotContains(t, string(body), "provider-secret")
			require.NotContains(t, string(body), "header_override")
			managed := NewAPIUpstreamManagementResponse(upstream, true)
			require.JSONEq(t, `{"Authorization":"Bearer provider-secret"}`, string(mustJSON(t, managed.HeaderOverride)))
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return body
}

func TestAPIUpstreamCredentialRejectsUnsafeInputsWithoutLeakingSecrets(t *testing.T) {
	cipher, err := byokcrypto.NewFromConfig("", "credential-test-jwt-secret-at-least-32-bytes")
	require.NoError(t, err)
	valid := APIUpstreamCredential{BearerToken: "bearer-secret"}
	ciphertext, err := EncryptAPIUpstreamCredential(cipher, 41, models.APIUpstreamAuthBearer, valid)
	require.NoError(t, err)
	malformedJSONCiphertext, err := cipher.Seal("{", 41)
	require.NoError(t, err)

	tests := []struct {
		name     string
		decrypt  bool
		cipher   *byokcrypto.Cipher
		id       uint
		authType models.APIUpstreamAuthType
		input    APIUpstreamCredential
		encoded  string
	}{
		{name: "nil cipher encrypt", cipher: nil, id: 41, authType: models.APIUpstreamAuthBearer, input: valid},
		{name: "zero upstream id encrypt", cipher: cipher, id: 0, authType: models.APIUpstreamAuthBearer, input: valid},
		{name: "auth type and field mismatch", cipher: cipher, id: 41, authType: models.APIUpstreamAuthHeader, input: valid},
		{name: "none rejects credential fields", cipher: cipher, id: 41, authType: models.APIUpstreamAuthNone, input: valid},
		{name: "nil cipher decrypt", decrypt: true, cipher: nil, id: 41, authType: models.APIUpstreamAuthBearer, encoded: ciphertext},
		{name: "zero upstream id decrypt", decrypt: true, cipher: cipher, id: 0, authType: models.APIUpstreamAuthBearer, encoded: ciphertext},
		{name: "wrong aad", decrypt: true, cipher: cipher, id: 42, authType: models.APIUpstreamAuthBearer, encoded: ciphertext},
		{name: "malformed base64", decrypt: true, cipher: cipher, id: 41, authType: models.APIUpstreamAuthBearer, encoded: "%%%not-base64%%%"},
		{name: "malformed json", decrypt: true, cipher: cipher, id: 41, authType: models.APIUpstreamAuthBearer, encoded: base64.StdEncoding.EncodeToString(malformedJSONCiphertext)},
		{name: "auth type mismatch after decrypt", decrypt: true, cipher: cipher, id: 41, authType: models.APIUpstreamAuthHeader, encoded: ciphertext},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.decrypt {
				_, err = DecryptAPIUpstreamCredential(tt.cipher, tt.id, tt.authType, tt.encoded)
			} else {
				_, err = EncryptAPIUpstreamCredential(tt.cipher, tt.id, tt.authType, tt.input)
			}
			require.Error(t, err)
			for _, forbidden := range []string{"bearer-secret", ciphertext, "%%%not-base64%%%", "byokcrypto", "authentication", "ciphertext", "AAD"} {
				require.NotContains(t, strings.ToLower(err.Error()), strings.ToLower(forbidden))
			}
		})
	}
}
