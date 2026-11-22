package gemini

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// omittedCallID marks a client-facing ID generated for an ID-less Gemini call.
const omittedCallID = "omitted_call_id"

type replayCall struct {
	wireID string
	name   string
	order  int
}

type orderedResponse struct {
	part  part
	order int
}

// encodeRequest 把消息、工具与生成设置转换为 Gemini 主体。
func encodeRequest(req *ir.Request, model string, options convert.Options) ([]byte, error) {
	var contents []content
	var system []part
	calls := map[string]replayCall{}
	callOrder := 0
	var pending []orderedResponse
	flushResponses := func() {
		if len(pending) == 0 {
			return
		}
		sort.SliceStable(pending, func(i, j int) bool { return pending[i].order < pending[j].order })
		parts := make([]part, len(pending))
		for i, response := range pending {
			parts[i] = response.part
		}
		contents = append(contents, content{Role: "user", Parts: parts})
		pending = pending[:0]
	}

	var previousRole ir.Role
	for _, message := range req.Messages {
		previous := previousRole
		previousRole = message.Role
		if message.Role == ir.RoleTool {
			call, ok := calls[message.ToolCallID]
			if !ok {
				return nil, fmt.Errorf("Gemini function response has no matching call: %s", message.ToolCallID)
			}
			response, err := encodeFunctionResponse(message, call, model)
			if err != nil {
				return nil, err
			}
			pending = append(pending, orderedResponse{part: response, order: call.order})
			continue
		}
		flushResponses()

		parts, err := encodeMessage(message, model)
		if err != nil {
			return nil, err
		}
		for _, call := range message.ToolCalls {
			wireID := call.ID
			if call.ProviderData != nil && call.ProviderData.Protocol == ir.ProtocolGemini &&
				string(call.ProviderData.Fields[omittedCallID]) == "true" {
				wireID = ""
			}
			calls[call.ID] = replayCall{wireID: wireID, name: call.Name}
		}
		// Parts may have been reordered to match the original Gemini response.
		for _, p := range parts {
			if p.FunctionCall != nil {
				id := p.FunctionCall.ID
				call, ok := calls[id]
				if !ok {
					continue
				}
				call.order = callOrder
				callOrder++
				calls[id] = call
				p.FunctionCall.ID = call.wireID
			}
		}
		if message.Role == ir.RoleSystem || message.Role == ir.RoleDeveloper {
			system = append(system, parts...)
			continue
		}
		if len(parts) == 0 {
			continue
		}
		role := "user"
		if message.Role == ir.RoleAssistant {
			role = "model"
		}
		if message.Role == ir.RoleAssistant && previous == ir.RoleAssistant && len(contents) > 0 && contents[len(contents)-1].Role == "model" {
			contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
		} else {
			contents = append(contents, content{Role: role, Parts: parts})
		}
	}
	flushResponses()
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = content{Parts: system}
	}
	// 2. 映射通用生成参数、思考和工具。
	capabilities := generationCapabilities(options.GeminiGeneration)
	generation := capabilities.generationConfig(req)
	if err := encodeTools(req, options.BuiltinToolFallback, body, generation); err != nil {
		return nil, err
	}
	// Forward extension fields after translating the source protocol controls.
	for key, value := range req.Extras {
		switch key {
		case "generationConfig", "generation_config":
			// Native generation settings take precedence after translation.
		case "modalities":
			data, _ := json.Marshal(value)
			var modalities []string
			if json.Unmarshal(data, &modalities) == nil {
				for i := range modalities {
					modalities[i] = strings.ToUpper(modalities[i])
				}
				generation["responseModalities"] = modalities
			}
		case "model", "messages", "input", "stream", "max_tokens", "max_completion_tokens",
			"reasoning", "reasoning_effort", "response_format", "text", "stream_options",
			"metadata", "store", "include", "prompt_cache_key", "reasoning_summary",
			"tool_choice", "parallel_tool_calls", "client_metadata", "previous_response_id", "truncation":
			// Source protocol controls have no native generateContent field.
		default:
			body[key] = value
		}
	}
	if err := capabilities.mergeNativeConfig(generation, req.Extras); err != nil {
		return nil, err
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	return json.Marshal(body)
}

func encodeFunctionResponse(message ir.Message, call replayCall, model string) (part, error) {
	var texts []string
	var media []part
	for _, block := range message.Content {
		if block.Type == ir.ContentTypeText {
			texts = append(texts, block.Text)
			continue
		}
		parts, err := encodeBlock(block, model)
		if err != nil {
			return part{}, err
		}
		media = append(media, parts...)
	}
	text := strings.Join(texts, "\n")
	response := any(map[string]any{"result": text})
	if raw := json.RawMessage(strings.TrimSpace(text)); json.Valid(raw) {
		if raw[0] == '{' {
			response = raw
		} else {
			response = map[string]json.RawMessage{"result": raw}
		}
	}
	return part{FunctionResponse: &functionResponse{
		ID: call.wireID, Name: call.name, Response: response, Parts: media,
	}}, nil
}

// encodeMessage 转换内容和调用，并按可用的位置提示还原混合顺序。
func encodeMessage(message ir.Message, model string) ([]part, error) {
	// 1. 保留明确的内容数组顺序，收集调用和图片位置。
	var parts []part
	type positioned struct {
		Parts    []part
		Position contentPosition
	}
	var positions []positioned
	text := ""
	explicit := false
	for _, block := range message.Content {
		if value, ok := block.Metadata["explicit_content_order"].(bool); ok && value {
			explicit = true
		}
	}
	for _, block := range message.Content {
		encoded, err := encodeBlock(block, model)
		if err != nil {
			return nil, err
		}
		parts = append(parts, encoded...)
		if block.Type == ir.ContentTypeText {
			text += block.Text
		}
		if p, ok := position(block.ProviderData); ok && block.Type == ir.ContentTypeImage {
			positions = append(positions, positioned{encoded, p})
		}
	}
	for _, call := range message.ToolCalls {
		args := json.RawMessage(strings.TrimSpace(call.Arguments))
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage(`{}`)
		}
		if args[0] != '{' || !json.Valid(args) {
			return nil, fmt.Errorf("Gemini function %s requires JSON object arguments", call.Name)
		}
		p := part{FunctionCall: &functionCall{ID: call.ID, Name: call.Name, Args: args}, ThoughtSignature: signature(call.ProviderData)}
		parts = append(parts, p)
		if pos, ok := position(call.ProviderData); ok {
			positions = append(positions, positioned{[]part{p}, pos})
		}
	}
	// 2. 只在非文本项都有有效提示时拆分聚合文本，无效提示沿用原顺序。
	nonText := 0
	var thoughts []part
	for _, p := range parts {
		if p.Thought {
			thoughts = append(thoughts, p)
			continue
		}
		if p.Text == nil {
			nonText++
		}
	}
	if explicit || len(positions) == 0 || len(positions) != nonText {
		return parts, nil
	}
	sort.SliceStable(positions, func(i, j int) bool {
		if positions[i].Position.TextOffset == positions[j].Position.TextOffset {
			return positions[i].Position.PartIndex < positions[j].Position.PartIndex
		}
		return positions[i].Position.TextOffset < positions[j].Position.TextOffset
	})
	for _, p := range positions {
		offset := p.Position.TextOffset
		if offset < 0 || offset > len(text) || !utf8.ValidString(text[:offset]) {
			return parts, nil
		}
	}
	ordered := thoughts
	offset := 0
	for _, p := range positions {
		if p.Position.TextOffset > offset {
			fragment := text[offset:p.Position.TextOffset]
			ordered = append(ordered, part{Text: &fragment})
		}
		ordered = append(ordered, p.Parts...)
		offset = p.Position.TextOffset
	}
	if offset < len(text) {
		fragment := text[offset:]
		ordered = append(ordered, part{Text: &fragment})
	}
	return ordered, nil
}

// encodeBlock 转换一个 IR 内容块。
func encodeBlock(block ir.ContentBlock, model string) ([]part, error) {
	// 1. 生成目标原生内容，签名按来源协议和模型恢复。
	p := part{ThoughtSignature: signature(block.ProviderData)}
	switch block.Type {
	case ir.ContentTypeText, ir.ContentTypeInputText, ir.ContentTypeOutputText:
		p.Text = &block.Text
	case ir.ContentTypeThinking:
		if block.Reasoning != nil {
			values := block.Reasoning.Content
			if len(values) == 0 {
				values = block.Reasoning.Summary
			}
			text := strings.Join(values, "")
			p.Text = &text
		} else {
			p.Text = &block.Text
		}
		p.Thought = true
	case ir.ContentTypeImage, ir.ContentTypeImageURL, ir.ContentTypeAudio:
		data, mime := block.MediaB64, block.MimeType
		if data == "" && strings.HasPrefix(block.MediaURL, "data:") {
			head, tail, ok := strings.Cut(block.MediaURL, ",")
			if ok && strings.HasSuffix(head, ";base64") {
				data = tail
				mime = strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
			}
		}
		if data != "" {
			p.InlineData = &media{MimeType: mime, Data: data}
		} else if block.MediaURL != "" {
			p.FileData = &fileData{MimeType: mime, FileURI: block.MediaURL}
		} else {
			return nil, fmt.Errorf("Gemini media content has no data or URI")
		}
	default:
		if block.RawJSON != nil && block.ProviderData != nil && block.ProviderData.Protocol == ir.ProtocolGemini {
			if err := json.Unmarshal(block.RawJSON, &p); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("unsupported Gemini content conversion: %s", block.Type)
		}
	}
	return []part{p}, nil
}

// signature 读取 Gemini 来源的原始签名。
func signature(data *ir.ProviderData) json.RawMessage {
	// 1. 不匹配的来源不恢复厂商字段。
	if data == nil || data.Protocol != ir.ProtocolGemini {
		return nil
	}
	return data.Fields["thoughtSignature"]
}

// position 读取可选内容位置提示。
func position(data *ir.ProviderData) (contentPosition, bool) {
	// 1. 辅助元数据缺失或不可读时使用原消息顺序。
	var p contentPosition
	if data == nil {
		return p, false
	}
	raw := data.Fields["content_position"]
	return p, len(raw) > 0 && json.Unmarshal(raw, &p) == nil
}

// object 将配置值转为独立的 JSON 对象。
func object(value any) map[string]any {
	// 1. 通过 JSON 复制配置，保持原请求不变。
	var out map[string]any
	data, err := json.Marshal(value)
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}
