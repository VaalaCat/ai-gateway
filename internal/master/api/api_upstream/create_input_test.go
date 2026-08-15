package api_upstream

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func TestBuildAPIUpstreamForCreateNormalizesAndValidatesCredentials(t *testing.T) {
	proxyURL := "http://proxy.example:3128"
	tests := []struct {
		name    string
		input   CreateInput
		wantErr bool
	}{
		{name: "defaults", input: CreateInput{Name: "origin", BaseURL: "https://origin.example"}},
		{name: "valid bearer", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthBearer, Credential: &APIUpstreamCredential{BearerToken: "bearer-secret"}, ProxyURL: &proxyURL, HeaderOverride: map[string]string{"X-Tenant": "west"}}},
		{name: "missing bearer", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthBearer}, wantErr: true},
		{name: "bearer field mismatch", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthBearer, Credential: &APIUpstreamCredential{QueryName: "key", QueryValue: "secret"}}, wantErr: true},
		{name: "missing header", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthHeader}, wantErr: true},
		{name: "header field mismatch", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthHeader, Credential: &APIUpstreamCredential{HeaderName: "X-Key", HeaderValue: "secret", BearerToken: "wrong"}}, wantErr: true},
		{name: "missing query", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthQuery}, wantErr: true},
		{name: "query field mismatch", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthQuery, Credential: &APIUpstreamCredential{QueryName: "key", QueryValue: "secret", BasicUsername: "wrong"}}, wantErr: true},
		{name: "missing basic", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthBasic}, wantErr: true},
		{name: "basic field mismatch", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthBasic, Credential: &APIUpstreamCredential{BasicUsername: "user", BasicPassword: "secret", HeaderName: "X-Wrong"}}, wantErr: true},
		{name: "unsafe header override", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", HeaderOverride: map[string]string{"Connection": "close"}}, wantErr: true},
		{name: "unsafe proxy URL", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", ProxyURL: stringPointer("://bad")}, wantErr: true},
		{name: "proxy userinfo", input: CreateInput{Name: "origin", BaseURL: "https://origin.example", ProxyURL: stringPointer("http://user:secret@proxy.example")}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := BuildAPIUpstreamForCreate(7, test.input)
			if test.wantErr {
				require.Error(t, err)
				_, createErr := (Creator{}).CreateInTx(dao.NewContext(app.NewApplication()), 7, test.input)
				require.EqualError(t, createErr, err.Error())
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint(7), got.BackendID)
			require.Equal(t, 1, got.Weight)
			wantAuth := test.input.AuthType
			if wantAuth == "" {
				wantAuth = models.APIUpstreamAuthNone
			}
			require.Equal(t, wantAuth, got.AuthType)
			require.Equal(t, 1, got.Status)
		})
	}
}

func stringPointer(value string) *string { return &value }
