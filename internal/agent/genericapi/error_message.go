package genericapi

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const redactedAPIErrorMessage = "redacted"

func safeAPIErrorMessage(err error, credential protocol.APIUpstreamCredential) (message string) {
	if err == nil {
		return ""
	}

	message = redactedAPIErrorMessage
	defer func() {
		if recover() != nil {
			message = redactedAPIErrorMessage
		}
	}()

	message = errorTextWithoutURL(err)
	message = redactAPIKnownCredentials(message, credential)
	message = normalizeAPIErrorText(message)
	if message == "" {
		return redactedAPIErrorMessage
	}
	return truncateAPIErrorText(message, apiattempt.MaxAPIErrorMessageBytes)
}

func errorTextWithoutURL(err error) string {
	if err == nil {
		return ""
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return strings.Join(nonEmptyStrings(urlErr.Op, errorTextWithoutURL(urlErr.Err)), ": ")
	}
	return err.Error()
}

func redactAPIKnownCredentials(message string, credential protocol.APIUpstreamCredential) string {
	for _, candidate := range apiCredentialCandidates(credential) {
		message = strings.ReplaceAll(message, candidate, "[REDACTED]")
	}
	return message
}

// behavior change: credential redaction is intentionally best effort.
func apiCredentialCandidates(credential protocol.APIUpstreamCredential) []string {
	candidates := nonEmptyStrings(
		credential.BearerToken,
		credential.HeaderValue,
		credential.QueryValue,
		credential.BasicPassword,
	)
	if credential.BearerToken != "" {
		candidates = append(candidates, "Bearer "+credential.BearerToken)
	}
	if credential.BasicUsername != "" || credential.BasicPassword != "" {
		basicPair := credential.BasicUsername + ":" + credential.BasicPassword
		candidates = append(candidates, basicPair, base64.StdEncoding.EncodeToString([]byte(basicPair)))
	}
	return candidates
}

func nonEmptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func normalizeAPIErrorText(message string) string {
	message = strings.ToValidUTF8(message, "�")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
}

func truncateAPIErrorText(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}

	bytes := 0
	for index, r := range message {
		runeBytes := utf8.RuneLen(r)
		if bytes+runeBytes > maxBytes {
			return message[:index]
		}
		bytes += runeBytes
	}
	return message
}
