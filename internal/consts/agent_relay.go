package consts

const (
	RelayModeInherit  = "inherit"
	RelayModeCustom   = "custom"
	RelayModeDisabled = "disabled"

	SettingAgentRelayDefaultURI                         = "agent.relay_default_uri"
	SettingAgentConnectivityProbeSuccessTTLSeconds      = "agent.connectivity_probe_success_ttl_seconds"
	SettingAgentConnectivityProbeFailureRetryMinSeconds = "agent.connectivity_probe_failure_retry_min_seconds"
	SettingAgentConnectivityProbeFailureRetryMaxSeconds = "agent.connectivity_probe_failure_retry_max_seconds"

	RouteErrorSelectorInvalid            = "selector_invalid"
	RouteErrorTargetNotFound             = "target_not_found"
	RouteErrorTargetDisabled             = "target_disabled"
	RouteErrorTagNoCandidate             = "tag_no_candidate"
	RouteErrorDirectDisabled             = "direct_disabled"
	RouteErrorDirectIngressUnsupported   = "direct_ingress_unsupported"
	RouteErrorDirectCircuitOpen          = "direct_circuit_open"
	RouteErrorDirectAuthUnavailable      = "direct_auth_unavailable"
	RouteErrorDirectDNS                  = "direct_dns"
	RouteErrorDirectConnect              = "direct_connect"
	RouteErrorDirectTLS                  = "direct_tls"
	RouteErrorDirectIdentityMismatch     = "direct_identity_mismatch"
	RouteErrorDirectProbeInvalidResponse = "direct_probe_invalid_response"
	RouteErrorDirectProbeHTTPStatus      = "direct_probe_http_status"
	RouteErrorDirectProbeBodyTooLarge    = "direct_probe_body_too_large"
	RouteErrorDirectCommitUncertain      = "direct_commit_uncertain"
	RouteErrorDirectResponseInterrupted  = "direct_response_interrupted"
	RouteErrorRelayUnsupported           = "relay_unsupported"
	RouteErrorRelayNotConfigured         = "relay_not_configured"
	RouteErrorRelayDisabled              = "relay_disabled"
	RouteErrorRelayNotReady              = "relay_not_ready"
	RouteErrorRelayOverloaded            = "relay_overloaded"
	RouteErrorRelayProtocol              = "relay_protocol"
	RouteErrorRelayAuth                  = "relay_auth"
	RouteErrorRelayCommitUncertain       = "relay_commit_uncertain"
	RouteErrorRelayResponseInterrupted   = "relay_response_interrupted"
	RouteErrorRequestCancelled           = "request_cancelled"
	RouteErrorRequestDeadline            = "request_deadline"
	RouteErrorBodyTooLarge               = "body_too_large"
	RouteErrorBodyStoreFailed            = "body_store_failed"
	RouteErrorStreamWindowTimeout        = "stream_window_timeout"
	RouteErrorSessionClosed              = "session_closed"
	RouteErrorDrainTimeout               = "drain_timeout"
	RouteErrorRelayProbeHTTPStatus       = "relay_probe_http_status"
	RouteErrorRelayProbeBodyTooLarge     = "relay_probe_body_too_large"
	RouteErrorRelayProbeInvalidResponse  = "relay_probe_invalid_response"
	RouteErrorRelayProbeInvalidResult    = "relay_probe_invalid_result"
)

const (
	RouteErrorSourceDirectOutboundDisabled = "source_direct_outbound_disabled"
	RouteErrorTargetDirectInboundDisabled  = "target_direct_inbound_disabled"
	RouteErrorSourceRelayOutboundDisabled  = "source_relay_outbound_disabled"
	RouteErrorTargetRelayInboundDisabled   = "target_relay_inbound_disabled"
	RouteErrorRelayConnectionDisabled      = "relay_connection_disabled"
)

var publicRouteErrorCodes = [...]string{
	RouteErrorSelectorInvalid, RouteErrorTargetNotFound, RouteErrorTargetDisabled, RouteErrorTagNoCandidate,
	RouteErrorDirectDisabled, RouteErrorDirectIngressUnsupported, RouteErrorDirectCircuitOpen, RouteErrorDirectAuthUnavailable, RouteErrorDirectDNS,
	RouteErrorDirectConnect, RouteErrorDirectTLS, RouteErrorDirectIdentityMismatch, RouteErrorDirectProbeInvalidResponse,
	RouteErrorDirectCommitUncertain, RouteErrorDirectResponseInterrupted, RouteErrorRelayUnsupported,
	RouteErrorRelayNotConfigured, RouteErrorRelayDisabled, RouteErrorRelayNotReady,
	RouteErrorRelayOverloaded, RouteErrorRelayProtocol, RouteErrorRelayAuth, RouteErrorRelayCommitUncertain,
	RouteErrorRelayResponseInterrupted, RouteErrorRequestCancelled, RouteErrorRequestDeadline, RouteErrorBodyTooLarge,
	RouteErrorBodyStoreFailed, RouteErrorStreamWindowTimeout, RouteErrorSessionClosed, RouteErrorDrainTimeout,
	RouteErrorSourceDirectOutboundDisabled, RouteErrorTargetDirectInboundDisabled,
	RouteErrorSourceRelayOutboundDisabled, RouteErrorTargetRelayInboundDisabled, RouteErrorRelayConnectionDisabled,
}

var connectivityProbeOnlyErrorCodes = [...]string{
	RouteErrorRelayProbeHTTPStatus,
	RouteErrorRelayProbeBodyTooLarge,
	RouteErrorRelayProbeInvalidResponse,
	RouteErrorRelayProbeInvalidResult,
	RouteErrorDirectProbeHTTPStatus,
	RouteErrorDirectProbeBodyTooLarge,
}

func PublicRouteErrorCodes() []string {
	return append([]string(nil), publicRouteErrorCodes[:]...)
}

func IsPublicRouteErrorCode(code string) bool {
	for _, candidate := range publicRouteErrorCodes {
		if code == candidate {
			return true
		}
	}
	return false
}

func IsDirectedTransportPolicyErrorCode(code string) bool {
	switch code {
	case RouteErrorSourceDirectOutboundDisabled,
		RouteErrorTargetDirectInboundDisabled,
		RouteErrorSourceRelayOutboundDisabled,
		RouteErrorTargetRelayInboundDisabled:
		return true
	default:
		return false
	}
}

func IsConnectivityProbeErrorCode(code string) bool {
	if IsPublicRouteErrorCode(code) {
		return true
	}
	for _, candidate := range connectivityProbeOnlyErrorCodes {
		if code == candidate {
			return true
		}
	}
	return false
}
