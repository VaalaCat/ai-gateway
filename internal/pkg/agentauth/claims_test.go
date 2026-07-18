package agentauth_test

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestTicketClaimsJSONContract(t *testing.T) {
	relayJSON, err := json.Marshal(agentauth.RelayClaims{
		AgentID:           "agent-a",
		MasterInstanceID:  "master-a",
		DesiredGeneration: 7,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{"agent-relay"},
		},
	})
	require.NoError(t, err)

	var relay map[string]any
	require.NoError(t, json.Unmarshal(relayJSON, &relay))
	require.Equal(t, "agent-a", relay["agent_id"])
	require.Equal(t, "master-a", relay["master_instance_id"])
	require.Equal(t, float64(7), relay["desired_generation"])
	require.NotContains(t, relay, "capability", "relay claims must not grow a forward-only capability")

	forwardJSON, err := json.Marshal(agentauth.ForwardClaims{
		SourceAgentID: "agent-source",
		Capability:    "agent_forward_ticket_v1",
	})
	require.NoError(t, err)
	var forward map[string]any
	require.NoError(t, json.Unmarshal(forwardJSON, &forward))
	require.Equal(t, "agent-source", forward["source_agent_id"])
	require.Equal(t, "agent_forward_ticket_v1", forward["capability"])

	welcomeJSON, err := json.Marshal(agentauth.WelcomeProofClaims{
		AgentID:           "agent-a",
		Nonce:             "nonce-a",
		MasterInstanceID:  "master-a",
		SessionGeneration: 8,
		DesiredGeneration: 9,
	})
	require.NoError(t, err)
	var welcome map[string]any
	require.NoError(t, json.Unmarshal(welcomeJSON, &welcome))
	require.Equal(t, "nonce-a", welcome["nonce"])
	require.Equal(t, float64(8), welcome["session_generation"])
	require.Equal(t, float64(9), welcome["desired_generation"])
}

func TestTicketAndPublicKeyWireTypes(t *testing.T) {
	relay := agentauth.RelayTicket("relay-token")
	forward := agentauth.ForwardTicket("forward-token")
	require.Equal(t, "relay-token", string(relay))
	require.Equal(t, "forward-token", string(forward))

	keyJSON, err := json.Marshal(agentauth.PublicKey{
		KeyID:     "key-a",
		Algorithm: "EdDSA",
		Key:       []byte{1, 2, 3},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"key_id":"key-a","algorithm":"EdDSA","key":"AQID"}`, string(keyJSON))
}
