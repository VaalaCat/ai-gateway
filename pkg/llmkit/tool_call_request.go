package llmkit

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
)

// ToolCallRequestInfo 描述请求里和工具调用有关的三个特征。
type ToolCallRequestInfo struct {
	HasTools            bool // 请求里有工具定义
	HasToolHistory      bool // 历史里有工具调用或工具结果
	HasPackedToolCallID bool // 历史里有 call_llmkit_ 开头的工具调用 ID
}

// toolCallIDReaders 按协议取出请求历史里所有工具调用和工具结果的 ID（缺失的 ID 记为空串）。
var toolCallIDReaders = map[Protocol]func(raw map[string]any) []string{
	ProtocolOpenAIChat:      chatToolCallIDs,
	ProtocolOpenAIResponses: responsesToolCallIDs,
	ProtocolClaudeMessages:  claudeToolCallIDs,
}

// InspectToolCallRequest 读取请求里的工具定义、工具历史和打包的工具调用 ID，不做协议转换。
func InspectToolCallRequest(protocol Protocol, body []byte) (ToolCallRequestInfo, error) {
	var info ToolCallRequestInfo
	// 1. 只有三种协议会带工具，其余协议直接返回空特征
	readIDs, ok := toolCallIDReaders[protocol]
	if !ok {
		return info, nil
	}

	// 2. 解析请求体
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return info, err
	}

	// 3. 工具定义
	if tools, ok := raw["tools"].([]any); ok {
		info.HasTools = len(tools) > 0
	}

	// 4. 工具历史和打包的 ID
	ids := readIDs(raw)
	info.HasToolHistory = len(ids) > 0
	for _, id := range ids {
		if convert.IsPackedToolCallID(id) {
			info.HasPackedToolCallID = true
			break
		}
	}
	return info, nil
}

// chatToolCallIDs 读取 Chat 请求里 assistant 的 tool_calls 和 tool 消息的 tool_call_id。
func chatToolCallIDs(raw map[string]any) []string {
	var ids []string
	messages, _ := raw["messages"].([]any)
	for _, value := range messages {
		message, _ := value.(map[string]any)
		if message["role"] == "tool" {
			ids = append(ids, idString(message["tool_call_id"]))
		}
		calls, _ := message["tool_calls"].([]any)
		for _, value := range calls {
			call, _ := value.(map[string]any)
			ids = append(ids, idString(call["id"]))
		}
	}
	return ids
}

// responsesToolCallIDs 读取 Responses 请求 input 里工具调用和工具输出的 call_id。
func responsesToolCallIDs(raw map[string]any) []string {
	var ids []string
	items, _ := raw["input"].([]any)
	for _, value := range items {
		item, _ := value.(map[string]any)
		switch item["type"] {
		case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
			ids = append(ids, idString(item["call_id"]))
		}
	}
	return ids
}

// claudeToolCallIDs 读取 Claude 请求里 tool_use 的 id 和 tool_result 的 tool_use_id。
func claudeToolCallIDs(raw map[string]any) []string {
	var ids []string
	messages, _ := raw["messages"].([]any)
	for _, value := range messages {
		message, _ := value.(map[string]any)
		blocks, _ := message["content"].([]any)
		for _, value := range blocks {
			block, _ := value.(map[string]any)
			switch block["type"] {
			case "tool_use":
				ids = append(ids, idString(block["id"]))
			case "tool_result":
				ids = append(ids, idString(block["tool_use_id"]))
			}
		}
	}
	return ids
}

// idString 取字符串形式的 ID，类型不对时返回空串。
func idString(value any) string {
	id, _ := value.(string)
	return id
}
