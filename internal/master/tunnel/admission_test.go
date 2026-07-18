package tunnel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundAttemptAdmissionDefaultsClosedAndChangesAtomically(t *testing.T) {
	var gate AdmissionGate
	var nilGate *AdmissionGate
	require.False(t, nilGate.AllowNew())
	require.Equal(t, "relay_fallback_disabled", nilGate.RejectionCode())
	require.False(t, gate.AllowNew())
	gate.Set(true)
	require.True(t, gate.AllowNew())
	gate.Set(false)
	require.False(t, gate.AllowNew())
}

func TestBoundAttemptAdmissionRejectsEveryNewOpenWithStableCode(t *testing.T) {
	var gate AdmissionGate
	require.Equal(t, "relay_fallback_disabled", gate.RejectionCode())
	gate.Set(true)
	require.Empty(t, gate.RejectionCode())
	gate.Set(false)
	require.Equal(t, "relay_fallback_disabled", gate.RejectionCode())
}
