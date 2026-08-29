package llmkit_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	codec "github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestCodecCancellationClosesStreams(t *testing.T) {
	t.Run("encode response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan codec.Event)
		chunks, err := codec.NewCodec().EncodeResponse(ctx, codec.EncodeResponseInput{
			Protocol: codec.ProtocolOpenAIChat,
			Events:   events,
			Stream:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		assertChannelCloses(t, chunks)
		close(events)
	})

	t.Run("decode response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		body := newBlockingReadCloser()
		events, err := codec.NewCodec().DecodeResponse(ctx, codec.DecodeResponseInput{
			Protocol: codec.ProtocolOpenAIChat,
			Body:     body,
			Stream:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-body.started:
		case <-time.After(time.Second):
			t.Fatal("decoder did not start reading")
		}
		cancel()
		assertChannelCloses(t, events)
	})
}

func TestCodecCancellationPublishesInterruptedReasoning(t *testing.T) {
	tests := []struct {
		name     string
		protocol codec.Protocol
		stream   string
		delta    codec.EventType
	}{
		{
			name:     "claude",
			protocol: codec.ProtocolClaudeMessages,
			delta:    codec.EventReasoningContentDelta,
			stream: `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"partial"}}

`,
		},
		{
			name:     "responses",
			protocol: codec.ProtocolOpenAIResponses,
			delta:    codec.EventReasoningContentDelta,
			stream: `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress"}}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"delta":"partial"}

`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			reader, writer := io.Pipe()
			defer writer.Close()
			events, err := codec.NewCodec().DecodeResponse(ctx, codec.DecodeResponseInput{
				Protocol: tt.protocol,
				Body:     reader,
				Stream:   true,
			})
			if err != nil {
				t.Fatal(err)
			}
			written := make(chan error, 1)
			go func() {
				_, err := io.WriteString(writer, tt.stream)
				written <- err
			}()

			for {
				select {
				case event, ok := <-events:
					if !ok {
						t.Fatal("event stream closed before reasoning delta")
					}
					if event.Type == tt.delta {
						cancel()
						goto canceled
					}
				case <-time.After(time.Second):
					t.Fatal("reasoning delta was not published")
				}
			}

		canceled:
			if err := <-written; err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			var gotInterrupted bool
			for event := range events {
				if event.Type == codec.EventReasoningDone && event.ReasoningStatus == codec.ReasoningInterrupted {
					gotInterrupted = true
				}
				if event.Type == codec.EventDone {
					t.Fatalf("cancellation emitted normal EventDone: %#v", event)
				}
			}
			if !gotInterrupted {
				t.Fatal("cancellation did not publish interrupted ReasoningDone")
			}
		})
	}
}

func TestCodecCancellationClosesForNonCloserBlockingBody(t *testing.T) {
	for _, protocol := range []codec.Protocol{codec.ProtocolClaudeMessages, codec.ProtocolOpenAIResponses} {
		t.Run(string(protocol), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			body := newNonCloserBlockingReader()
			t.Cleanup(body.releaseRead)
			events, err := codec.NewCodec().DecodeResponse(ctx, codec.DecodeResponseInput{
				Protocol: protocol,
				Body:     body,
				Stream:   true,
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-body.started:
			case <-time.After(time.Second):
				t.Fatal("decoder did not start blocking read")
			}
			cancel()
			assertChannelCloses(t, events)
		})
	}
}

func assertChannelCloses[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-channel:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("channel did not close after cancellation")
		}
	}
}

type blockingReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

type nonCloserBlockingReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newNonCloserBlockingReader() *nonCloserBlockingReader {
	return &nonCloserBlockingReader{started: make(chan struct{}), release: make(chan struct{})}
}

func (reader *nonCloserBlockingReader) Read([]byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	<-reader.release
	return 0, io.EOF
}

func (reader *nonCloserBlockingReader) releaseRead() {
	select {
	case <-reader.release:
	default:
		close(reader.release)
	}
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (reader *blockingReadCloser) Read([]byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	<-reader.closed
	return 0, io.EOF
}

func (reader *blockingReadCloser) Close() error {
	select {
	case <-reader.closed:
	default:
		close(reader.closed)
	}
	return nil
}
