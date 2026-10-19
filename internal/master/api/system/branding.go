package system

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Branding text is plain text, with limits measured in Unicode characters.
func validBrandingText(value string, maximum int, required bool) bool {
	return utf8.ValidString(value) && (!required || value != "") && utf8.RuneCountInString(value) <= maximum
}

// An icon can be hosted externally or on this site, but must never change origin
// through a protocol-relative path or use an executable/inline URL scheme.
func validSiteIconURL(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\\") || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) {
		return false
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || strings.ContainsRune(decoded, '\\') || strings.ContainsFunc(decoded, unicode.IsControl) {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Opaque != "" {
		return false
	}
	if strings.HasPrefix(value, "/") {
		return !strings.HasPrefix(decoded, "//") && u.Scheme == "" && u.Host == ""
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}
