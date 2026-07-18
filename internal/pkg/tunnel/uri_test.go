package tunnel

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRelayURIAcceptsAndSanitizesWebSocketURIs(t *testing.T) {
	t.Parallel()

	parsed, err := ParseRelayURI("  WSS://relay.example/tunnel/v1?token=secret&token=&flag  ")
	require.NoError(t, err)
	require.Equal(t, "wss", strings.ToLower(parsed.URI.Scheme))
	require.Equal(t, "relay.example", parsed.URI.Hostname())
	require.Equal(t, "/tunnel/v1", parsed.URI.Path)
	require.Equal(t, "wss://relay.example/tunnel/v1?token=secret&token=&flag", parsed.URI.String())
	require.Equal(t, "wss://relay.example/tunnel/v1?flag=REDACTED&token=REDACTED&token=REDACTED", parsed.Sanitized)
	require.NotContains(t, parsed.Sanitized, "secret")
}

func TestParseRelayURITrimsExactASCIIEdgeWhitespace(t *testing.T) {
	t.Parallel()

	parsed, err := ParseRelayURI("\t\n\v\f\r wss://relay.example/tunnel \t\n\v\f\r")
	require.NoError(t, err)
	require.Equal(t, "wss://relay.example/tunnel", parsed.URI.String())
}

func TestParseRelayURIRejectsUnicodeEdgeWhitespace(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"\u00a0wss://relay.example/tunnel",
		"wss://relay.example/tunnel\u00a0",
		"\u00a0wss://relay.example/tunnel\u00a0",
	} {
		_, err := ParseRelayURI(raw)
		require.EqualError(t, err, "invalid relay URI")
	}
}

func TestParseRelayURIRejectsRawASCIIControlCharacters(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"leading NUL":  "\x00wss://relay.example/tunnel",
		"trailing NUL": "wss://relay.example/tunnel\x00",
		"internal tab": "wss://relay.example/tunnel\tsegment",
		"internal LF":  "wss://relay.example/tunnel\nsegment",
		"internal VT":  "wss://relay.example/tunnel\vsegment",
		"internal FF":  "wss://relay.example/tunnel\fsegment",
		"internal CR":  "wss://relay.example/tunnel\rsegment",
		"internal DEL": "wss://relay.example/tunnel\x7fsegment",
	}
	for name, raw := range tests {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRelayURI(raw)
			require.EqualError(t, err, "invalid relay URI")
		})
	}
}

func TestParseRelayURIAcceptsPercentEncodedNUL(t *testing.T) {
	t.Parallel()

	parsed, err := ParseRelayURI("wss://relay.example/tunnel%00segment")
	require.NoError(t, err)
	require.Equal(t, "wss://relay.example/tunnel%00segment", parsed.URI.String())
}

func TestParseRelayURIValidatesExplicitPortRange(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw   string
		valid bool
	}{
		"zero":             {raw: "wss://relay.example:0/tunnel"},
		"maximum":          {raw: "wss://relay.example:65535/tunnel", valid: true},
		"above maximum":    {raw: "wss://relay.example:65536/tunnel"},
		"large":            {raw: "wss://relay.example:99999/tunnel"},
		"non-numeric":      {raw: "wss://relay.example:relay/tunnel"},
		"IPv6 zero":        {raw: "wss://[2001:db8::1]:0/tunnel"},
		"IPv6 maximum":     {raw: "wss://[2001:db8::1]:65535/tunnel", valid: true},
		"IPv6 above range": {raw: "wss://[2001:db8::1]:65536/tunnel"},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parsed, err := ParseRelayURI(test.raw)
			if !test.valid {
				require.EqualError(t, err, "invalid relay URI")
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.raw, parsed.URI.String())
		})
	}
}

func TestParseRelayURIAcceptsCanonicalStorageByteBoundary(t *testing.T) {
	t.Parallel()

	const prefix = "wss://relay.example/"
	tests := map[string]struct {
		raw       string
		canonical string
	}{
		"ASCII": {
			raw:       prefix + strings.Repeat("a", maxRelayURIBytes-len(prefix)),
			canonical: prefix + strings.Repeat("a", maxRelayURIBytes-len(prefix)),
		},
		"Unicode": {
			raw:       prefix + strings.Repeat("a", maxRelayURIBytes-len(prefix)-len("%E7%95%8C")) + "界",
			canonical: prefix + strings.Repeat("a", maxRelayURIBytes-len(prefix)-len("%E7%95%8C")) + "%E7%95%8C",
		},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.LessOrEqual(t, len(tt.raw), maxRelayURIBytes)
			require.Len(t, tt.canonical, maxRelayURIBytes)

			parsed, err := ParseRelayURI(tt.raw)
			require.NoError(t, err)
			require.Equal(t, tt.canonical, parsed.URI.String())
		})
	}
}

func TestParseRelayURIRejectsCanonicalValueBeyondStorageByteLimit(t *testing.T) {
	t.Parallel()

	const prefix = "wss://relay.example/"
	raw := prefix + strings.Repeat("a", maxRelayURIBytes-len(prefix)-len("界")) + "界"
	require.Len(t, raw, maxRelayURIBytes)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.Greater(t, len(parsed.String()), maxRelayURIBytes)

	_, err = ParseRelayURI(raw)
	require.EqualError(t, err, "invalid relay URI")
}

func TestParseRelayURIAcceptsPercentEncodedHash(t *testing.T) {
	t.Parallel()

	parsed, err := ParseRelayURI("wss://relay.example/path%23segment?marker=%23")
	require.NoError(t, err)
	require.Equal(t, "wss://relay.example/path%23segment?marker=%23", parsed.URI.String())
	require.Equal(t, "wss://relay.example/path%23segment?marker=REDACTED", parsed.Sanitized)
}

func TestParseRelayURIRejectsInvalidInputsWithoutLeakingRawURI(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-value"
	tests := map[string]string{
		"empty":                    "",
		"relative":                 "/tunnel?token=" + secret,
		"http":                     "http://relay.example/?token=" + secret,
		"ftp":                      "ftp://relay.example/?token=" + secret,
		"missing hostname":         "ws:///tunnel?token=" + secret,
		"opaque URI":               "ws:relay.example?token=" + secret,
		"userinfo":                 "wss://user:pass@relay.example/?token=" + secret,
		"empty userinfo marker":    "wss://@relay.example/?token=" + secret,
		"fragment":                 "wss://relay.example/tunnel?token=" + secret + "#fragment",
		"empty fragment":           "wss://relay.example/tunnel#",
		"empty query fragment":     "wss://relay.example/tunnel?token=value#",
		"empty query and fragment": "wss://relay.example/tunnel?#",
		"malformed query":          "wss://relay.example/tunnel?token=" + secret + "&bad=%zz",
		"too many bytes":           "wss://relay.example/" + strings.Repeat("a", 2049-len("wss://relay.example/")),
		"too many unicode bytes":   "wss://relay.example/" + strings.Repeat("a", 2049-len("wss://relay.example/")-len("界")) + "界",
	}

	for name, raw := range tests {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRelayURI(raw)
			require.Error(t, err)
			require.NotContains(t, err.Error(), secret)
			if raw != "" {
				require.NotContains(t, err.Error(), raw)
			}
		})
	}
}
