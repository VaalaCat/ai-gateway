package agentproxy

type DirectPathDisabledReason string

const (
	DirectPathDisabledSourceOutbound DirectPathDisabledReason = "source_direct_outbound_disabled"
	DirectPathDisabledTargetInbound  DirectPathDisabledReason = "target_direct_inbound_disabled"
)

type DirectPathDisabledEvent struct {
	SourceAgentID string
	TargetAgentID string
	Reason        DirectPathDisabledReason
}

type DirectPathDisabledRecorder interface {
	RecordDirectPathDisabled(DirectPathDisabledEvent)
}
