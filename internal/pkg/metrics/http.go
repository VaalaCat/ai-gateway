package metrics

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewAuthenticatedHandler exposes gatherer metrics to requests carrying token.
func NewAuthenticatedHandler(gatherer prometheus.Gatherer, token string) http.Handler {
	metricsHandler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !hasValidBearerToken(request.Header.Values("Authorization"), token) {
			writeUnauthorized(response)
			return
		}
		metricsHandler.ServeHTTP(response, request)
	})
}

func hasValidBearerToken(values []string, token string) bool {
	if len(values) != 1 {
		return false
	}

	authorization := values[0]
	separator := strings.IndexByte(authorization, ' ')
	if separator <= 0 || !strings.EqualFold(authorization[:separator], "Bearer") {
		return false
	}

	credentialStart := separator
	for credentialStart < len(authorization) && authorization[credentialStart] == ' ' {
		credentialStart++
	}
	credentials := authorization[credentialStart:]
	if credentials == "" || strings.IndexFunc(credentials, unicode.IsSpace) >= 0 {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(credentials), []byte(token)) == 1
}

func writeUnauthorized(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", "Bearer")
	response.Header().Set("Cache-Control", "no-store")
	http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}
