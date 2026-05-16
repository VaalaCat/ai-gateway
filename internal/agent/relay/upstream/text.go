package upstream

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ExtractTextFromPassthroughBody extracts text content from a captured response body.
// For SSE streams, it scans data lines for content deltas.
// For non-stream JSON, it extracts the message content.
// Currently supports OpenAI Chat Completions format only. Responses API and
// other provider formats will result in empty text (estimation falls back to prompt-only).
func ExtractTextFromPassthroughBody(body []byte, isStream bool) string {
	if len(body) == 0 {
		return ""
	}

	if isStream {
		var sb strings.Builder
		lines := bytes.Split(body, []byte("\n"))
		for _, line := range lines {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(data) == 0 || string(data) == "[DONE]" {
				continue
			}
			var evt struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal(data, &evt) == nil {
				for _, c := range evt.Choices {
					if c.Delta.Content != "" {
						sb.WriteString(c.Delta.Content)
					}
				}
			}
		}
		return sb.String()
	}

	// Non-stream: extract from choices[0].message.content
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &resp) == nil && len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}
	return ""
}
