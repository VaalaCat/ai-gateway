package native

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"go.uber.org/goleak"
)

func TestGateResponsesPreamble_OverloadBeforeCommitReturnsTypedError(t *testing.T) {
	input := closedResponsesEventChannel(
		llmkit.Event{
			Type: llmkit.EventStreamStart,
			RawPassthrough: &llmkit.RawSSEEvent{
				EventName: "response.created",
				Data:      `{"type":"response.created","sequence_number":0}`,
			},
		},
		llmkit.Event{
			Type: llmkit.EventRawPassthrough,
			RawPassthrough: &llmkit.RawSSEEvent{
				EventName: "response.in_progress",
				Data:      `{"type":"response.in_progress","sequence_number":1}`,
			},
		},
		llmkit.Event{
			Type: llmkit.EventError,
			Error: &llmkit.ErrorPayload{
				Code:    "server_is_overloaded",
				Message: "The server is overloaded. Please try again later.",
			},
		},
	)

	output, err := gateResponsesPreamble(
		context.Background(), input, true, llmkit.ProtocolOpenAIResponses,
	)
	if output != nil {
		t.Fatalf("output = %v, want nil before any event can be committed", output)
	}
	if err == nil {
		t.Fatal("error = nil, want responsesOverloadedError")
	}
	var overloaded *responsesOverloadedError
	if !errors.As(err, &overloaded) {
		t.Fatalf("error type = %T, want *responsesOverloadedError", err)
	}
	if overloaded.code != "server_is_overloaded" {
		t.Errorf("error code = %q, want %q", overloaded.code, "server_is_overloaded")
	}
	if overloaded.message != "The server is overloaded. Please try again later." {
		t.Errorf("error message = %q, want exact upstream message", overloaded.message)
	}
	if got := err.Error(); got != "openai responses stream failed: server_is_overloaded: The server is overloaded. Please try again later." {
		t.Errorf("Error() = %q, want exact formatted error", got)
	}
}

// behavior change: upstream keepalive events do not commit the response preamble.
func TestGateResponsesPreamble_KeepaliveBeforeOverloadReturnsTypedError(t *testing.T) {
	input := closedResponsesEventChannel(
		llmkit.Event{
			Type: llmkit.EventStreamStart,
			RawPassthrough: &llmkit.RawSSEEvent{
				EventName: "response.created",
				Data:      `{"type":"response.created","sequence_number":0}`,
			},
		},
		llmkit.Event{
			Type: llmkit.EventRawPassthrough,
			RawPassthrough: &llmkit.RawSSEEvent{
				EventName: "response.in_progress",
				Data:      `{"type":"response.in_progress","sequence_number":1}`,
			},
		},
		llmkit.Event{
			Type: llmkit.EventRawPassthrough,
			RawPassthrough: &llmkit.RawSSEEvent{
				EventName: "keepalive",
				Data:      `{"type":"keepalive","sequence_number":2}`,
			},
		},
		llmkit.Event{
			Type: llmkit.EventError,
			Error: &llmkit.ErrorPayload{
				Code:    "server_is_overloaded",
				Message: "Our servers are currently overloaded. Please try again later.",
			},
		},
	)

	output, err := gateResponsesPreamble(
		context.Background(), input, true, llmkit.ProtocolOpenAIResponses,
	)
	if output != nil {
		t.Fatalf("output = %v, want nil before any event can be committed", output)
	}
	var overloaded *responsesOverloadedError
	if !errors.As(err, &overloaded) {
		t.Fatalf("error = %v (%T), want *responsesOverloadedError", err, err)
	}
}

func TestGateResponsesPreamble_KeepaliveBeforeCreatedDoesNotBlockOverload(t *testing.T) {
	keepalive := llmkit.Event{
		Type: llmkit.EventRawPassthrough,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "keepalive",
			Data:      `{"type":"keepalive","sequence_number":0}`,
		},
	}
	created := llmkit.Event{
		Type: llmkit.EventStreamStart,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.created",
			Data:      `{"type":"response.created","sequence_number":1}`,
		},
	}
	inProgress := llmkit.Event{
		Type: llmkit.EventRawPassthrough,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.in_progress",
			Data:      `{"type":"response.in_progress","sequence_number":2}`,
		},
	}
	overloaded := llmkit.Event{
		Type: llmkit.EventError,
		Error: &llmkit.ErrorPayload{
			Code:    "server_is_overloaded",
			Message: "overloaded after leading keepalive",
		},
	}

	output, err := gateResponsesPreamble(
		context.Background(),
		closedResponsesEventChannel(keepalive, created, inProgress, overloaded),
		true,
		llmkit.ProtocolOpenAIResponses,
	)
	if output != nil {
		t.Fatalf("output = %v, want nil before any event can be committed", output)
	}
	var typed *responsesOverloadedError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v (%T), want *responsesOverloadedError", err, err)
	}
}

func TestGateResponsesPreamble_RepeatedKeepalivesBeforeContentAreDropped(t *testing.T) {
	created := llmkit.Event{
		Type: llmkit.EventStreamStart,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.created",
			Data:      `{"type":"response.created","sequence_number":0}`,
		},
	}
	inProgress := llmkit.Event{
		Type: llmkit.EventRawPassthrough,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.in_progress",
			Data:      `{"type":"response.in_progress","sequence_number":1}`,
		},
	}
	content := llmkit.Event{
		Type:  llmkit.EventContentDelta,
		Delta: &llmkit.DeltaPayload{Text: "ready"},
	}
	events := []llmkit.Event{created, inProgress}
	for sequence := 2; sequence < 1026; sequence++ {
		events = append(events, llmkit.Event{
			Type: llmkit.EventRawPassthrough,
			RawPassthrough: &llmkit.RawSSEEvent{
				EventName: "keepalive",
				Data:      fmt.Sprintf(`{"type":"keepalive","sequence_number":%d}`, sequence),
			},
		})
	}
	events = append(events, content)

	output, err := gateResponsesPreamble(
		context.Background(), closedResponsesEventChannel(events...), true, llmkit.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatalf("gateResponsesPreamble() error = %v, want nil", err)
	}
	requireExactResponsesEvents(t, drainResponsesEvents(output), []llmkit.Event{created, inProgress, content})
}

func TestGateResponsesPreamble_NonOverloadErrorsReleaseExactEvents(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		message string
	}{
		{
			name:    "server_error_even_when_message_names_overload",
			code:    "server_error",
			message: "server_is_overloaded",
		},
		{
			name:    "overload_code_prefix_is_not_exact",
			code:    "server_is_overloaded_retryable",
			message: "The server is overloaded.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created := llmkit.Event{
				Type: llmkit.EventStreamStart,
				RawPassthrough: &llmkit.RawSSEEvent{
					EventName: "response.created",
					Data:      `{"type":"response.created","sequence_number":0}`,
				},
			}
			inProgress := llmkit.Event{
				Type: llmkit.EventRawPassthrough,
				RawPassthrough: &llmkit.RawSSEEvent{
					EventName: "response.in_progress",
					Data:      `{"type":"response.in_progress","sequence_number":1}`,
				},
			}
			ordinaryError := llmkit.Event{
				Type: llmkit.EventError,
				Error: &llmkit.ErrorPayload{
					Code:    test.code,
					Message: test.message,
				},
			}

			output, err := gateResponsesPreamble(
				context.Background(),
				closedResponsesEventChannel(created, inProgress, ordinaryError),
				true,
				llmkit.ProtocolOpenAIResponses,
			)
			if err != nil {
				t.Fatalf("gateResponsesPreamble() error = %v, want nil", err)
			}
			requireExactResponsesEvents(t, drainResponsesEvents(output), []llmkit.Event{
				created,
				inProgress,
				ordinaryError,
			})
		})
	}
}

func TestGateResponsesPreamble_ContentCommitsBeforeOverload(t *testing.T) {
	created := llmkit.Event{
		Type: llmkit.EventStreamStart,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.created",
			Data:      `{"type":"response.created","sequence_number":0}`,
		},
	}
	partial := llmkit.Event{
		Type:  llmkit.EventContentDelta,
		Delta: &llmkit.DeltaPayload{Text: "partial"},
	}
	overloaded := llmkit.Event{
		Type: llmkit.EventError,
		Error: &llmkit.ErrorPayload{
			Code:    "server_is_overloaded",
			Message: "overloaded after content",
		},
	}

	output, err := gateResponsesPreamble(
		context.Background(),
		closedResponsesEventChannel(created, partial, overloaded),
		true,
		llmkit.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatalf("gateResponsesPreamble() error = %v, want nil after content commit", err)
	}
	requireExactResponsesEvents(t, drainResponsesEvents(output), []llmkit.Event{
		created,
		partial,
		overloaded,
	})
}

func TestGateResponsesPreamble_BypassReturnsInputChannelWithoutRead(t *testing.T) {
	tests := []struct {
		name     string
		stream   bool
		outbound llmkit.Protocol
	}{
		{
			name:     "non_streaming_request",
			stream:   false,
			outbound: llmkit.ProtocolOpenAIResponses,
		},
		{
			name:     "openai_chat_outbound",
			stream:   true,
			outbound: llmkit.ProtocolOpenAIChat,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := llmkit.Event{
				Type:  llmkit.EventContentDelta,
				Delta: &llmkit.DeltaPayload{Text: "first"},
			}
			input := make(chan llmkit.Event, 1)
			input <- first
			close(input)

			output, err := gateResponsesPreamble(
				context.Background(), input, test.stream, test.outbound,
			)
			if err != nil {
				t.Fatalf("gateResponsesPreamble() error = %v, want nil", err)
			}
			if output != (<-chan llmkit.Event)(input) {
				t.Fatal("output channel is not the original input channel")
			}
			if got := len(input); got != 1 {
				t.Fatalf("buffered input count = %d, want 1; bypass read an event", got)
			}
			requireExactResponsesEvents(t, drainResponsesEvents(output), []llmkit.Event{first})
		})
	}
}

func TestGateResponsesPreamble_CancellationAfterCreatedReturns(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan llmkit.Event)
	type gateResult struct {
		output <-chan llmkit.Event
		err    error
	}
	result := make(chan gateResult, 1)
	go func() {
		output, err := gateResponsesPreamble(ctx, input, true, llmkit.ProtocolOpenAIResponses)
		result <- gateResult{output: output, err: err}
	}()

	created := llmkit.Event{
		Type: llmkit.EventStreamStart,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.created",
			Data:      `{"type":"response.created","sequence_number":0}`,
		},
	}
	select {
	case input <- created:
	case <-time.After(time.Second):
		t.Fatal("gate did not consume response.created")
	}
	cancel()

	select {
	case got := <-result:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("gate did not return promptly after context cancellation")
	}
}

func TestGateResponsesPreamble_EmptyClosedInputReturnsEmptyOutput(t *testing.T) {
	input := make(chan llmkit.Event)
	close(input)

	output, err := gateResponsesPreamble(
		context.Background(), input, true, llmkit.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatalf("gateResponsesPreamble() error = %v, want nil", err)
	}
	if output == nil {
		t.Fatal("output = nil, want a closed empty event channel")
	}
	if got := drainResponsesEvents(output); len(got) != 0 {
		t.Fatalf("output events = %#v, want empty", got)
	}
}

func TestGateResponsesPreamble_UnknownRawEventReleasesStream(t *testing.T) {
	created := llmkit.Event{
		Type: llmkit.EventStreamStart,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.created",
			Data:      `{"type":"response.created","sequence_number":0}`,
		},
	}
	unknownRaw := llmkit.Event{
		Type: llmkit.EventRawPassthrough,
		RawPassthrough: &llmkit.RawSSEEvent{
			EventName: "response.provider_warmup",
			Data:      `{"type":"response.provider_warmup","sequence_number":1}`,
		},
	}
	overloaded := llmkit.Event{
		Type: llmkit.EventError,
		Error: &llmkit.ErrorPayload{
			Code:    "server_is_overloaded",
			Message: "overloaded after unknown raw event",
		},
	}

	output, err := gateResponsesPreamble(
		context.Background(),
		closedResponsesEventChannel(created, unknownRaw, overloaded),
		true,
		llmkit.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatalf("gateResponsesPreamble() error = %v, want nil after unknown raw event", err)
	}
	requireExactResponsesEvents(t, drainResponsesEvents(output), []llmkit.Event{
		created,
		unknownRaw,
		overloaded,
	})
}

func TestGateResponsesPreamble_MalformedPreambleReleasesStream(t *testing.T) {
	tests := []struct {
		name     string
		preamble []llmkit.Event
	}{
		{
			name: "duplicate_created",
			preamble: []llmkit.Event{
				{
					Type:           llmkit.EventStreamStart,
					RawPassthrough: &llmkit.RawSSEEvent{EventName: "response.created", Data: `{"sequence_number":0}`},
				},
				{
					Type:           llmkit.EventStreamStart,
					RawPassthrough: &llmkit.RawSSEEvent{EventName: "response.created", Data: `{"sequence_number":1}`},
				},
			},
		},
		{
			name: "duplicate_in_progress",
			preamble: []llmkit.Event{
				{
					Type:           llmkit.EventStreamStart,
					RawPassthrough: &llmkit.RawSSEEvent{EventName: "response.created", Data: `{"sequence_number":0}`},
				},
				{
					Type:           llmkit.EventRawPassthrough,
					RawPassthrough: &llmkit.RawSSEEvent{EventName: "response.in_progress", Data: `{"sequence_number":1}`},
				},
				{
					Type:           llmkit.EventRawPassthrough,
					RawPassthrough: &llmkit.RawSSEEvent{EventName: "response.in_progress", Data: `{"sequence_number":2}`},
				},
			},
		},
		{
			name: "in_progress_before_created",
			preamble: []llmkit.Event{
				{
					Type:           llmkit.EventRawPassthrough,
					RawPassthrough: &llmkit.RawSSEEvent{EventName: "response.in_progress", Data: `{"sequence_number":0}`},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			overloaded := llmkit.Event{
				Type: llmkit.EventError,
				Error: &llmkit.ErrorPayload{
					Code:    "server_is_overloaded",
					Message: "overload after malformed preamble",
				},
			}
			input := append(append([]llmkit.Event(nil), test.preamble...), overloaded)

			output, err := gateResponsesPreamble(
				context.Background(),
				closedResponsesEventChannel(input...),
				true,
				llmkit.ProtocolOpenAIResponses,
			)
			if err != nil {
				t.Fatalf("gateResponsesPreamble() error = %v, want malformed preamble release", err)
			}
			requireExactResponsesEvents(t, drainResponsesEvents(output), input)
		})
	}
}

func closedResponsesEventChannel(events ...llmkit.Event) <-chan llmkit.Event {
	channel := make(chan llmkit.Event, len(events))
	for _, event := range events {
		channel <- event
	}
	close(channel)
	return channel
}

func drainResponsesEvents(events <-chan llmkit.Event) []llmkit.Event {
	var collected []llmkit.Event
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

func requireExactResponsesEvents(t *testing.T, got, want []llmkit.Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d; events = %#v", len(got), len(want), got)
	}
	for index := range want {
		if !reflect.DeepEqual(got[index], want[index]) {
			t.Fatalf("event[%d] = %#v, want %#v", index, got[index], want[index])
		}
		if got[index].Delta != want[index].Delta ||
			got[index].ToolCall != want[index].ToolCall ||
			got[index].Usage != want[index].Usage ||
			got[index].Error != want[index].Error ||
			got[index].RawPassthrough != want[index].RawPassthrough ||
			got[index].ContentBlockIndex != want[index].ContentBlockIndex ||
			got[index].Reasoning != want[index].Reasoning {
			t.Fatalf("event[%d] pointer identity changed: got %#v, want %#v", index, got[index], want[index])
		}
	}
}
