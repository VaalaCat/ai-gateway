package chat

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const toolCallSignaturePrefix = "call_llmkit_v1_"

// 签名随标准工具 ID 往返，客户端无需保存上游扩展字段；发回 Chat 上游时还原。
type toolCallIdentity struct {
	ID               string `json:"id"`
	Signature        string `json:"signature,omitempty"`
	MessageSignature string `json:"message_signature,omitempty"`
}

func encodeToolCallID(id, signature, messageSignature string) string {
	identity := decodeToolCallID(id)
	if identity.Signature != "" || identity.MessageSignature != "" {
		id = identity.ID
		if signature == "" {
			signature = identity.Signature
		}
		if messageSignature == "" {
			messageSignature = identity.MessageSignature
		}
	}
	if id == "" {
		id = "call_" + strings.TrimPrefix(generateID(), "chatcmpl-")
	}
	if signature == "" && messageSignature == "" {
		return id
	}
	encoded, _ := json.Marshal(toolCallIdentity{
		ID:               id,
		Signature:        signature,
		MessageSignature: messageSignature,
	})
	return toolCallSignaturePrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeToolCallID(id string) toolCallIdentity {
	if strings.HasPrefix(id, toolCallSignaturePrefix) {
		encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, toolCallSignaturePrefix))
		var identity toolCallIdentity
		if err == nil && json.Unmarshal(encoded, &identity) == nil && identity.ID != "" &&
			(identity.Signature != "" || identity.MessageSignature != "") {
			return identity
		}
	}
	return toolCallIdentity{ID: id}
}
