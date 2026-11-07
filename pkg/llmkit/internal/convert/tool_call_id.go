package convert

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

const (
	// toolCallIDFamilyPrefix 是所有 llmkit 打包 ID 共有的前缀，包括旧格式和以后的版本。
	toolCallIDFamilyPrefix = "call_llmkit_"
	// toolCallIDPrefix 是当前版本打包 ID 的前缀。
	toolCallIDPrefix = "call_llmkit_v1_"
	// brokenToolCallIDHexLen 是解不开的包换成稳定 ID 时取的十六进制位数。
	brokenToolCallIDHexLen = 24
)

// toolCallIDPayload 是打包进工具调用 ID 的 JSON。
type toolCallIDPayload struct {
	ID       string                     `json:"id"`
	Protocol ir.Protocol                `json:"protocol,omitempty"`
	Models   []string                   `json:"models,omitempty"`
	Fields   map[string]json.RawMessage `json:"fields"`
}

// PackToolCallID 把原始 ID 和 ProviderData 编成一个工具调用 ID；没有 ProviderData 时返回原始 ID。
func PackToolCallID(id string, data *ir.ToolCallProviderData) string {
	// 1. 没有 ProviderData，原样返回。
	if data == nil {
		return id
	}

	// 2. 按固定字段顺序编成 JSON；map 键由 encoding/json 排序，同样输入得到同样输出。
	payload, err := json.Marshal(toolCallIDPayload{
		ID:       id,
		Protocol: data.Protocol,
		Models:   data.Models,
		Fields:   data.Fields,
	})
	if err != nil {
		// Fields 里的 RawMessage 不是合法 JSON 时才会走到这里，此时只发原始 ID。
		return id
	}

	// 3. 用不带填充的 base64url 编码，保证结果只含 [A-Za-z0-9_-]。
	return toolCallIDPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

// IsPackedToolCallID 判断一个工具调用 ID 是不是 llmkit 打包的，任何版本的包和坏包都算。
func IsPackedToolCallID(id string) bool {
	return strings.HasPrefix(id, toolCallIDFamilyPrefix)
}

// UnpackToolCallID 从工具调用 ID 拆出原始 ID 和 ProviderData。
func UnpackToolCallID(id string) (rawID string, data *ir.ToolCallProviderData) {
	// 1. 不是 llmkit 打包 ID，原样返回。
	if !IsPackedToolCallID(id) {
		return id, nil
	}

	// 2. 前缀对但不是当前版本、base64 解不开、JSON 解不开、没有 id，都按坏包处理。
	encoded, isCurrentVersion := strings.CutPrefix(id, toolCallIDPrefix)
	if !isCurrentVersion {
		return brokenToolCallID(id), nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return brokenToolCallID(id), nil
	}
	var payload toolCallIDPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ID == "" {
		return brokenToolCallID(id), nil
	}

	// 3. 有 id 没有 protocol 是旧格式，只取回 ID。
	if payload.Protocol == "" {
		return payload.ID, nil
	}

	// 4. 返回原始 ID 和还原出的 ProviderData。
	return payload.ID, &ir.ToolCallProviderData{
		Protocol: payload.Protocol,
		Models:   payload.Models,
		Fields:   payload.Fields,
	}
}

// brokenToolCallID 把解不开的包换成 call_ 加原字符串 SHA-256 前 24 位十六进制，同一个坏包每次得到同一个 ID。
func brokenToolCallID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "call_" + hex.EncodeToString(sum[:])[:brokenToolCallIDHexLen]
}

// CloneToolCallProviderData 深拷贝 ProviderData，改副本不影响原值。
func CloneToolCallProviderData(data *ir.ToolCallProviderData) *ir.ToolCallProviderData {
	// 1. nil 仍然是 nil。
	if data == nil {
		return nil
	}

	// 2. 复制 Models 和每个字段的原始 JSON 字节。
	cloned := &ir.ToolCallProviderData{
		Protocol: data.Protocol,
		Models:   slices.Clone(data.Models),
	}
	if data.Fields != nil {
		cloned.Fields = make(map[string]json.RawMessage, len(data.Fields))
		for key, value := range data.Fields {
			cloned.Fields[key] = slices.Clone(value)
		}
	}
	return cloned
}

// ToolSignatureFallback 是一轮工具调用没有可用签名时补什么。
type ToolSignatureFallback string

const (
	ToolSignatureFallbackNone   ToolSignatureFallback = "none"
	ToolSignatureFallbackGemini ToolSignatureFallback = "gemini"
)

// geminiFallbackExtraContent 是 gemini 兜底写进第一个调用的 extra_content，Gemini 认这个占位签名。
const geminiFallbackExtraContent = `{"google":{"thought_signature":"context_engineering_is_the_way_to_go"}}`

// UnpackRequestToolCallIDs 把使用者请求里工具调用和工具结果的包拆回原始 ID 和 ProviderData。
func UnpackRequestToolCallIDs(request *ir.Request) {
	for messageIndex := range request.Messages {
		message := &request.Messages[messageIndex]
		// 1. 工具结果只取回原始 ID。
		if message.ToolCallID != "" {
			message.ToolCallID, _ = UnpackToolCallID(message.ToolCallID)
		}

		// 2. 工具调用拆出 ProviderData；包和原生字段同时回传时以包为准。
		for callIndex := range message.ToolCalls {
			call := &message.ToolCalls[callIndex]
			rawID, data := UnpackToolCallID(call.ID)
			call.ID = rawID
			if data != nil {
				call.ProviderData = data
			}
		}
	}
}

// PrepareToolCallsForUpstream 按目标协议和模型决定每个工具调用写原生字段、写包还是只写原始 ID，再按兜底补假签名。
func PrepareToolCallsForUpstream(request *ir.Request, targetProtocol ir.Protocol, targetModel string, fallback ToolSignatureFallback) {
	// 1. 拆开 IR 里残留的包，下面只处理原始 ID。
	UnpackRequestToolCallIDs(request)

	// 2. Chat 编码器会把同一轮拆开的工具调用合成一条消息，先按同样方式合并，兜底才能按整轮判断。
	if targetProtocol == ir.ProtocolOpenAIChat {
		request.Messages = NormalizeAssistantToolCallSequence(request.Messages)
	}

	// 3. 逐条消息处理：工具结果跟随前面最近一个同原始 ID 的调用实际写出的 ID。
	writtenIDs := map[string]string{}
	for messageIndex := range request.Messages {
		message := &request.Messages[messageIndex]
		if writtenID, ok := writtenIDs[message.ToolCallID]; ok && message.Role == ir.RoleTool {
			message.ToolCallID = writtenID
		}
		if len(message.ToolCalls) == 0 {
			continue
		}

		// 4. 工具调用按规则改写 ID 和交给编码器写成原生字段的 ProviderData；写成包的调用也算已签名。
		signed := false
		for callIndex := range message.ToolCalls {
			call := &message.ToolCalls[callIndex]
			rawID := call.ID
			call.ID, call.ProviderData = upstreamToolCall(rawID, call.ProviderData, targetProtocol, targetModel)
			writtenIDs[rawID] = call.ID
			signed = signed || call.ProviderData != nil || call.ID != rawID
		}

		// 5. 这一轮一个签名（原生字段或包）都没写时，按兜底给第一个调用补占位签名。
		if fallback == ToolSignatureFallbackGemini && targetProtocol == ir.ProtocolOpenAIChat && !signed {
			message.ToolCalls[0].ProviderData = &ir.ToolCallProviderData{
				Protocol: ir.ProtocolOpenAIChat,
				Fields:   map[string]json.RawMessage{"extra_content": json.RawMessage(geminiFallbackExtraContent)},
			}
		}
	}
}

// upstreamToolCall 按 ProviderData 的模型记录决定一个调用写给上游的 ID，以及要写成原生字段的 ProviderData（不写时为 nil）。
func upstreamToolCall(rawID string, data *ir.ToolCallProviderData, targetProtocol ir.Protocol, targetModel string) (string, *ir.ToolCallProviderData) {
	if data == nil {
		return rawID, nil
	}
	last := len(data.Models) - 1
	switch {
	case last < 0:
		// 使用者自带，目标协议对得上就照写。
		return rawID, nativeToolCallProviderData(data, targetProtocol)
	case data.Models[last] != targetModel:
		// 这一层记的模型和本次发给上游的模型对不上，签名不能用。
		return rawID, nil
	case last > 0:
		// 取走本层的记录后还有剩，下一跳是另一层网关，整包往下传。
		remaining := CloneToolCallProviderData(data)
		remaining.Models = remaining.Models[:last]
		return PackToolCallID(rawID, remaining), nil
	default:
		// 取走后空了，下一跳就是产生签名的上游。
		return rawID, nativeToolCallProviderData(data, targetProtocol)
	}
}

// nativeToolCallProviderData 在目标协议就是产生字段的协议时，返回只带字段的 ProviderData 交给编码器写回；否则返回 nil。
func nativeToolCallProviderData(data *ir.ToolCallProviderData, targetProtocol ir.Protocol) *ir.ToolCallProviderData {
	if data.Protocol != targetProtocol || len(data.Fields) == 0 {
		return nil
	}
	return &ir.ToolCallProviderData{Protocol: data.Protocol, Fields: data.Fields}
}

// UnpackToolCallEvents 拆开上游响应事件里的包，并给 ProviderData 追加本层模型名。
func UnpackToolCallEvents(ctx context.Context, events <-chan ir.Event, targetModel string) <-chan ir.Event {
	rawIDs := map[string]string{}
	return forwardToolCallEvents(ctx, events, func(event ir.Event) ir.Event {
		return rewriteToolCallEvent(event, func(eventType ir.EventType, id string, data *ir.ToolCallProviderData) (string, *ir.ToolCallProviderData) {
			// 1. 参数分片只需要原始 ID，同一个包只拆一次。
			if eventType == ir.EventToolCallArgumentsDelta {
				if rawID, ok := rawIDs[id]; ok {
					return rawID, nil
				}
				rawID, _ := UnpackToolCallID(id)
				rawIDs[id] = rawID
				return rawID, nil
			}

			// 2. 包里的 ProviderData 优先，否则复制解码器给的，避免改到别处共用的值。
			rawID, packed := UnpackToolCallID(id)
			rawIDs[id] = rawID
			if packed != nil {
				data = packed
			} else {
				data = CloneToolCallProviderData(data)
			}

			// 3. 追加本层发给上游的模型名；没有转换状态时不追加，按使用者自带处理。
			if data != nil && targetModel != "" {
				data.Models = append(data.Models, targetModel)
			}
			return rawID, data
		})
	})
}

// PackToolCallEvents 把写给使用者的工具调用 ID 打包，并清掉 ProviderData。
func PackToolCallEvents(ctx context.Context, events <-chan ir.Event) <-chan ir.Event {
	packedIDs := map[string]string{}
	return forwardToolCallEvents(ctx, events, func(event ir.Event) ir.Event {
		return rewriteToolCallEvent(event, func(eventType ir.EventType, id string, data *ir.ToolCallProviderData) (string, *ir.ToolCallProviderData) {
			// 1. 同一个调用的 ID 在 Start 时定下，之后的分片和结束事件沿用。
			if packedID, ok := packedIDs[id]; ok && eventType != ir.EventToolCallStart {
				return packedID, nil
			}

			// 2. Start 或没有 Start 的非流式调用按当时已有的 ProviderData 打包。
			packedID := PackToolCallID(id, data)
			packedIDs[id] = packedID
			return packedID, nil
		})
	})
}

// rewriteToolCallEvent 复制工具调用事件并用 rewrite 改写其中的 ID 和 ProviderData；其他事件原样返回。
func rewriteToolCallEvent(event ir.Event, rewrite func(eventType ir.EventType, id string, data *ir.ToolCallProviderData) (string, *ir.ToolCallProviderData)) ir.Event {
	switch event.Type {
	case ir.EventToolCallStart, ir.EventToolCallArgumentsDelta, ir.EventToolCallEnd:
		if event.ToolCall == nil {
			return event
		}
		call := *event.ToolCall
		call.CallID, call.ProviderData = rewrite(event.Type, call.CallID, call.ProviderData)
		event.ToolCall = &call
	case ir.EventToolCallDelta:
		if event.Delta == nil || event.Delta.ToolCall == nil {
			return event
		}
		delta := *event.Delta
		call := *delta.ToolCall
		call.ID, call.ProviderData = rewrite(event.Type, call.ID, call.ProviderData)
		delta.ToolCall = &call
		event.Delta = &delta
	}
	return event
}

// forwardToolCallEvents 把每个事件改写后转发到新通道，上游关闭时关闭新通道。
// 取消后仍转发上游补发的收尾事件；使用者不再读取、缓冲又满时丢掉剩下的事件并在后台排空上游。
func forwardToolCallEvents(ctx context.Context, events <-chan ir.Event, rewrite func(ir.Event) ir.Event) <-chan ir.Event {
	if events == nil {
		return nil
	}
	out := make(chan ir.Event, 64)
	go func() {
		defer close(out)
		for event := range events {
			rewritten := rewrite(event)
			// 1. 缓冲有空位就直接放入，取消后上游补发的中断事件也能送到。
			select {
			case out <- rewritten:
				continue
			default:
			}

			// 2. 缓冲满了就等使用者读取；已取消时不再等待。
			select {
			case out <- rewritten:
			case <-ctx.Done():
				go func() {
					for range events {
					}
				}()
				return
			}
		}
	}()
	return out
}
