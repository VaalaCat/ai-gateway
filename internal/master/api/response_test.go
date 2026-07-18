package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAcceptedResponseWrites202AndUnwrappedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/jobs", Adapt(NewAdapter(nil, nil, nil), BindNone,
		func(*app.Context, EmptyRequest) (Accepted[map[string]string], error) {
			return Accepted[map[string]string]{Body: map[string]string{"state": "queued"}}, nil
		},
	))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/jobs", nil))

	require.Equal(t, http.StatusAccepted, recorder.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, map[string]string{"state": "queued"}, body)
}

func TestAcceptedAndCreatedKeepIndependentStatusContracts(t *testing.T) {
	require.Equal(t, http.StatusAccepted, Accepted[string]{Body: "queued"}.HTTPStatus())
	require.Equal(t, "queued", Accepted[string]{Body: "queued"}.ResponseBody())
	require.Equal(t, http.StatusCreated, Created[string]{Value: "created"}.StatusCode())
	require.Equal(t, "created", Created[string]{Value: "created"}.Body())
}

type nilHTTPStatusResponse struct {
	status int
	body   any
}

func (r *nilHTTPStatusResponse) HTTPStatus() int   { return r.status }
func (r *nilHTTPStatusResponse) ResponseBody() any { return r.body }

type nilLegacyStatusResponse struct {
	status int
	body   any
}

func (r *nilLegacyStatusResponse) StatusCode() int { return r.status }
func (r *nilLegacyStatusResponse) Body() any       { return r.body }

func TestAdaptTypedNilResponsesWriteOKNullWithoutCallingMethods(t *testing.T) {
	tests := []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{
			name: "accepted pointer",
			handler: Adapt(NewAdapter(nil, nil, nil), BindNone,
				func(*app.Context, EmptyRequest) (*Accepted[string], error) { return nil, nil }),
		},
		{
			name: "custom http response pointer",
			handler: Adapt(NewAdapter(nil, nil, nil), BindNone,
				func(*app.Context, EmptyRequest) (*nilHTTPStatusResponse, error) { return nil, nil }),
		},
		{
			name: "legacy status and body pointer",
			handler: Adapt(NewAdapter(nil, nil, nil), BindNone,
				func(*app.Context, EmptyRequest) (*nilLegacyStatusResponse, error) { return nil, nil }),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/response", test.handler)
			recorder := httptest.NewRecorder()

			require.NotPanics(t, func() {
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/response", nil))
			})
			require.Equal(t, http.StatusOK, recorder.Code)
			require.JSONEq(t, "null", recorder.Body.String())
		})
	}
}

func TestAdaptValueResponsesStillUseStatusAndBodyContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/accepted", Adapt(NewAdapter(nil, nil, nil), BindNone,
		func(*app.Context, EmptyRequest) (Accepted[string], error) {
			return Accepted[string]{Body: "queued"}, nil
		}))
	router.GET("/created", Adapt(NewAdapter(nil, nil, nil), BindNone,
		func(*app.Context, EmptyRequest) (Created[string], error) {
			return Created[string]{Value: "created"}, nil
		}))

	accepted := httptest.NewRecorder()
	router.ServeHTTP(accepted, httptest.NewRequest(http.MethodGet, "/accepted", nil))
	require.Equal(t, http.StatusAccepted, accepted.Code)
	require.JSONEq(t, `"queued"`, accepted.Body.String())
	created := httptest.NewRecorder()
	router.ServeHTTP(created, httptest.NewRequest(http.MethodGet, "/created", nil))
	require.Equal(t, http.StatusCreated, created.Code)
	require.JSONEq(t, `"created"`, created.Body.String())
}
