package genericapi

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/stretchr/testify/require"
)

type quotaFacts struct {
	user  *protocol.SyncedUser
	found bool
	err   error
	calls int
}

func (f *quotaFacts) FindUser(context.Context, uint) (*protocol.SyncedUser, bool, error) {
	f.calls++
	return f.user, f.found, f.err
}

type quotaSettings struct{ current settings.AgentSettings }

func (s *quotaSettings) Settings() settings.AgentSettings { return s.current }

func TestQuotaGateFreeAndSystemRequestsSkipUserLookup(t *testing.T) {
	for _, test := range []struct {
		name    string
		userID  uint
		service protocol.SyncedAPIService
	}{
		{name: "free", userID: 7, service: protocol.SyncedAPIService{ConsumesQuota: false}},
		{name: "system", userID: 0, service: protocol.SyncedAPIService{ConsumesQuota: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := &quotaFacts{err: errors.New("must not be called")}
			gate := NewQuotaGate(facts, &quotaSettings{})
			require.NoError(t, gate.Allow(context.Background(), test.userID, test.service))
			require.Zero(t, facts.calls)
		})
	}
}

func TestQuotaGateRequiresQuotaStrictlyAboveCurrentReserve(t *testing.T) {
	settingsFinder := &quotaSettings{current: settings.AgentSettings{MinQuotaReserve: 10}}
	for _, test := range []struct {
		name    string
		quota   int64
		wantErr error
	}{
		{name: "reserve plus one", quota: 11},
		{name: "equal reserve", quota: 10, wantErr: ErrInsufficientQuota},
		{name: "zero", quota: 0, wantErr: ErrInsufficientQuota},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := &quotaFacts{user: &protocol.SyncedUser{ID: 7, Quota: test.quota}, found: true}
			gate := NewQuotaGate(facts, settingsFinder)
			err := gate.Allow(context.Background(), 7, protocol.SyncedAPIService{ConsumesQuota: true})
			require.ErrorIs(t, err, test.wantErr)
			require.Equal(t, 1, facts.calls)
		})
	}
}

func TestQuotaGateMissingOrFailedUserFactIsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name  string
		facts *quotaFacts
	}{
		{name: "missing", facts: &quotaFacts{}},
		{name: "rpc error", facts: &quotaFacts{err: errors.New("rpc unavailable")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			gate := NewQuotaGate(test.facts, &quotaSettings{})
			require.ErrorIs(t, gate.Allow(context.Background(), 7, protocol.SyncedAPIService{ConsumesQuota: true}), ErrQuotaFactsUnavailable)
		})
	}
}
