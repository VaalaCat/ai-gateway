package native

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

type responsesOverloadedError struct {
	code    string
	message string
}

func (err *responsesOverloadedError) Error() string {
	return fmt.Sprintf("openai responses stream failed: %s: %s", err.code, err.message)
}

func gateResponsesPreamble(
	ctx context.Context,
	events <-chan llmkit.Event,
	stream bool,
	outbound llmkit.Protocol,
) (<-chan llmkit.Event, error) {
	if !stream || outbound != llmkit.ProtocolOpenAIResponses {
		return events, nil
	}

	var buffered [2]llmkit.Event
	bufferedCount := 0
	created := false
	inProgress := false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-events:
			if !ok {
				output := make(chan llmkit.Event, bufferedCount)
				for _, event := range buffered[:bufferedCount] {
					output <- event
				}
				close(output)
				return output, nil
			}

			if isResponsesKeepalive(event) {
				continue
			}

			if event.Type == llmkit.EventError && event.Error != nil && event.Error.Code == "server_is_overloaded" && created {
				return nil, &responsesOverloadedError{
					code:    event.Error.Code,
					message: event.Error.Message,
				}
			}
			if isResponsesCreated(event) && !created {
				buffered[bufferedCount] = event
				bufferedCount++
				created = true
				continue
			}
			if isResponsesInProgress(event) && created && !inProgress {
				buffered[bufferedCount] = event
				bufferedCount++
				inProgress = true
				continue
			}

			output := make(chan llmkit.Event)
			go forwardResponsesEvents(ctx, output, events, buffered[:bufferedCount], event)
			return output, nil
		}
	}
}

func isResponsesCreated(event llmkit.Event) bool {
	return event.Type == llmkit.EventStreamStart && event.RawPassthrough != nil &&
		event.RawPassthrough.EventName == "response.created"
}

func isResponsesInProgress(event llmkit.Event) bool {
	return event.Type == llmkit.EventRawPassthrough && event.RawPassthrough != nil &&
		event.RawPassthrough.EventName == "response.in_progress"
}

func isResponsesKeepalive(event llmkit.Event) bool {
	return event.Type == llmkit.EventRawPassthrough && event.RawPassthrough != nil &&
		event.RawPassthrough.EventName == "keepalive"
}

func forwardResponsesEvents(
	ctx context.Context,
	output chan<- llmkit.Event,
	events <-chan llmkit.Event,
	buffered []llmkit.Event,
	current llmkit.Event,
) {
	defer close(output)

	for _, event := range buffered {
		select {
		case output <- event:
		case <-ctx.Done():
			return
		}
	}
	select {
	case output <- current:
	case <-ctx.Done():
		return
	}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			select {
			case output <- event:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
