package gemini

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// encodeRequest 把消息、工具与生成设置转换为 Gemini 主体。
func encodeRequest(req *ir.Request, model string) ([]byte, error) {
	// 1. 转换消息并通过调用 ID 关联函数结果。
	var contents []content
	var system []part
	calls := map[string]ir.ToolCall{}
	var previousRole ir.Role
	for _, message := range req.Messages {
		previous := previousRole
		previousRole = message.Role
		if message.Role == ir.RoleTool {
			call, ok := calls[message.ToolCallID]
			if !ok {
				return nil, fmt.Errorf("Gemini function response has no matching call: %s", message.ToolCallID)
			}
			var texts []string
			var images []part
			for _, block := range message.Content {
				if block.Type == ir.ContentTypeText {
					texts = append(texts, block.Text)
				} else {
					p, err := encodeBlock(block, model)
					if err != nil {
						return nil, err
					}
					images = append(images, p...)
				}
			}
			text := strings.Join(texts, "\n")
			var result map[string]any
			if json.Unmarshal([]byte(text), &result) != nil || result == nil {
				result = map[string]any{"result": text}
			}
			p := part{FunctionResponse: &functionResponse{ID: call.ID, Name: call.Name, Response: result, Parts: images}}
			if previous == ir.RoleTool {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, p)
			} else {
				contents = append(contents, content{Role: "user", Parts: []part{p}})
			}
			continue
		}
		parts, err := encodeMessage(message, model)
		if err != nil {
			return nil, err
		}
		for _, call := range message.ToolCalls {
			calls[call.ID] = call
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
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = content{Parts: system}
	}
	// 2. 映射通用生成参数、思考和工具。
	generation := map[string]any{}
	if req.MaxTokens != 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		generation["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generation["topP"] = *req.TopP
	}
	if req.TopK != nil {
		generation["topK"] = *req.TopK
	}
	if req.Seed != nil {
		generation["seed"] = *req.Seed
	}
	if req.N != 0 {
		generation["candidateCount"] = req.N
	}
	if len(req.StopWords) > 0 {
		generation["stopSequences"] = req.StopWords
	}
	if req.FrequencyPenalty != nil {
		generation["frequencyPenalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		generation["presencePenalty"] = *req.PresencePenalty
	}
	if req.Logprobs != nil {
		generation["responseLogprobs"] = *req.Logprobs
	}
	if req.TopLogprobs != nil {
		generation["logprobs"] = *req.TopLogprobs
	}
	thinking := map[string]any{}
	switch {
	case req.ThinkingBudget != nil:
		thinking["thinkingBudget"] = *req.ThinkingBudget
	case req.ThinkingEnabled != nil && !*req.ThinkingEnabled || req.ReasoningEffort == "none":
		thinking["thinkingBudget"] = 0
	case req.ReasoningEffort != "":
		thinking["thinkingLevel"] = req.ReasoningEffort
	case req.ThinkingEnabled != nil && *req.ThinkingEnabled:
		thinking["thinkingBudget"] = -1
	}
	if req.IncludeThoughts != nil {
		thinking["includeThoughts"] = *req.IncludeThoughts
	}
	if len(thinking) > 0 {
		generation["thinkingConfig"] = thinking
	}
	format := object(req.ResponseFormat)
	if len(format) == 0 {
		format = object(object(req.Extras["text"])["format"])
	}
	if format["type"] == "json_object" {
		generation["responseMimeType"] = "application/json"
	}
	if format["type"] == "json_schema" {
		generation["responseMimeType"] = "application/json"
		schema := object(format["json_schema"])
		if schema["schema"] != nil {
			generation["responseJsonSchema"] = schema["schema"]
		} else if format["schema"] != nil {
			generation["responseJsonSchema"] = format["schema"]
		}
	}
	var declarations []any
	var tools []any
	for _, tool := range req.Tools {
		switch tool.Type {
		case "", "function":
			declaration := map[string]any{"name": tool.Name, "description": tool.Description}
			if tool.InputSchema != nil {
				declaration["parametersJsonSchema"] = tool.InputSchema
			}
			declarations = append(declarations, declaration)
		case "image_generation":
			generation["responseModalities"] = []string{"TEXT", "IMAGE"}
			raw := object(tool.RawConfig)
			imageConfig := map[string]any{}
			if ratio, ok := raw["aspect_ratio"]; ok {
				imageConfig["aspectRatio"] = ratio
			}
			if size, ok := raw["image_size"]; ok {
				imageConfig["imageSize"] = size
			}
			if config, ok := raw["imageConfig"]; ok {
				for key, value := range object(config) {
					imageConfig[key] = value
				}
			}
			if len(imageConfig) > 0 {
				generation["imageConfig"] = imageConfig
			}
		case "web_search", "web_search_preview":
			tools = append(tools, map[string]any{"googleSearch": map[string]any{}})
		default:
			if tool.RawConfig != nil {
				tools = append(tools, tool.RawConfig)
			}
		}
	}
	if len(declarations) > 0 {
		tools = append(tools, map[string]any{"functionDeclarations": declarations})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if req.ToolChoice != nil {
		mode := "AUTO"
		config := map[string]any{}
		switch req.ToolChoice.Type {
		case "none":
			mode = "NONE"
		case "required":
			mode = "ANY"
		case "function":
			mode = "ANY"
			config["allowedFunctionNames"] = []string{req.ToolChoice.Name}
		}
		config["mode"] = mode
		body["toolConfig"] = map[string]any{"functionCallingConfig": config}
	}
	// 3. 保留目标原生扩展，明确 generationConfig 逐字段覆盖映射值。
	for key, value := range req.Extras {
		switch key {
		case "generationConfig":
			for k, v := range object(value) {
				generation[k] = v
			}
		case "modalities":
			data, _ := json.Marshal(value)
			var modalities []string
			if json.Unmarshal(data, &modalities) == nil {
				for i := range modalities {
					modalities[i] = strings.ToUpper(modalities[i])
				}
				if _, explicit := object(req.Extras["generationConfig"])["responseModalities"]; !explicit {
					generation["responseModalities"] = modalities
				}
			}
		case "model", "messages", "input", "stream", "max_tokens", "max_completion_tokens", "reasoning", "reasoning_effort", "response_format", "text", "stream_options", "metadata", "store", "include", "prompt_cache_key", "reasoning_summary", "tool_choice", "parallel_tool_calls":
		default:
			body[key] = value
		}
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	return json.Marshal(body)
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
		args := json.RawMessage(call.Arguments)
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		if !json.Valid(args) {
			return nil, fmt.Errorf("invalid Gemini function arguments for %s", call.Name)
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
