package responses

import "github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"

type responsesAssistantTurn struct {
	reasoning  *ir.ContentBlock
	content    []ir.ContentBlock
	toolCalls  []ir.ToolCall
	hasMessage bool
}

func (turn *responsesAssistantTurn) addReasoning(block ir.ContentBlock) bool {
	if turn.reasoning != nil {
		return false
	}
	copied := block
	turn.reasoning = &copied
	return true
}

func (turn *responsesAssistantTurn) addMessage(content []ir.ContentBlock) bool {
	if turn.hasMessage {
		return false
	}
	turn.content = append(turn.content, content...)
	turn.hasMessage = true
	return true
}

func (turn *responsesAssistantTurn) addToolCall(call ir.ToolCall) {
	turn.toolCalls = append(turn.toolCalls, call)
}

func (turn *responsesAssistantTurn) flush() (ir.Message, bool) {
	if turn.reasoning == nil && !turn.hasMessage && len(turn.toolCalls) == 0 {
		return ir.Message{}, false
	}
	message := ir.Message{Role: ir.RoleAssistant, ToolCalls: turn.toolCalls}
	if turn.reasoning != nil {
		message.Content = append(message.Content, *turn.reasoning)
	}
	message.Content = append(message.Content, turn.content...)
	*turn = responsesAssistantTurn{}
	return message, true
}
