package sse

// OpenAI Responses API SSE event names.
const (
	ResponseCreated            = "response.created"
	ResponseInProgress         = "response.in_progress"
	ResponseCompleted          = "response.completed"
	ResponseFailed             = "response.failed"
	ResponseIncomplete         = "response.incomplete"
	OutputItemAdded            = "response.output_item.added"
	OutputItemDone             = "response.output_item.done"
	ContentPartAdded           = "response.content_part.added"
	ContentPartDelta           = "response.content_part.delta"
	ContentPartDone            = "response.content_part.done"
	OutputTextDelta            = "response.output_text.delta"
	OutputTextDone             = "response.output_text.done"
	ReasoningTextDelta         = "response.reasoning_text.delta"
	FunctionCallArgumentsDelta = "response.function_call_arguments.delta"
	FunctionCallArgumentsDone  = "response.function_call_arguments.done"
	RefusalDelta               = "response.refusal.delta"
)

// OpenAI Chat Completions SSE data marker.
const (
	ChatStreamDone = "[DONE]"
)
