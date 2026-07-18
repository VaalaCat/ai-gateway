package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type apiOperationFinder struct{ agent models.Agent }

func (f apiOperationFinder) FindAgent(_ context.Context, _ string) (models.Agent, error) {
	return f.agent, nil
}

type apiStrictOperationFinder struct {
	agent     models.Agent
	requested []string
}

func (f *apiStrictOperationFinder) FindAgent(_ context.Context, agentID string) (models.Agent, error) {
	f.requested = append(f.requested, agentID)
	if agentID != f.agent.AgentID {
		return models.Agent{}, errors.New("unexpected agent id")
	}
	return f.agent, nil
}

type apiOperationAuthorizer struct {
	agent      models.Agent
	operations []connectivity.Operation
	lease      connectivity.OperationLease
}

func (a *apiOperationAuthorizer) Authorize(agent models.Agent, operation connectivity.Operation) (connectivity.OperationLease, error) {
	a.agent = agent
	a.operations = append(a.operations, operation)
	return a.lease, nil
}

type apiOperationControl struct {
	agentID string
	method  string
	params  any
}

type apiOperationRelay struct {
	drains      []uint64
	disconnects []uint64
}

func (r *apiOperationRelay) Drain(_ string, generation uint64) error {
	r.drains = append(r.drains, generation)
	return nil
}

func (r *apiOperationRelay) Disconnect(_ string, generation uint64, _ error) error {
	r.disconnects = append(r.disconnects, generation)
	return nil
}

func (c *apiOperationControl) CallSessionContext(_ context.Context, agentID string, _ uint64, method string, params any, _ time.Duration) (json.RawMessage, error) {
	c.agentID, c.method, c.params = agentID, method, params
	return json.RawMessage(`{"state":"accepted"}`), nil
}

func operationContext(t *testing.T) *app.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return app.NewContext(ginContext, nil, zap.NewNop(), nil)
}

func TestOperationURIOverridesBodyAndReturnsAcceptedAck(t *testing.T) {
	db := setupTestDB(t)
	stored := models.Agent{AgentID: "canonical-agent", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&stored).Error)
	authorizer := &apiOperationAuthorizer{lease: connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7}}
	control := &apiOperationControl{}
	finder := &apiStrictOperationFinder{agent: stored}
	service := masteroperations.NewService(t.Context(), finder, masteroperations.Sources{
		Connections: authorizer, Control: control, Now: func() time.Time { return time.Unix(0, 1) },
	})
	handler := &Handler{Operations: service}

	response, err := handler.Operation(newTestContext(t, db), AgentOperationRequest{
		ID:     strconv.Itoa(int(stored.ID)),
		Action: string(connectivity.OperationInterrupt),
		OperationRequest: protocol.OperationRequest{
			AgentID: "body-agent", Operation: string(connectivity.OperationFullSync), RequestID: "41",
			ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
		},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, response.StatusCode())
	require.Equal(t, "accepted", response.OperationAck.State)
	require.Equal(t, []connectivity.Operation{connectivity.OperationInterrupt, connectivity.OperationInterrupt}, authorizer.operations)
	require.Equal(t, "canonical-agent", control.agentID)
	require.Equal(t, consts.RPCAgentOperation, control.method)
	forwarded := control.params.(protocol.OperationRequest)
	require.Equal(t, "canonical-agent", forwarded.AgentID)
	require.Equal(t, string(connectivity.OperationInterrupt), forwarded.Operation)
	require.NotEmpty(t, finder.requested)
	require.NotContains(t, finder.requested, strconv.Itoa(int(stored.ID)))
}

func TestOperationRejectsMissingDatabaseAgentBeforeDispatch(t *testing.T) {
	finder := &apiStrictOperationFinder{agent: models.Agent{AgentID: "canonical-agent", Status: consts.StatusEnabled}}
	service := masteroperations.NewService(t.Context(), finder, masteroperations.Sources{})
	handler := &Handler{Operations: service}

	_, err := handler.Operation(newTestContext(t, setupTestDB(t)), AgentOperationRequest{
		ID: "9999", Action: string(connectivity.OperationRelayReconnect),
		OperationRequest: protocol.OperationRequest{
			ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
		},
	})
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusNotFound, apiErr.Status)
	require.Empty(t, finder.requested)
}

func TestOperationErrorMappingUsesStableCodes(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid operation", err: masteroperations.ErrOperationInvalid, status: http.StatusBadRequest, code: "operation_invalid"},
		{name: "stale epoch", err: masteroperations.ErrSnapshotEpochChanged, status: http.StatusConflict, code: "snapshot_epoch_changed"},
		{name: "stale generation", err: connectivity.ErrConnectionGenerationChanged, status: http.StatusConflict, code: "connection_generation_changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiErr := operationAPIError(fmtOperationError(test.err), connectivity.OperationFullSync)
			require.Equal(t, test.status, apiErr.Status)
			require.Equal(t, test.code, apiErr.Code)
			require.NotContains(t, apiErr.Message, "secret detail")
		})
	}
}

func TestOperationRejectsUnavailableService(t *testing.T) {
	_, err := (&Handler{}).Operation(operationContext(t), AgentOperationRequest{})
	var apiErr *api.APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, http.StatusInternalServerError, apiErr.Status)
}

func TestOperationRequiresRelevantExpectedGeneration(t *testing.T) {
	service := masteroperations.NewService(t.Context(), apiOperationFinder{agent: models.Agent{
		AgentID: "canonical-agent", Status: consts.StatusEnabled,
	}}, masteroperations.Sources{})
	handler := &Handler{Operations: service}

	controlOperations := []connectivity.Operation{
		connectivity.OperationFullSync,
		connectivity.OperationProbe,
		connectivity.OperationRelayReconnect,
		connectivity.OperationDirectCircuitReset,
		connectivity.OperationInterrupt,
	}
	for _, operation := range controlOperations {
		t.Run(string(operation)+" requires control generation", func(t *testing.T) {
			_, err := handler.Operation(operationContext(t), AgentOperationRequest{
				ID: "uri-agent", Action: string(operation),
				OperationRequest: protocol.OperationRequest{
					Operation: string(connectivity.OperationRelayDrain), ExpectedEpoch: "master-a", ExpectedRelayGeneration: 11,
				},
			})
			apiErr := requireAPIError(t, err)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
			require.Equal(t, "operation_invalid", apiErr.Code)
		})
	}

	for _, operation := range []connectivity.Operation{connectivity.OperationRelayDrain, connectivity.OperationRelayDisconnect} {
		t.Run(string(operation)+" requires relay generation", func(t *testing.T) {
			_, err := handler.Operation(operationContext(t), AgentOperationRequest{
				ID: "uri-agent", Action: string(operation),
				OperationRequest: protocol.OperationRequest{
					ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
				},
			})
			apiErr := requireAPIError(t, err)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
			require.Equal(t, "operation_invalid", apiErr.Code)
		})
	}

	t.Run("unknown operation", func(t *testing.T) {
		_, err := handler.Operation(operationContext(t), AgentOperationRequest{
			ID: "uri-agent", Action: "unknown",
			OperationRequest: protocol.OperationRequest{
				ExpectedEpoch: "master-a", ExpectedControlGeneration: 7, ExpectedRelayGeneration: 11,
			},
		})
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusBadRequest, apiErr.Status)
		require.Equal(t, "operation_invalid", apiErr.Code)
	})
}

func TestOperationRelayMutationAllowsMissingControlGeneration(t *testing.T) {
	db := setupTestDB(t)
	stored := models.Agent{AgentID: "canonical-agent", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&stored).Error)
	authorizer := &apiOperationAuthorizer{lease: connectivity.OperationLease{SnapshotEpoch: "master-a", RelayGeneration: 11}}
	relay := &apiOperationRelay{}
	service := masteroperations.NewService(t.Context(), apiOperationFinder{agent: stored}, masteroperations.Sources{Connections: authorizer, Relay: relay})
	handler := &Handler{Operations: service}

	response, err := handler.Operation(newTestContext(t, db), AgentOperationRequest{
		ID: strconv.Itoa(int(stored.ID)), Action: string(connectivity.OperationRelayDrain),
		OperationRequest: protocol.OperationRequest{
			ExpectedEpoch: "master-a", ExpectedRelayGeneration: 11,
		},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, response.StatusCode())
	require.Equal(t, []uint64{11}, relay.drains)
}

func fmtOperationError(err error) error {
	return errors.Join(errors.New("secret detail"), err)
}
