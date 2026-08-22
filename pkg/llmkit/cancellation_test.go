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
