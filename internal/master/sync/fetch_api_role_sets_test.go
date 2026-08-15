package sync

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRoleSetFetchReturnsNotFoundSeparatelyFromBackendFailure(t *testing.T) {
	q, m := setupSyncDB(t)
	fetcher := APIRoleSetFetcher{}

	missing, err := fetcher.FetchUser(context.Background(), q, 404)
	require.NoError(t, err)
	require.Equal(t, protocol.APIRoleSetFetchResult{
		PrincipalID: 404, Exists: false, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{}},
	}, missing)

	require.NoError(t, m.User().Create(&models.User{
		Username: "role-fetch", Password: "x", GroupID: 1, Status: consts.StatusEnabled,
	}))
	user, err := q.User().GetByUsername("role-fetch")
	require.NoError(t, err)
	positiveEmpty, err := fetcher.FetchUser(context.Background(), q, user.ID)
	require.NoError(t, err)
	require.True(t, positiveEmpty.Exists)
	require.Empty(t, positiveEmpty.RoleSet.RoleIDs)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetcher.FetchUser(canceled, q, user.ID)
	require.Error(t, err)
}

// Break caught: putting API RoleSets in the token side payload makes the LLM
// token hot path wait for API-RBAC queries even though only generic invoke uses them.
func TestTokenFetchSideNeverQueriesOrSerializesAPIRoleSets(t *testing.T) {
	q, m, db := setupSyncDBWithDatabase(t)
	require.NoError(t, m.User().Create(&models.User{
		Username: "token-side", Password: "x", GroupID: 1, Status: consts.StatusEnabled,
	}))
	user, err := q.User().GetByUsername("token-side")
	require.NoError(t, err)
	token := &models.Token{
		Key: "sk-api-side", Name: "api-side", UserID: user.ID,
		APIRoleMode: models.APIRoleModeExplicit, Status: consts.StatusEnabled,
	}
	require.NoError(t, m.Token().Create(token))
	roleBindingQueries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:count-token-fetch-role-binding-queries",
		func(tx *gorm.DB) {
			if tx.Statement.Table == "role_bindings" {
				roleBindingQueries++
			}
		},
	))

	_, sideData, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, token.Key)
	require.NoError(t, err)
	require.True(t, found)
	var side protocol.TokenFetchSide
	require.NoError(t, json.Unmarshal(sideData, &side))
	require.NotNil(t, side.User)
	require.NotNil(t, side.TokenRoutings)
	require.Zero(t, roleBindingQueries)
	var sideFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sideData, &sideFields))
	require.NotContains(t, sideFields, "user_api_role_set")
	require.NotContains(t, sideFields, "token_api_role_set")
}

func TestUserFetchSeparatesNotFoundFromContextFailure(t *testing.T) {
	q, m := setupSyncDB(t)
	_, _, found, err := userFetchHandler{}.Fetch(context.Background(), q, "404")
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, m.User().Create(&models.User{
		Username: "fetch-context", Password: "x", GroupID: 1, Status: consts.StatusEnabled,
	}))
	user, err := q.User().GetByUsername("fetch-context")
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, found, err = userFetchHandler{}.Fetch(canceled, q, strconv.FormatUint(uint64(user.ID), 10))
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, found)
}

func TestTokenFetchRejectsMissingRequiredFacts(t *testing.T) {
	t.Run("missing owner", func(t *testing.T) {
		q, _, db := setupSyncDBWithDatabase(t)
		token := models.Token{
			Key: "sk-missing-owner", Name: "missing", UserID: 999,
			APIRoleMode: models.APIRoleModeExplicit, Status: consts.StatusEnabled,
		}
		require.NoError(t, db.Create(&token).Error)
		_, _, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, token.Key)
		require.Error(t, err)
		require.False(t, found)
	})

	t.Run("routing backend", func(t *testing.T) {
		q, m, db := setupSyncDBWithDatabase(t)
		require.NoError(t, m.User().Create(&models.User{
			Username: "failed-routing", Password: "x", GroupID: 1, Status: consts.StatusEnabled,
		}))
		user, err := q.User().GetByUsername("failed-routing")
		require.NoError(t, err)
		token := &models.Token{
			Key: "sk-failed-routing", Name: "failed", UserID: user.ID,
			APIRoleMode: models.APIRoleModeExplicit, Status: consts.StatusEnabled,
		}
		require.NoError(t, m.Token().Create(token))
		require.NoError(t, db.Migrator().DropTable(&models.ModelRouting{}))
		_, _, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, token.Key)
		require.Error(t, err)
		require.False(t, found)
	})
}

// Break caught: coupling the optional API RoleSet prewarm to token loading
// turns an API-RBAC-only outage into an LLM authentication outage.
func TestTokenFetchIgnoresAPIRoleSetBackendFailure(t *testing.T) {
	for _, mode := range []models.APIRoleMode{
		models.APIRoleModeInherit,
		models.APIRoleModeExplicit,
	} {
		t.Run(string(mode), func(t *testing.T) {
			q, m, db := setupSyncDBWithDatabase(t)
			require.NoError(t, m.User().Create(&models.User{
				Username: "isolated-" + string(mode), Password: "x", GroupID: 1, Status: consts.StatusEnabled,
			}))
			user, err := q.User().GetByUsername("isolated-" + string(mode))
			require.NoError(t, err)
			token := &models.Token{
				Key: "sk-isolated-" + string(mode), Name: "isolated", UserID: user.ID,
				APIRoleMode: mode, Status: consts.StatusEnabled,
			}
			require.NoError(t, m.Token().Create(token))
			require.NoError(t, db.Migrator().DropTable(&models.RoleBinding{}))

			data, sideData, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, token.Key)
			require.NoError(t, err)
			require.True(t, found)
			var fetched models.Token
			require.NoError(t, json.Unmarshal(data, &fetched))
			require.Equal(t, token.ID, fetched.ID)
			var side protocol.TokenFetchSide
			require.NoError(t, json.Unmarshal(sideData, &side))
			require.NotNil(t, side.User)
			require.Equal(t, user.ID, side.User.ID)
			require.NotNil(t, side.TokenRoutings)
		})
	}
}

// Break caught: removing RoleSet prewarm must not also remove the token fetch's
// own caller-cancellation boundary.
func TestTokenFetchPreservesCallerCancellation(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "pre-canceled",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline exceeded",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			wantErr: context.DeadlineExceeded,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			q, m := setupSyncDB(t)
			require.NoError(t, m.User().Create(&models.User{
				Username: "canceled-" + test.name, Password: "x", GroupID: 1, Status: consts.StatusEnabled,
			}))
			user, err := q.User().GetByUsername("canceled-" + test.name)
			require.NoError(t, err)
			token := &models.Token{
				Key: "sk-canceled-" + test.name, Name: "canceled", UserID: user.ID,
				APIRoleMode: models.APIRoleModeExplicit, Status: consts.StatusEnabled,
			}
			require.NoError(t, m.Token().Create(token))
			ctx, cancel := test.context()
			defer cancel()

			_, _, found, err := tokenFetchHandler{}.Fetch(ctx, q, token.Key)

			require.ErrorIs(t, err, test.wantErr)
			require.False(t, found)
		})
	}
}

func TestSystemTokenFetchPreservesCallerCancellation(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "pre-canceled",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline exceeded",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			wantErr: context.DeadlineExceeded,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			q, m := setupSyncDB(t)
			token := &models.Token{
				Key: "sk-system-canceled-" + test.name, Name: "system canceled", UserID: 0,
				APIRoleMode: models.APIRoleModeInherit, Status: consts.StatusEnabled,
			}
			require.NoError(t, m.Token().Create(token))
			ctx, cancel := test.context()
			defer cancel()

			_, _, found, err := tokenFetchHandler{}.Fetch(ctx, q, token.Key)

			require.ErrorIs(t, err, test.wantErr)
			require.False(t, found)
		})
	}
}

// Break caught: reintroducing optional prewarm silently puts API-RBAC latency
// and failures back on the LLM token path.
func TestTokenFetchPerformsNoAPIRoleSetQueries(t *testing.T) {
	q, m, db := setupSyncDBWithDatabase(t)
	require.NoError(t, m.User().Create(&models.User{
		Username: "prewarm-short-circuit", Password: "x", GroupID: 1, Status: consts.StatusEnabled,
	}))
	user, err := q.User().GetByUsername("prewarm-short-circuit")
	require.NoError(t, err)
	token := &models.Token{
		Key: "sk-prewarm-short-circuit", Name: "short-circuit", UserID: user.ID,
		APIRoleMode: models.APIRoleModeExplicit, Status: consts.StatusEnabled,
	}
	require.NoError(t, m.Token().Create(token))
	roleBindingQueries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:count-role-binding-queries",
		func(tx *gorm.DB) {
			if tx.Statement.Table == "role_bindings" {
				roleBindingQueries++
			}
		},
	))

	_, sideData, found, err := tokenFetchHandler{}.Fetch(context.Background(), q, token.Key)

	require.NoError(t, err)
	require.True(t, found)
	require.Zero(t, roleBindingQueries)
	var side protocol.TokenFetchSide
	require.NoError(t, json.Unmarshal(sideData, &side))
}
