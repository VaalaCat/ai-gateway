package llmkit

import "github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"

// ToolSignatureFallback 是一轮工具调用没有可用签名时补什么。
type ToolSignatureFallback = convert.ToolSignatureFallback

const (
	ToolSignatureFallbackNone   = convert.ToolSignatureFallbackNone
	ToolSignatureFallbackGemini = convert.ToolSignatureFallbackGemini
)
