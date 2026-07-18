package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/reporter"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/sourcegraph/conc"
	"go.uber.org/goleak"
	"go.uber.org/zap"
)

type lifecycleDirectBody struct{}

func (lifecycleDirectBody) Size() int64                  { return 0 }
func (lifecycleDirectBody) Open() (io.ReadCloser, error) { return http.NoBody, nil }
func (lifecycleDirectBody) Bytes(int64) ([]byte, error)  { return nil, nil }
func (lifecycleDirectBody) Close() error                 { return nil }

type blockingSubscribeBus struct {
	app.EventBus
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func TestShutdownCancelsInfiniteDirectSSEBeforeHTTPDrain(t *testing.T) {
	upstreamStarted := make(chan struct{})
	var startOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		startOnce.Do(func() { close(upstreamStarted) })
		<-r.Context().Done()
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	attempt := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	direct := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = direct.Forward(r.Context(), agentproxy.DirectRequest{
			TargetAgentID: "target", AddressFingerprint: "fp", TargetURL: target,
			Request: r, Body: lifecycleDirectBody{}, ForwardTicket: "ticket", Attempt: &attempt,
		}, w)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: handler}
	srv := &Server{directForwarder: direct, httpSrv: httpServer}
	srv.initLifecycle()
	var workers conc.WaitGroup
	workers.Go(func() { _ = httpServer.Serve(listener) })

	response, err := http.Post("http://"+listener.Addr().String(), "application/json", nil) //nolint:noctx -- lifecycle cancellation is the subject under test
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	<-upstreamStarted
	shutdownResult := make(chan error, 1)
	workers.Go(func() { shutdownResult <- srv.Shutdown(context.Background()) })
	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		direct.Cancel()
		_ = httpServer.Close()
		<-shutdownResult
		t.Fatal("Shutdown waited for an infinite direct SSE handler before cancelling direct forwarding")
	}
	workers.Wait()
	select {
	case <-direct.Done():
	default:
		t.Fatal("DirectForwarder.Done remained open when Server.Done closed")
	}
}

type blockingUsageSubscribeBus struct {
	app.EventBus
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type trackedSubscription struct {
	eventbus.Subscription
	once  sync.Once
	count *atomic.Int32
}

func (s *trackedSubscription) Unsubscribe() error {
	s.once.Do(func() { s.count.Add(1) })
	return s.Subscription.Unsubscribe()
}

type failingPatternSubscribeBus struct {
	app.EventBus
	failAt       int32
	calls        atomic.Int32
	unsubscribed atomic.Int32
	err          error
}

func (b *failingPatternSubscribeBus) SubscribePattern(pattern string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	if b.calls.Add(1) == b.failAt {
		return nil, b.err
	}
	sub, err := b.EventBus.SubscribePattern(pattern, handler)
	if err != nil {
		return nil, err
	}
	return &trackedSubscription{Subscription: sub, count: &b.unsubscribed}, nil
}

type failingUsageSubscribeBus struct {
	app.EventBus
	err error
}

func (b *failingUsageSubscribeBus) Subscribe(topic string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	if topic == events.UsageCompletedTopic.Value() {
		return nil, b.err
	}
	return b.EventBus.Subscribe(topic, handler)
}

func (b *blockingUsageSubscribeBus) Subscribe(topic string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	if topic == events.UsageCompletedTopic.Value() {
		b.once.Do(func() {
			close(b.entered)
			<-b.release
		})
	}
	return b.EventBus.Subscribe(topic, handler)
}

func (b *blockingSubscribeBus) SubscribePattern(pattern string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	b.calls.Add(1)
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return b.EventBus.SubscribePattern(pattern, handler)
}

func TestLifecycleShutdownBeforeRunClosesDone(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := &Server{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown before Run: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestLifecycleConcurrentShutdownIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := &Server{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var mu sync.Mutex
	var errs []error
	var workers conc.WaitGroup
	for range 8 {
		workers.Go(func() {
			err := srv.Shutdown(ctx)
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		})
	}
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Shutdown: %v", err)
		}
	}
	requireServerLifecycleDone(t, srv.Done())
}

func TestLifecycleShutdownRejectsNilContext(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := &Server{}
	if err := srv.Shutdown(nil); err == nil {
		t.Fatal("Shutdown(nil) = nil, want error")
	}
}

func TestPrepareBackgroundRejectsConcurrentStartupBeforeSecondSubscription(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	baseBus := eventbus.NewMemoryBus()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseStartup := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseStartup)
	bus := &blockingSubscribeBus{EventBus: baseBus, entered: entered, release: release}
	srv.Bus = bus

	firstDone := make(chan struct {
		background *PreparedBackground
		err        error
	}, 1)
	go func() {
		background, err := srv.PrepareBackground(context.Background())
		firstDone <- struct {
			background *PreparedBackground
			err        error
		}{background: background, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first PrepareBackground did not reach subscription barrier")
	}
	subscriptionsBefore := bus.calls.Load()

	second, err := srv.PrepareBackground(context.Background())
	if second != nil {
		second.Cancel(context.Canceled)
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second PrepareBackground = (%v, %v), want %v", second, err, ErrAlreadyRunning)
	}
	if subscriptionsAfter := bus.calls.Load(); subscriptionsAfter != subscriptionsBefore {
		t.Errorf("duplicate startup entered subscription boundary: before=%d after=%d", subscriptionsBefore, subscriptionsAfter)
	}

	releaseStartup()
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first PrepareBackground: %v", first.err)
	}
	first.background.Cancel(context.Canceled)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestPrepareRuntimeCommitFailureSnapshotsReasonBeforeConcurrentShutdown(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	stale, err := srv.beginStartup()
	if err != nil {
		t.Fatalf("first beginStartup: %v", err)
	}
	stale.Abort()
	current, err := srv.beginStartup()
	if err != nil {
		t.Fatalf("second beginStartup: %v", err)
	}
	failureSnapshotted := make(chan struct{})
	releaseFailure := make(chan struct{})
	srv.beforeStartupFailureReturn = func() {
		close(failureSnapshotted)
		<-releaseFailure
	}
	shutdownSnapshotted := make(chan struct{})
	srv.afterShutdownSnapshot = func() { close(shutdownSnapshotted) }
	prepareDone := make(chan error, 1)
	go func() {
		_, err := srv.prepareRuntime(context.Background(), stale, nil)
		prepareDone <- err
	}()
	select {
	case <-failureSnapshotted:
	case <-time.After(time.Second):
		t.Fatal("prepareRuntime did not snapshot commit failure")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(shutdownCtx) }()
	select {
	case <-shutdownSnapshotted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not enter while commit failure was pending")
	}
	close(releaseFailure)
	if err := <-prepareDone; !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("prepareRuntime commit failure = %v, want snapshotted %v", err, ErrAlreadyRunning)
	}
	current.Abort()
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestPrepareBackgroundParentCancellationAfterPhaseAReadyRollsBackCommittedRuntime(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	phaseAReady := make(chan struct{})
	srv.afterRuntimePhaseAReady = func(ctx context.Context) {
		close(phaseAReady)
		<-ctx.Done()
	}
	parent, cancelParent := context.WithCancelCause(context.Background())
	cause := errors.New("embedded parent canceled after phase A")
	prepareDone := make(chan struct {
		background *PreparedBackground
		err        error
	}, 1)
	go func() {
		background, err := srv.PrepareBackground(parent)
		prepareDone <- struct {
			background *PreparedBackground
			err        error
		}{background: background, err: err}
	}()
	select {
	case <-phaseAReady:
	case <-time.After(time.Second):
		t.Fatal("PrepareBackground did not reach post-phase-A barrier")
	}
	cancelParent(cause)
	result := <-prepareDone
	if result.background != nil {
		result.background.Cancel(context.Canceled)
		t.Fatal("canceled PrepareBackground returned an owner handle")
	}
	if !errors.Is(result.err, cause) {
		t.Fatalf("PrepareBackground error = %v, want %v", result.err, cause)
	}
	select {
	case <-srv.Done():
	case <-time.After(200 * time.Millisecond):
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if err := srv.Shutdown(cleanupCtx); err != nil {
			t.Fatalf("cleanup Shutdown: %v", err)
		}
		t.Fatal("committed runtime did not enter terminal startup rollback")
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after startup rollback = %+v", got)
	}
	if _, err := srv.Bus.Subscribe("after-close", func(context.Context, eventbus.Event) error { return nil }); !errors.Is(err, eventbus.ErrClosed) {
		t.Fatalf("owned bus remained open after startup rollback: %v", err)
	}
}

func TestRunCancellationAfterPhaseAReadyJoinsThroughShutdownOwner(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = "127.0.0.1:0"
	phaseAReady := make(chan struct{})
	srv.afterRuntimePhaseAReady = func(ctx context.Context) {
		close(phaseAReady)
		<-ctx.Done()
	}
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-phaseAReady:
	case <-time.After(time.Second):
		t.Fatal("Run did not reach post-phase-A barrier")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after Shutdown canceled its root context")
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestRunAdmissionRejectsBackgroundStartupBeforePublishingResources(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = "127.0.0.1:0"
	beforeRegister := make(chan struct{})
	releaseRegister := make(chan struct{})
	srv.beforeHTTPRegister = func() {
		close(beforeRegister)
		<-releaseRegister
	}

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-beforeRegister:
	case <-time.After(time.Second):
		t.Fatal("Run did not reach HTTP registration barrier")
	}
	background, backgroundErr := srv.PrepareBackground(context.Background())
	if background != nil {
		background.Cancel(context.Canceled)
	}
	close(releaseRegister)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	<-runDone
	if !errors.Is(backgroundErr, ErrAlreadyRunning) {
		t.Fatalf("background startup during Run = %v, want %v", backgroundErr, ErrAlreadyRunning)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestStartupAdmissionAbortCannotReleaseNewGeneration(t *testing.T) {
	srv := &Server{}
	first, err := srv.beginStartup()
	if err != nil {
		t.Fatalf("first beginStartup: %v", err)
	}
	first.Abort()
	second, err := srv.beginStartup()
	if err != nil {
		t.Fatalf("second beginStartup: %v", err)
	}
	(&startupLease{server: srv, generation: first.generation}).Abort()
	if third, err := srv.beginStartup(); third != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("beginStartup after stale Abort = (%v, %v), want %v", third, err, ErrAlreadyRunning)
	}
	second.Abort()
}

func TestShutdownDeadlineReturnsBeforePreparingLeaseJoins(t *testing.T) {
	srv := &Server{}
	lease, err := srv.beginStartup()
	if err != nil {
		t.Fatalf("beginStartup: %v", err)
	}
	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelDeadline()
	if err := srv.Shutdown(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown while startup lease held = %v, want deadline exceeded", err)
	}
	select {
	case <-srv.Done():
		t.Fatal("Server.Done closed before preparing startup lease joined")
	default:
	}
	lease.Abort()
	requireServerLifecycleDone(t, srv.Done())
	if srv.startupLease != nil {
		t.Fatal("joined startup lease remained referenced after Server.Done")
	}
	retryCtx, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	if err := srv.Shutdown(retryCtx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after startup lease join = %+v", got)
	}
}

func TestStartupAdmissionListenFailureAllowsRetry(t *testing.T) {
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = "127.0.0.1:not-a-port"
	if err := srv.Run(); err == nil {
		t.Fatal("Run with invalid listen address returned nil")
	}
	lease, err := srv.beginStartup()
	if err != nil {
		t.Fatalf("beginStartup after precommit failure: %v", err)
	}
	lease.Abort()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestCommittedBackgroundStartupCannotRestartAfterParentCancel(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	parent, cancelParent := context.WithCancelCause(context.Background())
	background, err := srv.PrepareBackground(parent)
	if err != nil {
		t.Fatalf("PrepareBackground: %v", err)
	}
	background.Commit()
	cancelParent(errors.New("embedded parent stopped"))
	background.Wait()
	if duplicate, err := srv.PrepareBackground(context.Background()); duplicate != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("PrepareBackground after committed parent cancel = (%v, %v), want %v", duplicate, err, ErrAlreadyRunning)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestLifecycleResourceCountsTrackRelayInflight(t *testing.T) {
	registry := inflight.NewRegistry(zap.NewNop(), 0)
	srv := &Server{Inflight: registry}
	if got := srv.ResourceCountsForTest().Inflight; got != 0 {
		t.Fatalf("Inflight before Track = %d, want 0", got)
	}
	entry := registry.Track(inflight.Meta{ReqID: "request-a"})
	if got := srv.ResourceCountsForTest().Inflight; got != 1 {
		t.Fatalf("Inflight after Track = %d, want 1", got)
	}
	entry.Done()
	if got := srv.ResourceCountsForTest().Inflight; got != 0 {
		t.Fatalf("Inflight after Done = %d, want 0", got)
	}
}

func serverLifecycleGoleakOptions() []goleak.Option {
	return []goleak.Option{
		goleak.IgnoreTopFunction("github.com/bytedance/gopkg/cache/asynccache.(*sharedTicker).tick"),
	}
}

func TestLifecycleFallbackDisabledRejectsNewRelayStream(t *testing.T) {
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	srv := &Server{
		Creds:  &enrollment.Credentials{AgentID: "target-a"},
		Store:  store,
		Router: gin.New(),
	}
	handler := srv.NewTunnelTargetHandler(nil)
	err := handler.ValidateOpen(wire.Open{
		Method: http.MethodPost, Path: "/v1/chat/completions", TargetAgentID: "target-a",
		ResponseWindow: 1,
	})
	if err == nil {
		t.Fatal("fallback disabled admitted a Relay Stream")
	}
}

func TestLifecycleEmbeddedAgentJoinsOwnedResources(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			MasterURL:       "http://127.0.0.1:1",
			CredentialsFile: filepath.Join(t.TempDir(), "agent.json"),
		},
		Runtime: config.RuntimeConfig{
			FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded", Secret: "secret"})
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	runDone := make(chan struct{})
	ready := make(chan struct{})
	srv.runBackgroundReady = func() { close(ready) }
	go func() {
		defer close(runDone)
		srv.RunBackground(context.Background())
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("RunBackground did not register all workers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after embedded Shutdown = %+v", got)
	}
	select {
	case <-srv.Reporter.Done():
	default:
		t.Fatal("Server.Done closed before Reporter.Done")
	}
	select {
	case <-runDone:
	case <-ctx.Done():
		t.Fatal("RunBackground remained blocked after Shutdown")
	}
}

func TestLifecycleShutdownDrainsHTTPBeforeCancelingRoot(t *testing.T) {
	srv := &Server{}
	root := srv.lifecycleContext()
	entered := make(chan struct{})
	release := make(chan struct{})
	handlerCanceled := make(chan error, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{
		BaseContext: func(net.Listener) context.Context { return root },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			select {
			case <-release:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
				handlerCanceled <- context.Cause(r.Context())
			}
		}),
	}
	srv.httpSrv = httpSrv
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = httpSrv.Serve(ln) }()
	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter handler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = srv.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	select {
	case err := <-handlerCanceled:
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatalf("handler cancel cause = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler remained active after graceful deadline")
	}
	select {
	case <-srv.Done():
	case <-time.After(time.Second):
		t.Fatal("Server.Done did not converge after deadline cancellation")
	}
	<-serveDone
	<-requestDone
}

func TestLifecycleShutdownDeadlineJoinsPendingReporter(t *testing.T) {
	requestEntered := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	releaseRequests := make(chan struct{})
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		requestEntered <- struct{}{}
		select {
		case <-req.Context().Done():
			select {
			case requestCanceled <- struct{}{}:
			default:
			}
		case <-releaseRequests:
		}
	}))
	defer master.Close()
	defer close(releaseRequests)
	store := reporter.NewMemPendingUsageStore(10, zap.NewNop())
	uploader, err := reporter.NewUsageUploader(reporter.UploaderConfig{
		Store: store, MasterURL: master.URL, AgentID: "agent-a", Secret: "secret",
		FlushInterval: time.Hour, BatchMax: 10, BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := eventbus.NewMemoryBus()
	rep := reporter.New(bus, zap.NewNop(), store, uploader, nil)
	srv := &Server{Reporter: rep}
	root := srv.lifecycleContext()
	rep.Start(root)
	store.Append([]protocol.UsageLogEntry{{RequestID: "pending"}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(ctx) }()
	<-requestEntered
	if got := srv.ResourceCountsForTest().Inflight; got != 1 {
		t.Fatalf("Inflight during reporter final drain = %d, want 1", got)
	}
	if err := <-shutdownDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("pending reporter request did not observe shutdown deadline")
	}
	select {
	case <-srv.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Server.Done did not converge after reporter deadline")
	}
	select {
	case <-rep.Done():
	default:
		t.Fatal("Server.Done closed before Reporter.Done")
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestLifecycleShutdownWinsHTTPRegistrationWindow(t *testing.T) {
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = "127.0.0.1:0"
	beforeRegister := make(chan struct{})
	releaseRegister := make(chan struct{})
	shutdownSnapshotted := make(chan struct{})
	srv.beforeHTTPRegister = func() {
		close(beforeRegister)
		<-releaseRegister
	}
	srv.afterShutdownSnapshot = func() { close(shutdownSnapshotted) }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-beforeRegister:
	case <-time.After(time.Second):
		t.Fatal("Run did not reach HTTP registration barrier")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(ctx) }()
	select {
	case <-shutdownSnapshotted:
	case <-ctx.Done():
		t.Fatal("Shutdown did not snapshot lifecycle resources")
	}
	select {
	case <-srv.Done():
		t.Fatal("Server.Done closed while Run still owned a startup lease")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRegister)

	if err := <-runDone; err == nil {
		t.Fatal("Run registered HTTP resources after shutdown")
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if srv.Listener != nil || srv.httpSrv != nil {
		t.Fatal("rejected HTTP resources remained registered")
	}
}

func TestRunListenFailureDoesNotStartLifecycleResources(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	tests := []struct {
		name       string
		listenAddr func(t *testing.T) string
	}{
		{
			name: "port conflict",
			listenAddr: func(t *testing.T) string {
				t.Helper()
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = occupied.Close() })
				return occupied.Addr().String()
			},
		},
		{
			name: "invalid address",
			listenAddr: func(t *testing.T) string {
				t.Helper()
				return "127.0.0.1:not-a-port"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newLifecycleEmbeddedServer(t)
			srv.Router = gin.New()
			srv.Cfg.Agent.Listen = tt.listenAddr(t)
			before := srv.ResourceCountsForTest()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			defer func() { _ = srv.Shutdown(shutdownCtx) }()

			if err := srv.Run(); err == nil {
				t.Fatal("Run returned nil for listen failure")
			}
			if srv.Syncer != nil || srv.Reporter != nil || srv.Listener != nil || srv.httpSrv != nil || srv.getClient() != nil {
				t.Fatal("listen failure published lifecycle resources")
			}
			if after := srv.ResourceCountsForTest(); after != before {
				t.Fatalf("resources changed across listen failure: before=%+v after=%+v", before, after)
			}
			select {
			case <-srv.Done():
				t.Fatal("listen failure closed Server.Done before explicit Shutdown")
			default:
			}

			if err := srv.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("first Shutdown: %v", err)
			}
			if err := srv.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("second Shutdown: %v", err)
			}
			requireServerLifecycleDone(t, srv.Done())
			if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
				t.Fatalf("resources after repeated Shutdown = %+v", got)
			}
		})
	}
}

func TestRunRegistrationFailureRollsBackPreparedResources(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = listenAddr
	before := srv.ResourceCountsForTest()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	defer func() { _ = srv.Shutdown(shutdownCtx) }()

	srv.beforeHTTPRegister = func() {
		srv.lifecycleMu.Lock()
		srv.closing = true
		srv.lifecycleMu.Unlock()
	}
	if err := srv.Run(); !errors.Is(err, errAgentServerClosing) {
		t.Fatalf("Run = %v, want %v", err, errAgentServerClosing)
	}
	if srv.Syncer != nil || srv.Reporter != nil || srv.Listener != nil || srv.httpSrv != nil || srv.getClient() != nil {
		t.Fatal("registration failure published prepared resources")
	}
	if after := srv.ResourceCountsForTest(); after != before {
		t.Fatalf("resources changed across registration failure: before=%+v after=%+v", before, after)
	}
	rebound, err := net.Listen("tcp", listenAddr)
	if err != nil {
		t.Fatalf("prepared listener remained open: %v", err)
	}
	_ = rebound.Close()
	select {
	case <-srv.Done():
		t.Fatal("startup rollback closed Server.Done before explicit Shutdown")
	default:
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after repeated Shutdown = %+v", got)
	}
}

func TestShutdownWaitsForPublishedRunStartupToReleaseRegistration(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = "127.0.0.1:0"
	baseBus := eventbus.NewMemoryBus()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSubscribe := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseSubscribe()
	srv.Bus = &blockingSubscribeBus{EventBus: baseBus, entered: entered, release: release}
	shutdownSnapshotted := make(chan struct{})
	srv.afterShutdownSnapshot = func() { close(shutdownSnapshotted) }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Run did not reach subscription barrier after publishing resources")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(ctx) }()
	select {
	case <-shutdownSnapshotted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not begin while startup subscription was blocked")
	}
	if err := <-shutdownDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline while startup worker is blocked", err)
	}
	select {
	case <-srv.Done():
		t.Fatal("Server.Done closed before blocked startup worker was released")
	default:
	}

	releaseSubscribe()
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after concurrent Shutdown")
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after startup/shutdown interleave = %+v", got)
	}
}

func TestRunDoesNotServeFirstUsageRequestBeforeReporterSubscriptionReady(t *testing.T) {
	testRunDoesNotServeFirstUsageRequestBeforeStartupReady(t, "Reporter", func(base app.EventBus, entered, release chan struct{}) app.EventBus {
		return &blockingUsageSubscribeBus{EventBus: base, entered: entered, release: release}
	})
}

func TestRunDoesNotServeFirstUsageRequestBeforeSyncSubscriptionsReady(t *testing.T) {
	testRunDoesNotServeFirstUsageRequestBeforeStartupReady(t, "Syncer", func(base app.EventBus, entered, release chan struct{}) app.EventBus {
		return &blockingSubscribeBus{EventBus: base, entered: entered, release: release}
	})
}

func TestRunDoesNotStartControlPhaseBeforeSyncSubscriptionsReady(t *testing.T) {
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	srv.Cfg.Agent.Listen = "127.0.0.1:0"
	baseBus := eventbus.NewMemoryBus()
	subscribeEntered := make(chan struct{})
	releaseSubscribe := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSubscribe) }) }
	t.Cleanup(release)
	srv.Bus = &blockingSubscribeBus{EventBus: baseBus, entered: subscribeEntered, release: releaseSubscribe}

	controlEntered := make(chan error, 1)
	var controlOnce sync.Once
	srv.beforeConnectLoop = func() {
		controlOnce.Do(func() {
			push := protocol.SyncPushParams{
				Entity: events.EntityChannel, Action: events.ActionCreate,
				Data: []byte(`{"id":987,"name":"first-control-push"}`), Version: 1,
			}
			controlEntered <- events.PublishSyncEvent(context.Background(), srv.Bus, push.Entity, push.Action, push)
		})
	}

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-subscribeEntered:
	case <-time.After(time.Second):
		t.Fatal("Syncer did not reach subscription barrier")
	}
	startedBeforeReady := false
	select {
	case err := <-controlEntered:
		startedBeforeReady = true
		if err != nil {
			t.Fatalf("early control push: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if !startedBeforeReady {
		select {
		case err := <-controlEntered:
			if err != nil {
				t.Fatalf("control push after readiness: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("control phase did not start after subscriptions became ready")
		}
	}
	if startedBeforeReady {
		t.Error("connectLoop started before Syncer subscriptions became ready")
	}
	if got := srv.Store.GetChannel(987); got == nil || got.Name != "first-control-push" {
		t.Errorf("first control push was not applied after startup: %+v", got)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after Shutdown")
	}
}

func TestRunStartupSubscriptionErrorsCloseCommittedResources(t *testing.T) {
	tests := []struct {
		name string
		bus  func(app.EventBus, error) app.EventBus
	}{
		{
			name: "Reporter usage subscription",
			bus: func(base app.EventBus, err error) app.EventBus {
				return &failingUsageSubscribeBus{EventBus: base, err: err}
			},
		},
		{
			name: "Syncer pattern subscription with rollback",
			bus: func(base app.EventBus, err error) app.EventBus {
				return &failingPatternSubscribeBus{EventBus: base, failAt: 3, err: err}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newLifecycleEmbeddedServer(t)
			srv.Router = gin.New()
			srv.Cfg.Agent.Listen = "127.0.0.1:0"
			phaseBStarted := make(chan struct{}, 1)
			srv.beforeConnectLoop = func() { phaseBStarted <- struct{}{} }
			baseBus := eventbus.NewMemoryBus()
			startupErr := errors.New("startup subscribe failed")
			srv.Bus = tt.bus(baseBus, startupErr)

			if err := srv.Run(); !errors.Is(err, startupErr) {
				t.Fatalf("Run error = %v, want %v", err, startupErr)
			}
			requireServerLifecycleDone(t, srv.Done())
			if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
				t.Fatalf("resources after startup error = %+v", got)
			}
			select {
			case <-phaseBStarted:
				t.Fatal("phase B started after phase A subscription error")
			default:
			}
			if _, err := baseBus.Subscribe("after-close", func(context.Context, eventbus.Event) error { return nil }); !errors.Is(err, eventbus.ErrClosed) {
				t.Fatalf("owned bus remained open after startup error: %v", err)
			}
			if bus, ok := srv.Bus.(*failingPatternSubscribeBus); ok {
				if got := bus.unsubscribed.Load(); got != bus.failAt-1 {
					t.Fatalf("rolled back pattern subscriptions = %d, want %d", got, bus.failAt-1)
				}
			}
		})
	}
}

func TestWaitForRunStartupHonorsResultsErrorsAndCancellation(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		results := make(chan error, 2)
		results <- nil
		results <- nil
		if err := waitForRunStartup(context.Background(), results, 2); err != nil {
			t.Fatalf("waitForRunStartup: %v", err)
		}
	})
	t.Run("startup error", func(t *testing.T) {
		startupErr := errors.New("startup failed")
		results := make(chan error, 1)
		results <- startupErr
		if err := waitForRunStartup(context.Background(), results, 1); !errors.Is(err, startupErr) {
			t.Fatalf("waitForRunStartup error = %v, want %v", err, startupErr)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		cause := errors.New("shutdown")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		if err := waitForRunStartup(ctx, make(chan error), 1); !errors.Is(err, cause) {
			t.Fatalf("waitForRunStartup error = %v, want %v", err, cause)
		}
	})
	t.Run("ready and canceled simultaneously", func(t *testing.T) {
		cause := errors.New("shutdown wins ready")
		for range 1000 {
			ctx, cancel := context.WithCancelCause(context.Background())
			results := make(chan error, 1)
			results <- nil
			cancel(cause)
			if err := waitForRunStartup(ctx, results, 1); !errors.Is(err, cause) {
				t.Fatalf("waitForRunStartup error = %v, want %v", err, cause)
			}
		}
	})
}

func testRunDoesNotServeFirstUsageRequestBeforeStartupReady(
	t *testing.T,
	dependency string,
	block func(app.EventBus, chan struct{}, chan struct{}) app.EventBus,
) {
	t.Helper()
	defer goleak.VerifyNone(t, serverLifecycleGoleakOptions()...)
	srv := newLifecycleEmbeddedServer(t)
	srv.Cfg.Agent.Listen = "127.0.0.1:0"
	baseBus := eventbus.NewMemoryBus()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSubscription := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseSubscription)
	srv.Bus = block(baseBus, entered, release)
	srv.Router = gin.New()
	handlerEntered := make(chan error, 1)
	srv.Router.GET("/first-usage", func(c *gin.Context) {
		err := events.PublishUsageCompleted(c.Request.Context(), srv.Bus, protocol.UsageLogEntry{RequestID: "first-request"})
		handlerEntered <- err
		c.Status(http.StatusNoContent)
	})

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("%s did not reach subscription barrier", dependency)
	}

	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Listener.Addr().String() + "/first-usage")
		if err == nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	servedBeforeReady := false
	select {
	case err := <-handlerEntered:
		servedBeforeReady = true
		if err != nil {
			t.Errorf("early request usage publish: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
	}
	releaseSubscription()
	if !servedBeforeReady {
		select {
		case err := <-handlerEntered:
			if err != nil {
				t.Fatalf("request usage publish: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("first request did not enter after subscription became ready")
		}
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("first request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not complete")
	}
	if servedBeforeReady {
		t.Errorf("Run served the first request before %s installed subscriptions", dependency)
	}
	if got := srv.Reporter.PendingCount(); got != 1 {
		t.Errorf("Reporter.PendingCount() = %d, want first request usage retained", got)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after Shutdown")
	}
}

func TestRunAfterCommitRejectsSimultaneousReadyAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errAgentServerClosing)
	ready := make(chan struct{})
	close(ready)
	ran := make(chan struct{}, 1)
	for range 100 {
		runAfterCommit(ctx, ready, func() { ran <- struct{}{} })
	}
	select {
	case <-ran:
		t.Fatal("startup worker ran after owner context was canceled")
	default:
	}
}

func TestLifecycleRunAfterDoneFailsClosed(t *testing.T) {
	srv := newLifecycleEmbeddedServer(t)
	srv.Router = gin.New()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := srv.Run(); err == nil {
		t.Fatal("Run after Done returned nil")
	}
	if srv.Syncer != nil || srv.Reporter != nil || srv.Listener != nil || srv.httpSrv != nil {
		t.Fatal("Run after Done created lifecycle resources")
	}
}

func TestLifecycleRunBackgroundAfterDoneFailsClosed(t *testing.T) {
	srv := newLifecycleEmbeddedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		srv.RunBackground(context.Background())
	}()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("RunBackground after Done remained blocked")
	}
	if srv.Syncer != nil || srv.Reporter != nil {
		t.Fatal("RunBackground after Done created lifecycle resources")
	}
}

func newLifecycleEmbeddedServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			MasterURL:       "http://127.0.0.1:1",
			CredentialsFile: filepath.Join(t.TempDir(), "agent.json"),
		},
		Runtime: config.RuntimeConfig{
			FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded", Secret: "secret"})
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	return srv
}

func requireServerLifecycleDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Server.Done did not close")
	}
}
