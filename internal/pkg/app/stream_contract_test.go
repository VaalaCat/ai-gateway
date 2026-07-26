package app

import (
	"context"
	"io"
	"net/http"
	"testing"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type attemptStreamContractStub struct{}

func (attemptStreamContractStub) Commit(context.Context) error            { return nil }
func (attemptStreamContractStub) Upload(context.Context, io.Reader) error { return nil }
func (attemptStreamContractStub) CopyAttemptResponse(context.Context, http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	return attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}, nil
}
func (attemptStreamContractStub) CommitState() tunnel.CommitState { return tunnel.Committed }
func (attemptStreamContractStub) Cancel(error)                    {}
func (attemptStreamContractStub) Close() error                    { return nil }

type probeStreamContractStub struct{ attemptStreamContractStub }

func (probeStreamContractStub) CopyResponse(context.Context, http.ResponseWriter) error { return nil }

func TestTypedStreamContractsKeepAttemptAndProbeResultsSeparate(t *testing.T) {
	var attempt AttemptStream = attemptStreamContractStub{}
	var probe ProbeStream = probeStreamContractStub{}
	result, err := attempt.CopyAttemptResponse(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.NoError(t, probe.CopyResponse(t.Context(), nil))
}

func TestProbePolicyAliasesTunnelContract(t *testing.T) {
	var respect tunnel.ProbePolicy = ProbeRespectBusinessPolicy
	var bypass tunnel.ProbePolicy = ProbeBypassBusinessPolicy
	require.Equal(t, tunnel.ProbeRespectBusinessPolicy, respect)
	require.Equal(t, tunnel.ProbeBypassBusinessPolicy, bypass)
}
