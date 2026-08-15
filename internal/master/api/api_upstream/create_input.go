package api_upstream

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"golang.org/x/net/http/httpguts"
	"gorm.io/datatypes"
)

// BuildAPIUpstreamForCreate normalizes and validates a create input into the
// database model that Creator may persist. It performs no encryption or I/O.
func BuildAPIUpstreamForCreate(backendID uint, input CreateInput) (models.APIUpstream, error) {
	status := consts.StatusEnabled
	if input.Status != nil {
		status = *input.Status
	}
	headers, err := normalizeAPIUpstreamHeaderOverrides(input.HeaderOverride)
	if err != nil {
		return models.APIUpstream{}, err
	}
	row := models.APIUpstream{
		BackendID: backendID, Name: input.Name, BaseURL: input.BaseURL, Weight: input.Weight,
		Priority: input.Priority, AuthType: input.AuthType, Status: status,
		HeaderOverride: datatypes.NewJSONType(headers),
	}
	if row.Weight == 0 {
		row.Weight = 1
	}
	if row.AuthType == "" {
		row.AuthType = models.APIUpstreamAuthNone
	}
	if err := validateCredentialTransition(models.APIUpstreamAuthNone, row.AuthType, input.Credential, true); err != nil {
		return models.APIUpstream{}, err
	}
	if err := validateAPIUpstreamProxyURL(input.ProxyURL); err != nil {
		return models.APIUpstream{}, err
	}
	if err := row.Validate(); err != nil {
		return models.APIUpstream{}, err
	}
	return row, nil
}

func normalizeAPIUpstreamHeaderOverrides(input map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(input))
	for name, value := range input {
		if !validAPIUpstreamHeaderOverride(name, value) {
			return nil, fmt.Errorf("invalid upstream header override")
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := result[canonical]; exists {
			return nil, fmt.Errorf("duplicate upstream header override")
		}
		result[canonical] = value
	}
	return result, nil
}

func validAPIUpstreamHeaderOverride(name, value string) bool {
	if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
		return false
	}
	if strings.EqualFold(name, "Host") {
		return httpguts.ValidHostHeader(value)
	}
	return !unsafeAPIUpstreamAdminHeader(name)
}

func unsafeAPIUpstreamAdminHeader(name string) bool {
	lower := strings.ToLower(name)
	if _, unsafe := apiUpstreamHopByHopHeaders[lower]; unsafe {
		return true
	}
	return lower == "forwarded" || strings.HasPrefix(lower, "x-vaala-") || strings.HasPrefix(lower, "x-forwarded-")
}

var apiUpstreamHopByHopHeaders = map[string]struct{}{
	"connection": {}, "keep-alive": {}, "proxy-authenticate": {}, "proxy-authorization": {},
	"proxy-connection": {}, "te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {},
}

func validateAPIUpstreamProxyURL(raw *string) error {
	if raw == nil || *raw == "" {
		return nil
	}
	parsed, err := url.Parse(*raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Fragment != "" {
		return fmt.Errorf("invalid upstream proxy URL")
	}
	return nil
}
