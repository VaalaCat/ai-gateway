package upstream

import (
	"bytes"
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"go.uber.org/zap"
)

// NormalizeUsage ensures PromptTokens always represents non-cached input tokens.
// OpenAI includes cached tokens in prompt_tokens and reports them separately in
// prompt_tokens_details.cached_tokens (mapped to CachedTokens in IR). Claude
// already excludes cached tokens from input_tokens. This function detects the
// OpenAI case (CachedTokens > 0 but CacheReadTokens == 0) and adjusts accordingly.
func NormalizeUsage(u codec.Usage) codec.Usage {
	if u.CachedTokens > 0 && u.CacheReadTokens == 0 {
		u.CacheReadTokens = u.CachedTokens
		u.PromptTokens -= u.CachedTokens
		if u.PromptTokens < 0 {
			u.PromptTokens = 0
		}
	}
	return u
}

// isContentEvent returns true for event types that carry actual response
// content (text, tool calls, thinking). Control events like StreamStart,
// Usage, Done, and Error are excluded.
func isContentEvent(t codec.EventType) bool {
	switch t {
	case codec.EventContentDelta, codec.EventToolCallDelta, codec.EventThinkingDelta:
		return true
	default:
		return false
	}
}

// EmitDroppedToolsLog 检查 codec 写入的 dropped_tools metadata，若非空则输出一条
// warn 日志。与编码路径解耦，便于单元测试。
func EmitDroppedToolsLog(
	logger *zap.Logger,
	req *codec.Request,
	channelID uint,
	inbound, outbound codec.Protocol,
	policy string,
) {
	raw, ok := req.Metadata["dropped_tools"]
	if !ok {
		return
	}
	dropped, ok := raw.([]codec.DroppedTool)
	if !ok || len(dropped) == 0 {
		return
	}
	logger.Warn("codec dropped incompatible tools",
		zap.Uint("channel_id", channelID),
		zap.String("inbound", string(inbound)),
		zap.String("outbound", string(outbound)),
		zap.String("policy", policy),
		zap.Any("dropped", dropped),
	)
}

// ExtractUsageFromPassthroughBody extracts token usage from a passthrough response body.
// For SSE streams, it scans for the "response.completed" event containing usage.
// For non-stream JSON, it extracts usage from the top-level "usage" field.
// It also handles OpenAI Chat format ("prompt_tokens"/"completion_tokens") and
// Responses API format ("input_tokens"/"output_tokens").
func ExtractUsageFromPassthroughBody(body []byte, isStream bool) (prompt, completion, cacheRead, cacheWrite int) {
	if len(body) == 0 {
		return
	}

	if isStream {
		lines := bytes.Split(body, []byte("\n"))

		// Detect Claude SSE format by scanning for a "message_start" event
		for _, line := range lines {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			var peek struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &peek) == nil && peek.Type == "message_start" {
				return extractClaudeSSEUsage(lines)
			}
		}

		// OpenAI Chat / Responses API: scan backward for usage
		for i := len(lines) - 1; i >= 0; i-- {
			line := bytes.TrimSpace(lines[i])
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			var evt struct {
				Type     string `json:"type"`
				Response struct {
					Usage json.RawMessage `json:"usage"`
				} `json:"response"`
				Usage json.RawMessage `json:"usage"` // Chat format: usage at top level
			}
			if json.Unmarshal(data, &evt) != nil {
				continue
			}
			// Look for response.completed (Responses API) or [DONE] predecessor with usage
			usageData := evt.Response.Usage
			if len(usageData) == 0 {
				usageData = evt.Usage
			}
			if len(usageData) > 0 {
				prompt, completion, cacheRead, cacheWrite = ParseUsageJSON(usageData)
				if prompt > 0 || completion > 0 {
					return
				}
			}
		}
		return
	}

	// Non-stream: parse top-level usage
	var respObj struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &respObj) == nil && len(respObj.Usage) > 0 {
		prompt, completion, cacheRead, cacheWrite = ParseUsageJSON(respObj.Usage)
	}
	return
}

// extractClaudeSSEUsage extracts token usage from Claude SSE stream events.
// It reads input/cache tokens from message_start (message.usage) and
// output tokens from message_delta (top-level usage). If message_delta
// contains cumulative values (aligned with Anthropic SDK), those take priority.
func extractClaudeSSEUsage(lines [][]byte) (prompt, completion, cacheRead, cacheWrite int) {
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))

		var evt struct {
			Type    string `json:"type"`
			Message struct {
				Usage json.RawMessage `json:"usage"`
			} `json:"message"`
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(data, &evt) != nil {
			continue
		}

		switch evt.Type {
		case "message_start":
			if len(evt.Message.Usage) > 0 {
				prompt, completion, cacheRead, cacheWrite = ParseUsageJSON(evt.Message.Usage)
			}
		case "message_delta":
			if len(evt.Usage) > 0 {
				p, c, cr, cw := ParseUsageJSON(evt.Usage)
				if c > 0 {
					completion = c
				}
				// Use cumulative values from message_delta when available
				if p > 0 {
					prompt = p
				}
				if cr > 0 {
					cacheRead = cr
				}
				if cw > 0 {
					cacheWrite = cw
				}
			}
		}
	}
	return
}

// ParseUsageJSON parses usage from JSON, supporting OpenAI Chat format
// (prompt_tokens/completion_tokens), Responses API format (input_tokens/output_tokens),
// and Claude format (cache_read_input_tokens/cache_creation_input_tokens).
func ParseUsageJSON(data []byte) (prompt, completion, cacheRead, cacheWrite int) {
	var usage struct {
		// Responses API format
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		// Chat format
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		// Cache details (Responses API)
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		// Cache details (Chat format)
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		// Cache details (Claude format)
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	}
	if json.Unmarshal(data, &usage) != nil {
		return
	}

	prompt = usage.InputTokens
	if prompt == 0 {
		prompt = usage.PromptTokens
	}
	completion = usage.OutputTokens
	if completion == 0 {
		completion = usage.CompletionTokens
	}
	// OpenAI: cached tokens from prompt_tokens_details or input_tokens_details
	cacheRead = usage.InputTokensDetails.CachedTokens
	if cacheRead == 0 {
		cacheRead = usage.PromptTokensDetails.CachedTokens
	}
	// Claude: cache_read_input_tokens / cache_creation_input_tokens
	if cacheRead == 0 {
		cacheRead = usage.CacheReadInputTokens
	}
	cacheWrite = usage.CacheCreationInputTokens
	return
}
