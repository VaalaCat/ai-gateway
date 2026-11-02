package protocolconfig

import (
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"strings"
)

// These protocols are forwarded without a chat codec or protocol conversion.
const (
	ProtocolImageEdits      llmkit.Protocol = "openai_image_edits"
	ProtocolImageVariations llmkit.Protocol = "openai_image_variations"
	ProtocolVideos          llmkit.Protocol = "openai_videos"
	ProtocolDecisions       llmkit.Protocol = "openai_decisions"
)

func IsProviderAPI(protocol llmkit.Protocol) bool {
	switch protocol {
	case ProtocolImageEdits, ProtocolImageVariations, ProtocolVideos, ProtocolDecisions:
		return true
	default:
		return false
	}
}

func providerAPIProtocol(path string) llmkit.Protocol {
	switch path {
	case "/v1/images/edits":
		return ProtocolImageEdits
	case "/v1/images/variations":
		return ProtocolImageVariations
	case "/v1/decisions":
		return ProtocolDecisions
	case "/v1/videos":
		return ProtocolVideos
	}
	if strings.HasPrefix(path, "/v1/videos/") {
		return ProtocolVideos
	}
	return llmkit.ProtocolUnknown
}
