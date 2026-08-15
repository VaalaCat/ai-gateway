package agentproxy

import (
	"context"
	"net/url"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// DirectSessionTarget is the frozen address of a direct peer. Callers resolve
// and freeze the address once; the pool never re-selects an address internally.
// WebSocketURL and ProxyURL are already validated by the resolver. The pool
// treats them as opaque, only ever emitting the sanitized endpoint in errors.
type DirectSessionTarget struct {
	TargetAgentID      string
	AddressFingerprint string
	WebSocketURL       *url.URL
	ProxyURL           *url.URL
}

// DirectAttemptStreamOpener opens an attempt stream to a frozen direct target.
type DirectAttemptStreamOpener interface {
	OpenAttemptStream(
		context.Context,
		DirectSessionTarget,
		app.AttemptStreamRequest,
	) (app.AttemptStream, error)
}

// DirectHTTPAPIStreamOpener opens a Generic API stream to a frozen direct target.
type DirectHTTPAPIStreamOpener interface {
	OpenHTTPAPIStream(
		context.Context,
		DirectSessionTarget,
		app.APIOpen,
	) (app.HTTPAPIStream, error)
}

// DirectWebSocketAPIStreamOpener opens one Generic API WebSocket stream to a
// frozen direct target. Implementations must not retry or choose another path.
type DirectWebSocketAPIStreamOpener interface {
	OpenWebSocketAPIStream(
		context.Context,
		DirectSessionTarget,
		app.WebSocketOpen,
	) (app.WebSocketAPIStream, error)
}

// DirectTransportIdentity is an opaque fingerprint of the peer address,
// effective proxy, and credential generation. It is safe to use as a circuit
// key because it never contains their raw values.
type DirectTransportIdentity [32]byte

// DirectAttemptTransport freezes the pool inputs used by one Direct attempt.
// Building it performs local validation only. AcquireAttemptStream owns session
// admission and dialing; the returned reservation opens the attempt stream.
type DirectAttemptTransport interface {
	TransportIdentity() DirectTransportIdentity
	AcquireAttemptStream(context.Context) (DirectAttemptStreamReservation, error)
}

// DirectAttemptStreamReservation owns one acquired session admission. Its
// actual transport may differ from the built transport when a replacement
// falls back to a still-healthy prior session. Release is idempotent.
type DirectAttemptStreamReservation interface {
	TransportIdentity() DirectTransportIdentity
	AddressFingerprint() string
	OpenAttemptStream(context.Context, app.AttemptStreamRequest) (app.AttemptStream, error)
	Release()
}

// DirectHTTPAPIStreamReservation owns one acquired session admission for a
// Generic API stream. Release is idempotent.
type DirectHTTPAPIStreamReservation interface {
	TransportIdentity() DirectTransportIdentity
	AddressFingerprint() string
	OpenHTTPAPIStream(context.Context, app.APIOpen) (app.HTTPAPIStream, error)
	Release()
}

// DirectHTTPAPITransport freezes the pool inputs used by one Generic API stream.
type DirectHTTPAPITransport interface {
	TransportIdentity() DirectTransportIdentity
	AcquireHTTPAPIStream(context.Context) (DirectHTTPAPIStreamReservation, error)
}

type DirectHTTPAPITransportBuilder interface {
	BuildDirectHTTPAPITransport(context.Context, DirectSessionTarget) (DirectHTTPAPITransport, error)
}

// DirectAttemptTransportBuilder builds a frozen transport for a Direct target.
type DirectAttemptTransportBuilder interface {
	BuildDirectAttemptTransport(context.Context, DirectSessionTarget) (DirectAttemptTransport, error)
}

// DirectProbeStreamOpener opens a connectivity probe stream to a frozen direct target.
type DirectProbeStreamOpener interface {
	OpenProbeStream(
		context.Context,
		DirectSessionTarget,
		app.ProbeStreamRequest,
	) (app.ProbeStream, error)
}
