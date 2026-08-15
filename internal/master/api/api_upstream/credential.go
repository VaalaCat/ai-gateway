package api_upstream

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"golang.org/x/net/http/httpguts"
)

// APIUpstreamCredential is the plaintext-only credential input used at the
// administrative boundary. It is never embedded in a management response.
type APIUpstreamCredential = protocol.APIUpstreamCredential

var errInvalidAPIUpstreamCredential = errors.New("invalid upstream credential")

// EncryptAPIUpstreamCredential validates the credential's auth shape and
// encrypts its JSON representation with the persisted upstream ID as AEAD AAD.
func EncryptAPIUpstreamCredential(cipher *byokcrypto.Cipher, upstreamID uint, authType models.APIUpstreamAuthType, credential APIUpstreamCredential) (string, error) {
	if cipher == nil || upstreamID == 0 || validateAPIUpstreamCredential(authType, credential) != nil {
		return "", errInvalidAPIUpstreamCredential
	}
	if authType == models.APIUpstreamAuthNone {
		return "", nil
	}
	plaintext, err := json.Marshal(credential)
	if err != nil {
		return "", errInvalidAPIUpstreamCredential
	}
	sealed, err := cipher.Seal(string(plaintext), upstreamID)
	if err != nil {
		return "", errInvalidAPIUpstreamCredential
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptAPIUpstreamCredential opens a base64-encoded credential and validates
// its full shape again, so ciphertext cannot be reused for another auth type.
func DecryptAPIUpstreamCredential(cipher *byokcrypto.Cipher, upstreamID uint, authType models.APIUpstreamAuthType, ciphertext string) (APIUpstreamCredential, error) {
	if cipher == nil || upstreamID == 0 {
		return APIUpstreamCredential{}, errInvalidAPIUpstreamCredential
	}
	if authType == models.APIUpstreamAuthNone {
		if ciphertext == "" {
			return APIUpstreamCredential{}, nil
		}
		return APIUpstreamCredential{}, errInvalidAPIUpstreamCredential
	}
	encoded, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return APIUpstreamCredential{}, errInvalidAPIUpstreamCredential
	}
	plaintext, err := cipher.Open(encoded, upstreamID)
	if err != nil {
		return APIUpstreamCredential{}, errInvalidAPIUpstreamCredential
	}
	var credential APIUpstreamCredential
	if err := json.Unmarshal([]byte(plaintext), &credential); err != nil || validateAPIUpstreamCredential(authType, credential) != nil {
		return APIUpstreamCredential{}, errInvalidAPIUpstreamCredential
	}
	return credential, nil
}

func validateAPIUpstreamCredential(authType models.APIUpstreamAuthType, credential APIUpstreamCredential) error {
	empty := func() bool {
		return credential.BearerToken == "" && credential.HeaderName == "" && credential.HeaderValue == "" &&
			credential.QueryName == "" && credential.QueryValue == "" && credential.BasicUsername == "" && credential.BasicPassword == ""
	}
	switch authType {
	case models.APIUpstreamAuthNone:
		if empty() {
			return nil
		}
	case models.APIUpstreamAuthBearer:
		if credential.BearerToken != "" && credential.HeaderName == "" && credential.HeaderValue == "" && credential.QueryName == "" && credential.QueryValue == "" && credential.BasicUsername == "" && credential.BasicPassword == "" && httpguts.ValidHeaderFieldValue("Bearer "+credential.BearerToken) {
			return nil
		}
	case models.APIUpstreamAuthHeader:
		if credential.BearerToken == "" && credential.HeaderName != "" && credential.HeaderValue != "" && credential.QueryName == "" && credential.QueryValue == "" && credential.BasicUsername == "" && credential.BasicPassword == "" && validCredentialHeader(credential.HeaderName, credential.HeaderValue) {
			return nil
		}
	case models.APIUpstreamAuthQuery:
		if credential.BearerToken == "" && credential.HeaderName == "" && credential.HeaderValue == "" && credential.QueryName != "" && credential.QueryValue != "" && credential.BasicUsername == "" && credential.BasicPassword == "" && !strings.ContainsAny(credential.QueryName, "\x00\r\n") && !strings.ContainsAny(credential.QueryValue, "\x00\r\n") {
			return nil
		}
	case models.APIUpstreamAuthBasic:
		if credential.BearerToken == "" && credential.HeaderName == "" && credential.HeaderValue == "" && credential.QueryName == "" && credential.QueryValue == "" && credential.BasicUsername != "" && credential.BasicPassword != "" && httpguts.ValidHeaderFieldValue(credential.BasicUsername) && httpguts.ValidHeaderFieldValue(credential.BasicPassword) {
			return nil
		}
	}
	return errInvalidAPIUpstreamCredential
}

func validCredentialHeader(name, value string) bool {
	return !strings.EqualFold(name, "Host") && !unsafeAPIUpstreamAdminHeader(name) && httpguts.ValidHeaderFieldName(name) && httpguts.ValidHeaderFieldValue(value)
}

// APIUpstreamManagementResponse is the credential-safe response shape for
// administration APIs. Encrypted secret fields never exist on this type, and
// header overrides are included only after the handler verifies manage access.
type APIUpstreamManagementResponse struct {
	ID                   uint                       `json:"id"`
	BackendID            uint                       `json:"backend_id"`
	Name                 string                     `json:"name"`
	BaseURL              string                     `json:"base_url"`
	Weight               int                        `json:"weight"`
	Priority             int                        `json:"priority"`
	AuthType             models.APIUpstreamAuthType `json:"auth_type"`
	Status               int                        `json:"status"`
	CredentialConfigured bool                       `json:"credential_configured"`
	ProxyURLConfigured   bool                       `json:"proxy_url_configured"`
	HeaderOverride       map[string]string          `json:"header_override,omitempty"`
}

func NewAPIUpstreamManagementResponse(upstream models.APIUpstream, includeHeaderOverride ...bool) APIUpstreamManagementResponse {
	response := APIUpstreamManagementResponse{
		ID: upstream.ID, BackendID: upstream.BackendID, Name: upstream.Name, BaseURL: upstream.BaseURL,
		Weight: upstream.Weight, Priority: upstream.Priority, AuthType: upstream.AuthType, Status: upstream.Status,
		CredentialConfigured: upstream.CredentialCiphertext != "", ProxyURLConfigured: upstream.ProxyURLCiphertext != "",
	}
	if len(includeHeaderOverride) > 0 && includeHeaderOverride[0] {
		response.HeaderOverride = upstream.HeaderOverride.Data()
	}
	return response
}
