package loaders

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestAPIRoleSetLoaderClassifiesPositiveEmptyMissingAndFailure(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		newLoader   func(*stubWSClient) entitycache.Loader[uint, *protocol.APIRoleSet]
		result      protocol.APIRoleSetFetchResult
		responseErr error
		wantErr     error
	}{
		{
			name: "user positive empty", method: consts.RPCSyncFetchUserAPIRoles,
			newLoader: func(client *stubWSClient) entitycache.Loader[uint, *protocol.APIRoleSet] {
				return &UserAPIRoleSetLoader{Client: client}
			},
			result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{}}},
		},
		{
			name: "token missing", method: consts.RPCSyncFetchTokenAPIRoles,
			newLoader: func(client *stubWSClient) entitycache.Loader[uint, *protocol.APIRoleSet] {
				return &TokenAPIRoleSetLoader{Client: client}
			},
			result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: false}, wantErr: entitycache.ErrNotFound,
		},
		{
			name: "rpc error is not missing", method: consts.RPCSyncFetchTokenAPIRoles,
			newLoader: func(client *stubWSClient) entitycache.Loader[uint, *protocol.APIRoleSet] {
				return &TokenAPIRoleSetLoader{Client: client}
			},
			responseErr: errors.New("rpc unavailable"), wantErr: errors.New("rpc unavailable"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &stubWSClient{respond: func(method string, params any) (json.RawMessage, error) {
				require.Equal(t, test.method, method)
				require.Equal(t, protocol.APIRoleSetFetchRequest{PrincipalID: 7}, params)
				if test.responseErr != nil {
					return nil, test.responseErr
				}
				return json.Marshal(test.result)
			}}
			got, err := test.newLoader(client).Load(context.Background(), 7)
			if test.wantErr != nil {
				require.ErrorContains(t, err, test.wantErr.Error())
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got.RoleIDs)
			require.Empty(t, got.RoleIDs)
		})
	}
}

func TestAPIRoleSetLoaderRejectsPrincipalMismatch(t *testing.T) {
	client := &stubWSClient{respond: func(string, any) (json.RawMessage, error) {
		return json.Marshal(protocol.APIRoleSetFetchResult{PrincipalID: 8, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{1}}})
	}}
	_, err := (&UserAPIRoleSetLoader{Client: client}).Load(context.Background(), 7)
	require.ErrorContains(t, err, "principal")
	require.NotErrorIs(t, err, entitycache.ErrNotFound)
}
