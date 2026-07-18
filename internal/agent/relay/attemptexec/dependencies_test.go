package attemptexec

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/stretchr/testify/require"
)

type dependencyProvider struct{}

func (*dependencyProvider) Execute(*state.RelayContext, state.Attempt) ProviderResult {
	return ProviderResult{}
}

type dependencyDispatcher struct{}

func (*dependencyDispatcher) Dispatch(*state.RelayContext, state.Attempt) state.AttemptResult {
	return state.AttemptResult{}
}

func TestProviderExecutorAvailableValidatesDependencies(t *testing.T) {
	var typedNilProvider *dependencyProvider
	var typedNilExecutor *Executor
	var typedNilDispatcher *dependencyDispatcher
	tests := []struct {
		name     string
		provider ProviderAttemptExecutor
		want     bool
	}{
		{name: "nil provider", provider: nil, want: false},
		{name: "typed nil custom provider", provider: typedNilProvider, want: false},
		{name: "typed nil built-in executor", provider: typedNilExecutor, want: false},
		{name: "built-in executor without dispatcher", provider: NewProviderExecutor(nil, nil, nil), want: false},
		{name: "built-in executor with typed nil dispatcher", provider: NewProviderExecutor(typedNilDispatcher, nil, nil), want: false},
		{name: "custom provider", provider: &dependencyProvider{}, want: true},
		{name: "built-in executor", provider: NewProviderExecutor(&dependencyDispatcher{}, nil, nil), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ProviderExecutorAvailable(tt.provider))
		})
	}
}
