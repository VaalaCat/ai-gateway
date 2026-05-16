// Package claude implements the Anthropic Claude Messages API protocol codec.
package claude

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
)

func init() {
	codec.RegisterInbound(codec.ProtocolClaude, &ClaudeCodec{})
	codec.RegisterOutbound(codec.ProtocolClaude, &ClaudeCodec{})
}

// ClaudeCodec implements both codec.InboundCodec and codec.OutboundCodec for
// the Anthropic Claude Messages API protocol.
type ClaudeCodec struct{}

var _ codec.InboundCodec = (*ClaudeCodec)(nil)
var _ codec.OutboundCodec = (*ClaudeCodec)(nil)

// generateID produces a unique message ID in Claude's format.
func generateID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "msg_" + hex.EncodeToString(b)
}

// mapStopReason converts Claude stop_reason values to normalized finish reasons.
func mapStopReason(reason string) string {
	switch reason {
	case consts.ClaudeStopEndTurn:
		return consts.FinishReasonStop
	case consts.ClaudeStopMaxTokens:
		return consts.FinishReasonLength
	case consts.ClaudeStopToolUse:
		return consts.FinishReasonToolCalls
	case "stop_sequence":
		return consts.FinishReasonStop
	// C14: preserve pause_turn and refusal as distinct IR values — they pass
	// through to the default case and are kept as-is for Claude-native clients.
	// OpenAI encoders map them to "stop" on the outbound side.
	default:
		return reason
	}
}

// reverseMapStopReason converts normalized finish reasons back to Claude stop_reason values.
func reverseMapStopReason(reason string) string {
	switch reason {
	case consts.FinishReasonStop:
		return consts.ClaudeStopEndTurn
	case consts.FinishReasonLength:
		return consts.ClaudeStopMaxTokens
	case consts.FinishReasonToolCalls:
		return consts.ClaudeStopToolUse
	// C8: content_filter → end_turn (Claude doesn't have a content_filter stop reason)
	case consts.FinishReasonContentFilter:
		return consts.ClaudeStopEndTurn
	default:
		return reason
	}
}
