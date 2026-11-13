package gemini

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type responseDecoder struct {
	ctx              context.Context
	out              chan ir.Event
	model            string
	started          bool
	finish           string
	partIndex        int
	textOffset       int
	calls            []pendingCall
	reasoning        bool
	reasoningSummary strings.Builder
	lastCall         int
	image            *ir.Event
	buffering        bool
	pending          []pendingEvent
}

// DecodeResponse 逐帧读取 Gemini，输出内容与用量事件。
func (*handler) DecodeResponse(ctx context.Context, input protocol.DecodeResponseInput, state any) (<-chan ir.Event, error) {
	// 1. 给读取器绑定取消和关闭，并创建单请求解码状态。
	resp := protocol.DecodeHTTPResponse(ctx, input)
	config, _ := state.(conversionState)
	d := &responseDecoder{ctx: ctx, out: make(chan ir.Event, 16), model: config.model, lastCall: -1}
	go func() {
		defer close(d.out)
		defer resp.Body.Close()
		// 2. JSON 和 SSE 共用帧处理，持续读取 finish 后的用量。
		var err error
		if input.Stream {
			err = d.readSSE(resp.Body)
		} else {
			var raw json.RawMessage
			err = json.NewDecoder(resp.Body).Decode(&raw)
			if err == nil {
				err = d.frame(raw)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			d.finishReasoning(ir.ReasoningInterrupted)
			d.buffering = false
			d.emit(ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Code: "gemini_response_error", Message: err.Error()}})
			return
		}
		if input.Stream && d.finish == "" {
			d.finishReasoning(ir.ReasoningInterrupted)
			d.buffering = false
			d.emit(ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Code: "unexpected_eof", Message: "Gemini stream ended before finishReason"}})
			return
		}
		// 3. 签名收齐后输出工具事件，再结束整份响应。
		d.flushImage()
		d.finishReasoning(ir.ReasoningCompleted)
		d.flushCalls(true)
		reason := finishReason(d.finish)
		if len(d.calls) > 0 && reason == "stop" {
			reason = "tool_calls"
		}
		d.emit(ir.Event{Type: ir.EventDone, FinishReason: reason})
	}()
	return convert.AdaptNamespacedFunctionEvents(ctx, d.out, config.namespacedFunctions), nil
}

// readSSE 以空行分隔帧，不用 Scanner 的小行缓冲限制图片。
func (d *responseDecoder) readSSE(body io.Reader) error {
	// 1. 保留多行 data，读到 EOF 时处理最后一帧。
	reader := bufio.NewReader(body)
	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			data.WriteString(value)
		}
		if line == "" || err != nil {
			if data.Len() > 0 {
				raw := data.String()
				data.Reset()
				if strings.TrimSpace(raw) == "[DONE]" {
					return nil
				}
				if frameErr := d.frame([]byte(raw)); frameErr != nil {
					return frameErr
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if d.ctx.Err() != nil {
			return d.ctx.Err()
		}
	}
}

// frame 转换一个 Gemini 响应对象。
func (d *responseDecoder) frame(raw []byte) error {
	// 1. 读取上游明确错误与单候选内容。
	var response response
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode Gemini response: %w", err)
	}
	if response.Error != nil {
		return fmt.Errorf("Gemini %s (%d): %s", response.Error.Status, response.Error.Code, response.Error.Message)
	}
	if response.PromptFeedback.BlockReason != "" {
		return fmt.Errorf("Gemini prompt blocked: %s", response.PromptFeedback.BlockReason)
	}
	if len(response.Candidates) > 1 {
		return fmt.Errorf("unsupported_response_content: Gemini returned multiple candidates")
	}
	if !d.started {
		d.started = true
		d.emit(ir.Event{Type: ir.EventStreamStart, Model: response.ModelVersion})
	}
	for _, candidate := range response.Candidates {
		if candidate.Index != 0 {
			return fmt.Errorf("unsupported_response_content: Gemini candidate index %d", candidate.Index)
		}
		if candidate.FinishReason != "" {
			d.finish = candidate.FinishReason
		}
		for _, p := range candidate.Content.Parts {
			signatureOnly := len(p.ThoughtSignature) > 0 && p.Text == nil && p.InlineData == nil && p.FileData == nil && p.FunctionCall == nil && len(p.ExecutableCode) == 0 && len(p.CodeExecutionResult) == 0
			if !signatureOnly {
				if !p.Thought {
					d.finishReasoning(ir.ReasoningCompleted)
				}
				d.flushImage()
				d.lastCall = -1
			}
			index := d.partIndex
			d.partIndex++
			pdata := d.providerData(p.ThoughtSignature, index)
			switch {
			case p.FunctionCall != nil:
				call := p.FunctionCall
				existing := -1
				if call.ID != "" {
					for i := range d.calls {
						if d.calls[i].Call.CallID == call.ID {
							existing = i
							break
						}
					}
				}
				if existing >= 0 {
					d.lastCall = existing
					current := &d.calls[existing].Call
					if call.Name != "" {
						current.Name = call.Name
					}
					if len(call.Args) > 0 {
						current.Arguments = string(call.Args)
					}
					if len(p.ThoughtSignature) > 0 {
						current.ProviderData.Fields["thoughtSignature"] = p.ThoughtSignature
					}
				} else {
					id := call.ID
					if id == "" {
						id = callID()
					}
					args := string(call.Args)
					if args == "" {
						args = "{}"
					}
					d.calls = append(d.calls, pendingCall{Call: ir.StreamingToolCall{CallID: id, Name: call.Name, Arguments: args, Index: len(d.calls), ProviderData: pdata}, PartIndex: index})
					d.lastCall = len(d.calls) - 1
					d.buffering = true
					d.pending = append(d.pending, pendingEvent{CallIndex: d.lastCall})
				}
			case p.InlineData != nil:
				if !strings.HasPrefix(p.InlineData.MimeType, "image/") {
					return fmt.Errorf("unsupported_response_content: Gemini inline media %s", p.InlineData.MimeType)
				}
				if d.model != "" {
					pdata.Models = []string{d.model}
				}
				block := &ir.ContentBlock{Type: ir.ContentTypeImage, MimeType: p.InlineData.MimeType, MediaB64: p.InlineData.Data, ProviderData: pdata}
				d.image = &ir.Event{Type: ir.EventContentBlock, ContentBlock: block, ContentBlockIndex: &index}
			case p.FileData != nil:
				if !strings.HasPrefix(p.FileData.MimeType, "image/") {
					return fmt.Errorf("unsupported_response_content: Gemini file media %s", p.FileData.MimeType)
				}
				if d.model != "" {
					pdata.Models = []string{d.model}
				}
				d.image = &ir.Event{Type: ir.EventContentBlock, ContentBlock: &ir.ContentBlock{Type: ir.ContentTypeImage, MimeType: p.FileData.MimeType, MediaURL: p.FileData.FileURI, ProviderData: pdata}, ContentBlockIndex: &index}
			case p.Text != nil:
				if p.Thought {
					d.reasoning = true
					d.reasoningSummary.WriteString(*p.Text)
					d.emit(ir.Event{Type: ir.EventReasoningSummaryDelta, Reasoning: &ir.ReasoningContent{Summary: []string{*p.Text}}, ContentBlockIndex: &index})
					// Chat 编码器仍消费兼容增量；结构化消费者会跳过此事件。
					d.emit(ir.Event{Type: ir.EventThinkingDelta, Delta: &ir.DeltaPayload{ContentType: ir.ContentTypeThinking, Text: *p.Text}, ContentBlockIndex: &index})
				} else {
					d.emit(ir.Event{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: *p.Text, ContentType: ir.ContentTypeText}, ContentBlockIndex: &index})
					d.textOffset += len(*p.Text)
				}
			case len(p.ThoughtSignature) > 0:
				// 2. 紧随函数的独立签名片段归入尚未发布的最近调用。
				if d.image != nil {
					d.image.ContentBlock.ProviderData.Fields["thoughtSignature"] = p.ThoughtSignature
				} else if d.lastCall >= 0 {
					d.calls[d.lastCall].Call.ProviderData.Fields["thoughtSignature"] = p.ThoughtSignature
				} else {
					if d.model != "" {
						pdata.Models = []string{d.model}
					}
					d.emit(ir.Event{Type: ir.EventContentBlock, ContentBlock: &ir.ContentBlock{Type: ir.ContentTypeText, ProviderData: pdata}, ContentBlockIndex: &index})
				}
			case p.FunctionResponse != nil, len(p.ExecutableCode) > 0, len(p.CodeExecutionResult) > 0:
				return fmt.Errorf("unsupported_response_content: Gemini executable code output")
			}
			d.flushCalls(false)
		}
	}
	// 3. 用量报告为累计值，保留上游原始统计供诊断。
	if response.UsageMetadata != nil {
		usage := response.UsageMetadata
		prompt := usage.PromptTokenCount + usage.ToolUsePromptTokenCount
		completion := usage.CandidatesTokenCount + usage.ThoughtsTokenCount
		input := prompt - usage.CachedContentTokenCount
		if input < 0 {
			input = 0
		}
		var metadata map[string]any
		_ = json.Unmarshal(raw, &metadata)
		d.emit(ir.Event{Type: ir.EventUsage, Usage: &ir.Usage{PromptTokens: input, CompletionTokens: completion, CacheReadTokens: usage.CachedContentTokenCount, ReasoningTokens: usage.ThoughtsTokenCount, TotalTokens: usage.TotalTokenCount}, Metadata: map[string]any{"usageMetadata": metadata["usageMetadata"]}})
	}
	return nil
}

// finishReasoning 输出本段完整的可读思考摘要。
func (d *responseDecoder) finishReasoning(status ir.ReasoningStatus) {
	// 1. 关闭连续 thought 分片，保留摘要而不伪装成隐藏推理正文。
	if !d.reasoning {
		return
	}
	d.emit(ir.Event{Type: ir.EventReasoningDone, Reasoning: &ir.ReasoningContent{Summary: []string{d.reasoningSummary.String()}}, ReasoningStatus: status})
	d.reasoning = false
	d.reasoningSummary.Reset()
}

// flushImage 输出已完成的图片块。
func (d *responseDecoder) flushImage() {
	// 1. 图片后没有紧随签名时沿用已有原生签名。
	if d.image != nil {
		d.emit(*d.image)
		d.image = nil
	}
}

// providerData 保存 part 原始签名和图文位置。
func (d *responseDecoder) providerData(signature json.RawMessage, index int) *ir.ProviderData {
	// 1. 位置提示不作为 Gemini 原生字段回传。
	position, _ := json.Marshal(contentPosition{PartIndex: index, TextOffset: d.textOffset})
	data := &ir.ProviderData{Protocol: ir.ProtocolGemini, Fields: map[string]json.RawMessage{"content_position": position}}
	if len(signature) > 0 && string(signature) != "null" {
		data.Fields["thoughtSignature"] = signature
	}
	return data
}

// flushCalls 在调用签名完整后生成标准工具事件。
func (d *responseDecoder) flushCalls(final bool) {
	// 1. 按原响应顺序发布调用和其后的事件，不让文本越过尚未签名的调用。
	d.buffering = false
	consumed := 0
	for _, queued := range d.pending {
		if queued.CallIndex >= 0 && !final && len(d.calls[queued.CallIndex].Call.ProviderData.Fields["thoughtSignature"]) == 0 {
			break
		}
		consumed++
		if queued.CallIndex < 0 {
			d.emit(queued.Event)
			continue
		}
		pending := d.calls[queued.CallIndex]
		call := pending.Call
		start := call
		start.Arguments = ""
		d.emit(ir.Event{Type: ir.EventToolCallStart, ToolCall: &start, ContentBlockIndex: &pending.PartIndex})
		if call.Arguments != "" {
			delta := call
			delta.ProviderData = nil
			d.emit(ir.Event{Type: ir.EventToolCallArgumentsDelta, ToolCall: &delta, ContentBlockIndex: &pending.PartIndex})
		}
		end := call
		d.emit(ir.Event{Type: ir.EventToolCallEnd, ToolCall: &end, ContentBlockIndex: &pending.PartIndex})
	}
	d.pending = d.pending[consumed:]
	d.buffering = len(d.pending) > 0
}

// emit 在请求有效时发送一个事件。
func (d *responseDecoder) emit(event ir.Event) bool {
	// 1. 调用签名收齐之前暂存后续事件。
	if d.buffering {
		d.pending = append(d.pending, pendingEvent{Event: event, CallIndex: -1})
		return d.ctx.Err() == nil
	}
	// 2. 取消后停止发送。
	select {
	case d.out <- event:
		return true
	case <-d.ctx.Done():
		return false
	}
}

// finishReason 将 Gemini 结束原因映射为通用值。
func finishReason(reason string) string {
	// 1. 已知结束原因映射，其他值保留供客户端记录。
	switch reason {
	case "", "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return "content_filter"
	default:
		return strings.ToLower(reason)
	}
}

// callID 生成工具调用的独立身份。
func callID() string {
	// 1. 使用随机 ID 关联没有原生 ID 的调用和结果。
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return "call_" + hex.EncodeToString(raw[:])
}
