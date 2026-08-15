package sync

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	apiupstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestAPIProjectionOmitsPriceAndDerivesConsumesQuota(t *testing.T) {
	projector := NewAPIProjector(nil)
	tests := []struct {
		name  string
		price int64
		want  bool
	}{
		{name: "free", price: 0, want: false},
		{name: "paid", price: 1, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projector.ProjectService(models.APIService{
				ID: 7, Slug: "weather", Name: "Weather", PricePerCall: tt.price, Status: 1,
			})
			require.Equal(t, protocol.SyncedAPIService{
				ID: 7, Slug: "weather", Name: "Weather", ConsumesQuota: tt.want, Status: 1,
			}, got)
			body, err := json.Marshal(got)
			require.NoError(t, err)
			require.NotContains(t, string(body), "price")
		})
	}
}

func TestAPIProjectionCopiesRouteSlicesAndUpstreamMap(t *testing.T) {
	projector := NewAPIProjector(nil)
	protocols := datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP}
	methods := datatypes.JSONSlice[string]{"GET"}
	subprotocols := datatypes.JSONSlice[string]{"chat.v1"}
	route := models.APIRoute{
		ID: 2, APIServiceID: 1, BackendID: 9, Slug: "forecast", Protocols: protocols,
		AllowedMethods: methods, WebSocketSubprotocols: subprotocols,
		ExampleRequest: datatypes.NewJSONType(models.APIRequestExample{Method: "POST", Query: "operator-only"}),
	}
	gotRoute := projector.ProjectRoute(route)
	protocols[0] = models.APIProtocolWebSocket
	methods[0] = "POST"
	subprotocols[0] = "mutated"
	require.Equal(t, []string{"http"}, gotRoute.Protocols)
	require.Equal(t, []string{"GET"}, gotRoute.AllowedMethods)
	require.Equal(t, []string{"chat.v1"}, gotRoute.WebSocketSubprotocols)
	require.Equal(t, uint(9), gotRoute.BackendID)
	routeBody, err := json.Marshal(gotRoute)
	require.NoError(t, err)
	require.NotContains(t, string(routeBody), "example_request")

	headers := map[string]string{"X-Tenant": "one"}
	upstream := models.APIUpstream{ID: 3, BackendID: 9, AuthType: models.APIUpstreamAuthNone, HeaderOverride: datatypes.NewJSONType(headers)}
	gotUpstream, err := projector.ProjectUpstream(upstream)
	require.NoError(t, err)
	headers["X-Tenant"] = "two"
	require.Equal(t, "one", gotUpstream.HeaderOverride["X-Tenant"])
	require.Equal(t, uint(9), gotUpstream.BackendID)
	upstreamBody, err := json.Marshal(gotUpstream)
	require.NoError(t, err)
	require.NotContains(t, string(upstreamBody), "service_id")
}

func TestAPIProjectionDecryptsExecutionSecretsAndFailsClosed(t *testing.T) {
	cipher, err := byokcrypto.NewFromConfig("", "api-projection-test-secret")
	require.NoError(t, err)
	credential := protocol.APIUpstreamCredential{BearerToken: "secret-token"}
	credentialCiphertext, err := apiupstream.EncryptAPIUpstreamCredential(
		cipher, 41, models.APIUpstreamAuthBearer, credential,
	)
	require.NoError(t, err)
	sealedProxy, err := cipher.Seal("http://proxy.internal:8080", 41)
	require.NoError(t, err)

	row := models.APIUpstream{
		ID: 41, BackendID: 7, Name: "primary", BaseURL: "https://api.example.com",
		AuthType: models.APIUpstreamAuthBearer, CredentialCiphertext: credentialCiphertext,
		ProxyURLCiphertext: base64.StdEncoding.EncodeToString(sealedProxy), Priority: 2, Weight: 3, Status: 1,
	}
	got, err := NewAPIProjector(cipher).ProjectUpstream(row)
	require.NoError(t, err)
	require.Equal(t, credential, got.Credential)
	require.Equal(t, "http://proxy.internal:8080", got.ProxyURL)
	body, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(body), "ciphertext")
	require.NotContains(t, string(body), credentialCiphertext)

	badRows := []models.APIUpstream{
		{ID: 41, AuthType: models.APIUpstreamAuthBearer, CredentialCiphertext: "not-base64"},
		{ID: 42, AuthType: models.APIUpstreamAuthBearer, CredentialCiphertext: credentialCiphertext},
		{ID: 41, AuthType: models.APIUpstreamAuthBearer, CredentialCiphertext: credentialCiphertext, ProxyURLCiphertext: "not-base64"},
	}
	for _, bad := range badRows {
		_, err := NewAPIProjector(cipher).ProjectUpstream(bad)
		require.Error(t, err)
		require.NotContains(t, err.Error(), credentialCiphertext)
	}
	_, err = NewAPIProjector(nil).ProjectUpstream(row)
	require.Error(t, err)
}
