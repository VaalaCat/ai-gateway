package upstream

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// GeminiUsageObserver 从原生 JSON、JSON 数组和 SSE 中提取用量，不保存图片内容。
type GeminiUsageObserver struct {
	Stream          bool
	Usage           llmkit.Usage
	Err             error
	depth           int
	inString        bool
	escaped         bool
	text            []byte
	key             string
	fieldValue      bool
	rootArray       bool
	stringField     string
	sawCandidates   bool
	candidatesDepth int
	finished        bool
	capture         []byte
	captureDepth    int
	captureKey      string
	format          string
	linePrefix      [5]byte
	linePrefixLen   int
	dataLine        bool
	ignoredLine     bool
}

// Write 读取 JSON 字符串和容器边界，只保留用量或错误对象。
func (o *GeminiUsageObserver) Write(p []byte) (int, error) {
	// 1. 只从 JSON 正文或 SSE data 字段读取对象边界。
	for _, b := range p {
		if !o.isJSONByte(b) {
			continue
		}
		if o.captureDepth != 0 && len(o.capture) < 64*1024 {
			o.capture = append(o.capture, b)
		}
		if o.inString {
			if len(o.text) < 128 {
				o.text = append(o.text, b)
			}
			if o.escaped {
				o.escaped = false
				continue
			}
			if b == '\\' {
				o.escaped = true
				continue
			}
			if b == '"' {
				o.inString = false
				var value string
				if json.Unmarshal(o.text, &value) == nil {
					if o.stringField == "finishReason" && value != "" && o.candidatesDepth != 0 && o.depth == o.candidatesDepth+1 {
						o.finished = true
					}
					o.key = value
				} else {
					o.key = ""
				}
			}
			continue
		}
		switch b {
		case ':':
			o.fieldValue = true
		case '"':
			o.stringField = ""
			if o.fieldValue {
				o.stringField = o.key
			}
			o.fieldValue = false
			o.inString = true
			o.text = append(o.text[:0], b)
		case '{', '[':
			if o.depth == 0 {
				o.rootArray = b == '['
			}
			objectDepth := 1
			if o.rootArray {
				objectDepth = 2
			}
			o.depth++
			if b == '[' && o.fieldValue && o.key == "candidates" && o.depth == objectDepth+1 {
				o.candidatesDepth = o.depth
			}
			if b == '{' && o.candidatesDepth != 0 && o.depth == o.candidatesDepth+1 {
				o.sawCandidates = true
			}
			if b == '{' && o.fieldValue && o.depth == objectDepth+1 && o.captureDepth == 0 && (o.key == "usageMetadata" || o.key == "error" || o.key == "promptFeedback") {
				o.captureDepth, o.captureKey = o.depth, o.key
				o.capture = append(o.capture[:0], b)
			}
			o.fieldValue = false
			o.key = ""
		case '}', ']':
			if o.depth == o.candidatesDepth {
				o.candidatesDepth = 0
			}
			if o.captureDepth != 0 && o.depth == o.captureDepth {
				o.readCapture()
				o.captureDepth = 0
				o.capture = o.capture[:0]
			}
			o.depth--
			o.key = ""
		case ',':
			o.key = ""
		}
	}
	return len(p), nil
}

// isJSONByte 当前字节是否属于 JSON 正文或 SSE 的 data 字段？
func (o *GeminiUsageObserver) isJSONByte(b byte) bool {
	// 1. 首个非空白字符确定原生 JSON 与 SSE 格式。
	if o.format == "" {
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			return false
		}
		if b == '{' || b == '[' {
			o.format = "json"
		} else {
			o.format = "sse"
		}
	}
	if o.format == "json" {
		return true
	}
	// 2. 只读取 data: 后的字节，注释、event、id、retry 和未知字段全部跳过。
	if b == '\n' || b == '\r' {
		wasData := o.dataLine
		o.linePrefixLen, o.dataLine, o.ignoredLine = 0, false, false
		return wasData
	}
	if o.dataLine {
		return true
	}
	if o.ignoredLine {
		return false
	}
	o.linePrefix[o.linePrefixLen] = b
	o.linePrefixLen++
	if b != "data:"[o.linePrefixLen-1] {
		o.ignoredLine = true
		return false
	}
	if o.linePrefixLen == len(o.linePrefix) {
		o.dataLine = true
	}
	return false
}

// readCapture 更新最新累计用量，或记录上游明确返回的错误。
func (o *GeminiUsageObserver) readCapture() {
	// 1. 累计用量替换已有值，不逐帧相加。
	if o.captureKey == "usageMetadata" {
		var u struct {
			Prompt     int `json:"promptTokenCount"`
			Completion int `json:"candidatesTokenCount"`
			Thoughts   int `json:"thoughtsTokenCount"`
			Cache      int `json:"cachedContentTokenCount"`
			Total      int `json:"totalTokenCount"`
			Tools      int `json:"toolUsePromptTokenCount"`
		}
		if json.Unmarshal(o.capture, &u) == nil {
			o.Usage = llmkit.Usage{PromptTokens: max(0, u.Prompt+u.Tools-u.Cache), CompletionTokens: u.Completion + u.Thoughts, CacheReadTokens: u.Cache, ReasoningTokens: u.Thoughts, TotalTokens: u.Total}
		}
		return
	}
	// 2. 原始响应照常发送，错误仅用于请求结果和日志。
	var failure struct {
		Message     string `json:"message"`
		Code        int    `json:"code"`
		BlockReason string `json:"blockReason"`
	}
	if json.Unmarshal(o.capture, &failure) != nil {
		return
	}
	if o.captureKey == "error" && (failure.Code != 0 || failure.Message != "") {
		o.Err = fmt.Errorf("Gemini upstream error (%d): %s", failure.Code, failure.Message)
	}
	if failure.BlockReason != "" {
		o.Err = fmt.Errorf("Gemini prompt blocked: %s", failure.BlockReason)
	}
}

// Finish 返回上游错误或未结束的 JSON 响应错误。
func (o *GeminiUsageObserver) Finish() error {
	// 1. 优先返回上游声明的错误，再检查响应是否中途结束。
	if o.Err != nil {
		return o.Err
	}
	if o.inString || o.depth != 0 {
		return io.ErrUnexpectedEOF
	}
	if o.Stream && o.sawCandidates && !o.finished {
		return io.ErrUnexpectedEOF
	}
	return nil
}
