package agentauth

import "github.com/golang-jwt/jwt/v5"

type RelayTicket string

type ForwardTicket string

type PublicKey struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Key       []byte `json:"key"`
}

type RelayClaims struct {
	AgentID           string `json:"agent_id"`
	MasterInstanceID  string `json:"master_instance_id"`
	DesiredGeneration uint64 `json:"desired_generation"`
	jwt.RegisteredClaims
}

type ForwardClaims struct {
	SourceAgentID string `json:"source_agent_id"`
	Capability    string `json:"capability"`
	jwt.RegisteredClaims
}

type WelcomeProofClaims struct {
	AgentID           string `json:"agent_id"`
	Nonce             string `json:"nonce"`
	MasterInstanceID  string `json:"master_instance_id"`
	SessionGeneration uint64 `json:"session_generation"`
	DesiredGeneration uint64 `json:"desired_generation"`
	jwt.RegisteredClaims
}
