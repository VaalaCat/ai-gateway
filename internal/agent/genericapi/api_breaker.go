package genericapi

import (
	"sort"
	"sync"

	"github.com/failsafe-go/failsafe-go/circuitbreaker"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

const apiBreakerSource state.ChannelSource = "api_upstream"

type APIBreakerSettingsReader interface {
	Settings() settings.AgentSettings
}

type APIStatusFailurePredicate func(status int) bool

type APIClientAbortReason string

const (
	APIClientAbortCanceled         APIClientAbortReason = "canceled"
	APIClientAbortDeadlineExceeded APIClientAbortReason = "deadline_exceeded"
)

// APIBreakerCompletion explicitly separates a client-originated abort from
// upstream transport errors. Err is never classified by error text or by
// context sentinels alone.
type APIBreakerCompletion struct {
	Result      *apiattempt.APIExecutionResult
	Err         error
	ClientAbort APIClientAbortReason
}

type APIBreakerPermit interface {
	Finish(completion APIBreakerCompletion)
}

type apiBreakerEntry struct {
	mu             sync.Mutex
	breaker        circuitbreaker.CircuitBreaker[state.AttemptResult]
	halfOpenActive bool
	deleted        bool
}

type APIBreakerRegistry struct {
	settings      APIBreakerSettingsReader
	registry      *resilience.Registry
	failureStatus APIStatusFailurePredicate

	mu      sync.Mutex
	entries map[uint]*apiBreakerEntry
}

func NewAPIBreakerRegistry(settings APIBreakerSettingsReader, failureStatus APIStatusFailurePredicate) *APIBreakerRegistry {
	if failureStatus == nil {
		failureStatus = defaultAPIStatusFailure
	}
	return &APIBreakerRegistry{
		settings: settings, registry: resilience.NewRegistry(), failureStatus: failureStatus,
		entries: make(map[uint]*apiBreakerEntry),
	}
}

// Healthy is a non-consuming health snapshot used before weighted selection.
// A concurrent state change may still make TryAcquire fail, in which case the
// request fails closed instead of selecting a second upstream.
func (r *APIBreakerRegistry) Healthy(upstreamID uint) bool {
	config, valid := r.config()
	if !valid || upstreamID == 0 {
		return false
	}
	if !config.BreakerEnabled {
		r.Clear()
		return true
	}
	entry := r.entry(upstreamID)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	breaker := r.currentBreakerLocked(entry, upstreamID, config)
	if breaker == nil {
		return false
	}
	switch {
	case breaker.IsClosed():
		return true
	case breaker.IsHalfOpen():
		return !entry.halfOpenActive
	default:
		return breaker.RemainingDelay() <= 0
	}
}

func (r *APIBreakerRegistry) TryAcquire(upstreamID uint) (APIBreakerPermit, bool) {
	config, valid := r.config()
	if !valid || upstreamID == 0 {
		return nil, false
	}
	if !config.BreakerEnabled {
		r.Clear()
		return &disabledAPIBreakerPermit{upstreamID: upstreamID}, true
	}
	entry := r.entry(upstreamID)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	breaker := r.currentBreakerLocked(entry, upstreamID, config)
	if breaker == nil || !breaker.TryAcquirePermit() {
		return nil, false
	}
	halfOpen := breaker.IsHalfOpen()
	if halfOpen {
		entry.halfOpenActive = true
	}
	return &apiBreakerPermit{
		registry: r, entry: entry, breaker: breaker, upstreamID: upstreamID, halfOpen: halfOpen,
	}, true
}

func (r *APIBreakerRegistry) entry(upstreamID uint) *apiBreakerEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.entries[upstreamID]; entry != nil {
		return entry
	}
	entry := &apiBreakerEntry{}
	r.entries[upstreamID] = entry
	return entry
}

func (r *APIBreakerRegistry) currentBreakerLocked(
	entry *apiBreakerEntry,
	upstreamID uint,
	config resilience.Config,
) circuitbreaker.CircuitBreaker[state.AttemptResult] {
	if entry.deleted {
		return nil
	}
	breaker := r.registry.Get(apiBreakerKey(upstreamID), config)
	if entry.breaker != breaker {
		entry.breaker = breaker
		entry.halfOpenActive = false
	}
	return breaker
}

type apiBreakerPermit struct {
	registry   *APIBreakerRegistry
	entry      *apiBreakerEntry
	breaker    circuitbreaker.CircuitBreaker[state.AttemptResult]
	upstreamID uint
	halfOpen   bool
	once       sync.Once
}

func (p *apiBreakerPermit) Finish(completion APIBreakerCompletion) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		stampAPIUpstreamID(completion.Result, p.upstreamID)
		p.entry.mu.Lock()
		defer p.entry.mu.Unlock()
		if p.halfOpen {
			p.entry.halfOpenActive = false
		}
		if p.entry.breaker != p.breaker || p.entry.deleted {
			return
		}
		if neutralAPIBreakerCompletion(completion) {
			if p.halfOpen && p.breaker.IsHalfOpen() {
				// failsafe has no neutral release. Re-open without recording a
				// failure so exactly one later probe is admitted after cooldown.
				p.breaker.Open()
			}
			return
		}
		if completion.Err != nil || p.registry.failureStatus(completion.Result.UpstreamStatus) {
			p.breaker.RecordFailure()
			return
		}
		p.breaker.RecordSuccess()
	})
}

type disabledAPIBreakerPermit struct {
	upstreamID uint
	once       sync.Once
}

func (p *disabledAPIBreakerPermit) Finish(completion APIBreakerCompletion) {
	if p == nil {
		return
	}
	p.once.Do(func() { stampAPIUpstreamID(completion.Result, p.upstreamID) })
}

func stampAPIUpstreamID(result *apiattempt.APIExecutionResult, upstreamID uint) {
	if result != nil {
		result.APIUpstreamID = upstreamID
	}
}

func neutralAPIBreakerCompletion(completion APIBreakerCompletion) bool {
	if completion.ClientAbort == APIClientAbortCanceled || completion.ClientAbort == APIClientAbortDeadlineExceeded {
		return true
	}
	if completion.Result == nil || !completion.Result.ProviderDispatchKnown || !completion.Result.ProviderDispatched {
		return true
	}
	return completion.Err == nil && completion.Result.UpstreamStatus == 0
}

func (r *APIBreakerRegistry) Delete(upstreamID uint) {
	if r == nil || r.registry == nil || upstreamID == 0 {
		return
	}
	r.mu.Lock()
	if entry := r.entries[upstreamID]; entry != nil {
		entry.mu.Lock()
		entry.deleted = true
		r.registry.Delete(apiBreakerKey(upstreamID))
		entry.mu.Unlock()
		delete(r.entries, upstreamID)
	} else {
		r.registry.Delete(apiBreakerKey(upstreamID))
	}
	r.mu.Unlock()
}

func (r *APIBreakerRegistry) Clear() {
	if r == nil || r.registry == nil {
		return
	}
	r.mu.Lock()
	for upstreamID, entry := range r.entries {
		entry.mu.Lock()
		entry.deleted = true
		r.registry.Delete(apiBreakerKey(upstreamID))
		entry.mu.Unlock()
		delete(r.entries, upstreamID)
	}
	r.mu.Unlock()
}

func (r *APIBreakerRegistry) config() (resilience.Config, bool) {
	if r == nil || r.settings == nil || r.registry == nil {
		return resilience.Config{}, false
	}
	value := r.settings.Settings()
	if (value.BreakerEnabled != 0 && value.BreakerEnabled != 1) || value.BreakerThreshold <= 0 || value.BreakerCooldownMs < 0 {
		return resilience.Config{}, false
	}
	return resilience.Config{
		BreakerEnabled: value.BreakerEnabled == 1, BreakerThreshold: value.BreakerThreshold,
		BreakerCooldownMs: value.BreakerCooldownMs, HalfOpenPermitLimit: 1,
	}, true
}

func apiBreakerKey(upstreamID uint) resilience.BreakerKey {
	return resilience.BreakerKey{Source: apiBreakerSource, ID: upstreamID}
}

func defaultAPIStatusFailure(status int) bool {
	return status == 429 || status >= 500 && status <= 599
}

type APIBreakerSnapshot struct {
	APIUpstreamID uint    `json:"api_upstream_id"`
	State         string  `json:"state"`
	RemainingMs   int64   `json:"remaining_ms"`
	Failures      int     `json:"failures"`
	Successes     int     `json:"successes"`
	FailureRate   float64 `json:"failure_rate"`
}

func (r *APIBreakerRegistry) SnapshotBreakers() []APIBreakerSnapshot {
	if r == nil || r.registry == nil {
		return []APIBreakerSnapshot{}
	}
	config, valid := r.config()
	if !valid || !config.BreakerEnabled {
		r.Clear()
		return []APIBreakerSnapshot{}
	}
	source := r.registry.SnapshotBreakers()
	result := make([]APIBreakerSnapshot, 0, len(source))
	for _, snapshot := range source {
		result = append(result, APIBreakerSnapshot{
			APIUpstreamID: snapshot.ChannelID, State: snapshot.State, RemainingMs: snapshot.RemainingMs,
			Failures: snapshot.Failures, Successes: snapshot.Successes, FailureRate: snapshot.FailureRate,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].APIUpstreamID < result[j].APIUpstreamID })
	return result
}
