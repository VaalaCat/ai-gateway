package agentproxy

import (
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

const (
	CodeDirectDNS                  = "direct_dns"
	CodeDirectConnect              = "direct_connect"
	CodeDirectTLS                  = "direct_tls"
	CodeDirectRoundTrip            = "direct_round_trip"
	CodeDirectCircuitOpen          = "direct_circuit_open"
	CodeDirectCircuitHalfOpen      = "direct_circuit_half_open"
	CodeDirectCircuitCapacity      = "direct_circuit_capacity"
	CodeDirectInvalidInput         = "direct_invalid_input"
	CodeDirectBody                 = "direct_body"
	CodeDirectResponseCopy         = "direct_response_copy"
	CodeDirectClosed               = "direct_closed"
	CodeDirectDisabled             = "direct_disabled"
	CodeDirectIngressUnsupported   = "direct_ingress_unsupported"
	CodeDirectAuthUnavailable      = "direct_auth_unavailable"
	CodeDirectIdentityMismatch     = "direct_identity_mismatch"
	CodeDirectProbeInvalidResponse = "direct_probe_invalid_response"
	CodeDirectCommitUncertain      = "direct_commit_uncertain"
	CodeDirectResponseInterrupted  = "direct_response_interrupted"
	CodeRelayFallbackDisabled      = tunnel.ErrorCodeRelayFallbackDisabled
	CodeRelayNotReady              = "relay_not_ready"
	CodeRelayCommitUncertain       = "relay_commit_uncertain"
	CodeRelayResponseInterrupted   = "relay_response_interrupted"
	CodeRequestCancelled           = "request_cancelled"
	CodeRequestDeadline            = "request_deadline"
)

// BeforeWriteError is emitted only by dial and TLS stages that complete
// before http.Transport receives a connection capable of writing the request.
type BeforeWriteError struct {
	Stage string
	Code  string
	Err   error
}

func (e *BeforeWriteError) Error() string {
	if e == nil {
		return "direct before-write failure"
	}
	return fmt.Sprintf("direct %s failed: %v", e.Stage, e.Err)
}

func (e *BeforeWriteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func classifyRoundTripFailure(err error) DirectOutcome {
	var beforeWrite *BeforeWriteError
	if errors.As(err, &beforeWrite) {
		return DirectOutcome{
			Commit: tunnel.PreCommit,
			Stage:  beforeWrite.Stage,
			Code:   beforeWrite.Code,
			Err:    err,
		}
	}
	return DirectOutcome{
		Commit: tunnel.CommitUncertain,
		Stage:  "round_trip",
		Code:   CodeDirectRoundTrip,
		Err:    err,
	}
}

func classifyHTTPResponse(_ int) DirectOutcome {
	return DirectOutcome{Commit: tunnel.Committed, Stage: "response"}
}
