package reporter

import (
	"context"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

var _ app.Reporter = (*Reporter)(nil)

type Reporter struct {
	bus           app.EventBus
	client        app.WSClient // nil-safe: if nil, logs are buffered but not sent
	logger        *zap.Logger
	buffer        []protocol.UsageLogEntry
	mu            sync.Mutex
	bufferSize    int
	flushInterval time.Duration
	agentID       string
	cancel        context.CancelFunc
}

func New(bus app.EventBus, client app.WSClient, logger *zap.Logger, bufferSize int, flushInterval time.Duration, agentID string) *Reporter {
	return &Reporter{
		bus:           bus,
		client:        client,
		logger:        logger,
		bufferSize:    bufferSize,
		flushInterval: flushInterval,
		agentID:       agentID,
	}
}

// Start subscribes to usage events and starts the flush loop
func (r *Reporter) Start(ctx context.Context) {
	ctx, r.cancel = context.WithCancel(ctx)

	// Subscribe to usage.completed events
	events.SubscribeUsageCompleted(r.bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		if entry.Timestamp == 0 {
			entry.Timestamp = time.Now().Unix()
		}
		r.mu.Lock()
		r.buffer = append(r.buffer, entry)
		shouldFlush := len(r.buffer) >= r.bufferSize
		r.mu.Unlock()

		if shouldFlush {
			r.flush()
		}
		return nil
	})

	// Start periodic flush loop
	go func() {
		ticker := time.NewTicker(r.flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				r.flush() // Final flush
				return
			case <-ticker.C:
				r.flush()
			}
		}
	}()
}

func (r *Reporter) flush() {
	r.mu.Lock()
	if len(r.buffer) == 0 {
		r.mu.Unlock()
		return
	}
	logs := r.buffer
	r.buffer = nil
	r.mu.Unlock()

	if r.client == nil {
		r.logger.Warn("no WS client, dropping usage logs", zap.Int("count", len(logs)))
		return
	}

	report := protocol.UsageReport{AgentID: r.agentID, Logs: logs}
	if err := r.client.Notify(consts.RPCReportUsage, report); err != nil {
		r.logger.Error("failed to report usage", zap.Error(err), zap.Int("count", len(logs)))
		// Put back into buffer
		r.mu.Lock()
		r.buffer = append(logs, r.buffer...)
		// Cap buffer to prevent unbounded growth
		if len(r.buffer) > r.bufferSize*10 {
			dropped := len(r.buffer) - r.bufferSize*10
			r.buffer = r.buffer[dropped:]
			r.logger.Warn("buffer overflow, dropped oldest entries", zap.Int("dropped", dropped))
		}
		r.mu.Unlock()
	} else {
		r.logger.Debug("reported usage", zap.Int("count", len(logs)))
	}
}

// Stop cancels the flush loop
func (r *Reporter) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// PendingCount returns the number of buffered entries
func (r *Reporter) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buffer)
}

// SetClient sets or replaces the WS client (e.g., after reconnection)
func (r *Reporter) SetClient(client app.WSClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.client = client
}
