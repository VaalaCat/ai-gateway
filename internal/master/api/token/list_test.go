package token

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func seedListToken(t *testing.T, db *gorm.DB, name string, userID uint, status int, expiredAt int64) models.Token {
	t.Helper()
	ensureTokenListOwner(t, db, userID)
	token := models.Token{
		Name:      name,
		Key:       "sk-" + name,
		UserID:    userID,
		Status:    status,
		ExpiredAt: expiredAt,
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token %q: %v", name, err)
	}
	if status == consts.StatusDisabled {
		if err := db.Model(&token).Update("status", consts.StatusDisabled).Error; err != nil {
			t.Fatalf("disable token %q: %v", name, err)
		}
	}
	return token
}

func ensureTokenListOwner(t *testing.T, db *gorm.DB, userID uint) {
	t.Helper()
	var owner models.User
	err := db.First(&owner, userID).Error
	if err == nil {
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("load token owner %d: %v", userID, err)
	}
	owner = models.User{ID: userID, Username: fmt.Sprintf("token-list-owner-%d", userID)}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("seed token owner %d: %v", userID, err)
	}
}

func TestListProjectsOwnerUsernamesInOneBatch(t *testing.T) {
	// Break caught: listing tokens one owner at a time creates an N+1 query,
	// while omitting the projection leaves the admin picker without ownership context.
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	alice := &models.User{Username: "owner-projection-alice"}
	bob := &models.User{Username: "owner-projection-bob"}
	require.NoError(t, db.Create(alice).Error)
	require.NoError(t, db.Create(bob).Error)
	for i := range 50 {
		owner := alice
		if i%2 == 1 {
			owner = bob
		}
		seedListToken(t, db, fmt.Sprintf("owner-projection-%d", i), owner.ID, consts.StatusEnabled, -1)
	}

	const callbackName = "test:count_list_owner_batch_query"
	ownerQueries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			ownerQueries++
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callbackName)) })

	response, err := h.List(ctx, ListRequest{PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 50}})
	require.NoError(t, err)
	require.Len(t, response.Data, 50)
	require.Equal(t, 1, ownerQueries)
	for _, item := range response.Data {
		if item.UserID == alice.ID {
			require.Equal(t, alice.Username, item.OwnerUsername)
		} else {
			require.Equal(t, bob.ID, item.UserID)
			require.Equal(t, bob.Username, item.OwnerUsername)
		}
	}

	body, err := json.Marshal(response)
	require.NoError(t, err)
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Len(t, raw.Data, 50)
	require.Equal(t, response.Data[0].OwnerUsername, raw.Data[0]["owner_username"].(string))
	require.Equal(t, response.Data[0].ID, uint(raw.Data[0]["id"].(float64)))
}

func TestListOwnerBatchFailureReturnsNoPartialResponse(t *testing.T) {
	// Break caught: a failed owner query must not quietly emit selectable tokens
	// without an owner username.
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	owner := &models.User{Username: "owner-batch-failure"}
	require.NoError(t, db.Create(owner).Error)
	seedListToken(t, db, "owner-batch-failure-token", owner.ID, consts.StatusEnabled, -1)

	const callbackName = "test:fail_list_owner_batch_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			_ = tx.AddError(errors.New("forced owner batch failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callbackName)) })

	response, err := h.List(ctx, ListRequest{PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 50}})
	requireAPIStatus(t, err, http.StatusInternalServerError)
	require.Empty(t, response.Data)
	require.Zero(t, response.Total)
}

func TestListEmptyPageSkipsOwnerBatchQuery(t *testing.T) {
	// Break caught: looking up owners for an empty response adds a pointless query
	// and makes an out-of-range page depend on unrelated user-table availability.
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	seedListToken(t, db, "only-token", 1, consts.StatusEnabled, -1)

	const callbackName = "test:count_empty_page_owner_query"
	ownerQueries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			ownerQueries++
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callbackName)) })

	response, err := h.List(ctx, ListRequest{PaginationQuery: api.PaginationQuery{Page: 2, PageSize: 50}})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	require.Equal(t, int64(1), response.Total)
	require.Zero(t, ownerQueries)
}

func TestListKeepsOwnerlessSystemTokenReadable(t *testing.T) {
	// Break caught: UserID=0 is the existing system-token sentinel, not a
	// dangling user row. Its presence must not make the whole admin list fail.
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	systemToken := models.Token{
		Name: "__system_test__", Key: "sk-system-list", UserID: 0,
		Status: consts.StatusEnabled, ExpiredAt: -1,
	}
	require.NoError(t, db.Create(&systemToken).Error)

	response, err := h.List(ctx, ListRequest{PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 50}})
	require.NoError(t, err)
	require.Len(t, response.Data, 1)
	require.Equal(t, systemToken.ID, response.Data[0].ID)
	require.Equal(t, "System", response.Data[0].OwnerUsername)
}

func TestListRejectsDanglingOrdinaryTokenOwner(t *testing.T) {
	// Break caught: UserID=0 is the sole ownerless sentinel. Returning a
	// non-system token with a missing owner would make picker ownership look valid.
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	dangling := models.Token{
		Name: "dangling-owner", Key: "sk-dangling-owner", UserID: 98765,
		Status: consts.StatusEnabled, ExpiredAt: -1,
	}
	require.NoError(t, db.Create(&dangling).Error)

	response, err := h.List(ctx, ListRequest{PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 50}})
	requireAPIStatus(t, err, http.StatusInternalServerError)
	require.Empty(t, response.Data)
	require.Zero(t, response.Total)
}

func TestListUsableOnlyUsesServerClock(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	h.clock = func() time.Time { return time.Unix(now, 0) }
	setScope(ctx, true, 1)

	seedListToken(t, db, "enabled-never", 1, consts.StatusEnabled, -1)
	seedListToken(t, db, "enabled-future", 1, consts.StatusEnabled, now+1)
	seedListToken(t, db, "enabled-expired", 1, consts.StatusEnabled, now-1)
	seedListToken(t, db, "disabled-future", 1, consts.StatusDisabled, now+1)

	response, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 2},
		UsableOnly:      true,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if response.Page != 1 || response.PageSize != 2 || response.Total != 2 {
		t.Fatalf("pagination = page %d size %d total %d, want page 1 size 2 total 2", response.Page, response.PageSize, response.Total)
	}
	want := map[string]bool{"enabled-never": true, "enabled-future": true}
	if len(response.Data) != len(want) {
		t.Fatalf("got %d usable tokens, want %d", len(response.Data), len(want))
	}
	for _, token := range response.Data {
		if !want[token.Name] {
			t.Fatalf("unexpected usable token %q", token.Name)
		}
	}
}

func TestListWithoutUsableOnlyPreservesExistingStatusFilter(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	seedListToken(t, db, "disabled", 1, consts.StatusDisabled, 1_800_000_001)

	response, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 2},
		Status:          strconv.Itoa(consts.StatusDisabled),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].Name != "disabled" {
		t.Fatalf("disabled status query = %#v, want only disabled token", response)
	}
}

func TestListFiltersByAPIRoleMode(t *testing.T) {
	// This catches mode filtering applied after pagination, ignored altogether,
	// or accepting arbitrary mode values as an unfiltered list.
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	inherit := seedListToken(t, db, "mode-inherit", 1, consts.StatusEnabled, -1)
	explicit := models.Token{
		Name: "mode-explicit", Key: "sk-mode-explicit", UserID: 1, Status: consts.StatusEnabled,
		ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit,
	}
	require.NoError(t, db.Create(&explicit).Error)

	filtered, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 10},
		APIRoleMode:     string(models.APIRoleModeExplicit),
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), filtered.Total)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, explicit.ID, filtered.Data[0].ID)

	_, err = h.List(ctx, ListRequest{APIRoleMode: "manual"})
	requireAPIStatus(t, err, 400)

	unfiltered, err := h.List(ctx, ListRequest{PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 10}})
	require.NoError(t, err)
	require.Equal(t, int64(2), unfiltered.Total)
	require.ElementsMatch(t, []uint{inherit.ID, explicit.ID}, []uint{unfiltered.Data[0].ID, unfiltered.Data[1].ID})
}

func TestListUsableOnlyStillHonorsScopedOwner(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	h.clock = func() time.Time { return time.Unix(now, 0) }
	setScope(ctx, false, 1)
	seedListToken(t, db, "current-user", 1, consts.StatusEnabled, now+1)
	seedListToken(t, db, "other-user", 2, consts.StatusEnabled, now+1)

	response, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 10},
		UserID:          "2",
		UsableOnly:      true,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].Name != "current-user" || response.Data[0].UserID != 1 {
		t.Fatalf("scoped usable tokens = %#v, want only current user's token", response)
	}
}

func TestListHTTPBindsUsableOnlyQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	clockCalls := 0
	h.clock = func() time.Time {
		clockCalls++
		return time.Unix(now, 750_000_000)
	}
	seedListToken(t, db, "enabled-current", 1, consts.StatusEnabled, now)
	seedListToken(t, db, "disabled-future", 1, consts.StatusDisabled, now+1)
	seedListToken(t, db, "enabled-expired", 1, consts.StatusEnabled, now-1)

	router := gin.New()
	router.GET("/tokens", func(c *gin.Context) {
		c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: true, UserID: 1})
	}, api.Adapt(api.NewAdapter(nil, nil, ctx.App), api.BindQuery, h.List))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/tokens?usable_only=true&page=1&page_size=10", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /tokens?usable_only=true status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("X-Vaala-Server-Time-Ms"); got != "1800000000750" {
		t.Fatalf("X-Vaala-Server-Time-Ms = %q, want 1800000000750", got)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want one shared sample for filter and header", clockCalls)
	}

	var response api.PaginatedResponse[models.Token]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].Name != "enabled-current" {
		t.Fatalf("usable_only response = %#v, want only enabled-current", response)
	}
	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, exists := raw["server_now"]; exists {
		t.Fatal("Token list JSON must remain backward compatible; server time belongs in response metadata")
	}
}

func TestListAPIRouteScopeFiltersBeforePaginationAndReturnsAccurateTotal(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	h.clock = func() time.Time { return time.Unix(now, 0) }
	setScope(ctx, true, 1)
	service, route := seedTokenListInvokeScope(t, db)
	role := seedTokenListInvokeRole(t, db, models.APIResourceRoute, route.ID)
	owner := models.User{Username: "picker-owner", GroupID: 7, Status: consts.StatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("seed picker owner: %v", err)
	}

	oldest := seedTokenListExplicit(t, db, "invokable-oldest", owner.ID, consts.StatusEnabled, -1)
	denied := seedTokenListExplicit(t, db, "zero-permission", owner.ID, consts.StatusEnabled, -1)
	newest := seedTokenListExplicit(t, db, "invokable-newest", owner.ID, consts.StatusEnabled, now+1)
	disabled := seedTokenListExplicit(t, db, "disabled", owner.ID, consts.StatusDisabled, now+1)
	expired := seedTokenListExplicit(t, db, "expired", owner.ID, consts.StatusEnabled, now-1)
	for _, token := range []models.Token{oldest, newest, disabled, expired} {
		if err := db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error; err != nil {
			t.Fatalf("bind picker token %d: %v", token.ID, err)
		}
	}

	first, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 1},
		APIServiceID:    &service.ID,
		APIRouteID:      &route.ID,
	})
	if err != nil {
		t.Fatalf("List first route-scoped page: %v", err)
	}
	if first.Total != 2 || len(first.Data) != 1 || first.Data[0].ID != newest.ID {
		t.Fatalf("first scoped page = %#v, want newest invokable Token and total 2", first)
	}

	second, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 2, PageSize: 1},
		APIServiceID:    &service.ID,
		APIRouteID:      &route.ID,
	})
	if err != nil {
		t.Fatalf("List second route-scoped page: %v", err)
	}
	if second.Total != 2 || len(second.Data) != 1 || second.Data[0].ID != oldest.ID {
		t.Fatalf("second scoped page = %#v, want oldest invokable Token and total 2", second)
	}
	if denied.ID == 0 {
		t.Fatal("zero-permission fixture must be persisted")
	}
}

// Break caught: exact Route-Token validation must intersect Token ID with
// current owner visibility, usability, and invoke permission before returning
// a key-bearing Token.
func TestListAPIRouteScopeFiltersExactToken(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	h.clock = func() time.Time { return time.Unix(now, 0) }
	owner := models.User{Username: "exact-picker-owner", GroupID: 7, Status: consts.StatusEnabled}
	otherOwner := models.User{Username: "exact-picker-other-owner", GroupID: 7, Status: consts.StatusEnabled}
	for _, user := range []*models.User{&owner, &otherOwner} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("seed picker owner: %v", err)
		}
	}
	setScope(ctx, false, owner.ID)
	service, route := seedTokenListInvokeScope(t, db)
	role := seedTokenListInvokeRole(t, db, models.APIResourceRoute, route.ID)

	allowed := seedTokenListExplicit(t, db, "exact-allowed", owner.ID, consts.StatusEnabled, now+1)
	denied := seedTokenListExplicit(t, db, "exact-denied", owner.ID, consts.StatusEnabled, now+1)
	disabled := seedTokenListExplicit(t, db, "exact-disabled", owner.ID, consts.StatusDisabled, now+1)
	expired := seedTokenListExplicit(t, db, "exact-expired", owner.ID, consts.StatusEnabled, now-1)
	other := seedTokenListExplicit(t, db, "exact-other-owner", otherOwner.ID, consts.StatusEnabled, now+1)
	for _, token := range []models.Token{allowed, disabled, expired, other} {
		if err := db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error; err != nil {
			t.Fatalf("bind picker Token %d: %v", token.ID, err)
		}
	}

	for _, test := range []struct {
		name  string
		token models.Token
		want  int
	}{
		{name: "allowed", token: allowed, want: 1},
		{name: "denied", token: denied, want: 0},
		{name: "disabled", token: disabled, want: 0},
		{name: "expired", token: expired, want: 0},
		{name: "other owner", token: other, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := h.List(ctx, ListRequest{
				PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 1},
				TokenID:         &test.token.ID,
				APIServiceID:    &service.ID,
				APIRouteID:      &route.ID,
			})
			if err != nil {
				t.Fatalf("List exact Route Token: %v", err)
			}
			if response.Total != int64(test.want) || len(response.Data) != test.want {
				t.Fatalf("exact scoped response = %#v; want %d Token", response, test.want)
			}
			if test.want == 1 && response.Data[0].ID != test.token.ID {
				t.Fatalf("returned Token %d, want %d", response.Data[0].ID, test.token.ID)
			}
		})
	}
}

// Break caught: loading every usable Token and filtering by ID only in the
// browser makes each client page trigger another full RBAC scan. The exact ID
// must bound the Token principal batch passed into TokenInvokeFinder.
func TestListAPIRouteScopeExactTokenBoundsRBACCandidates(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	h.clock = func() time.Time { return time.Unix(now, 0) }
	owner := models.User{Username: "bounded-picker-owner", GroupID: 7, Status: consts.StatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("seed picker owner: %v", err)
	}
	setScope(ctx, false, owner.ID)
	service, route := seedTokenListInvokeScope(t, db)
	role := seedTokenListInvokeRole(t, db, models.APIResourceRoute, route.ID)
	target := seedTokenListExplicit(t, db, "bounded-target", owner.ID, consts.StatusEnabled, now+1)
	if err := db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: target.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("bind target Token: %v", err)
	}
	unrelated := make([]models.Token, 250)
	for i := range unrelated {
		unrelated[i] = models.Token{
			Name: "unrelated-" + strconv.Itoa(i), Key: "sk-unrelated-" + strconv.Itoa(i),
			UserID: owner.ID, Status: consts.StatusEnabled, ExpiredAt: now + 1,
			APIRoleMode: models.APIRoleModeExplicit,
		}
	}
	if err := db.Create(&unrelated).Error; err != nil {
		t.Fatalf("seed unrelated Tokens: %v", err)
	}

	var tokenPrincipalBatchSizes []int
	const callbackName = "test:observe_exact_token_rbac_batch"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "role_bindings" || !strings.Contains(tx.Statement.SQL.String(), "principal_id IN") {
			return
		}
		if len(tx.Statement.Vars) == 0 || fmt.Sprint(tx.Statement.Vars[0]) != string(models.APIPrincipalToken) {
			return
		}
		batchSize := 0
		for _, variable := range tx.Statement.Vars {
			if _, ok := variable.(uint); ok {
				batchSize++
			}
		}
		tokenPrincipalBatchSizes = append(tokenPrincipalBatchSizes, batchSize)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callbackName)) })

	response, err := h.List(ctx, ListRequest{
		PaginationQuery: api.PaginationQuery{Page: 1, PageSize: 1},
		TokenID:         &target.ID,
		APIServiceID:    &service.ID,
		APIRouteID:      &route.ID,
	})
	if err != nil {
		t.Fatalf("List exact Route Token: %v", err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].ID != target.ID {
		t.Fatalf("exact scoped response = %#v, want target Token", response)
	}
	require.Equal(t, []int{1}, tokenPrincipalBatchSizes)
}

func TestListAPIRouteScopeFailsClosedForIncompleteOrCrossServiceScope(t *testing.T) {
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	service, _ := seedTokenListInvokeScope(t, db)
	_, foreignRoute := seedTokenListInvokeScope(t, db)

	_, err := h.List(ctx, ListRequest{APIServiceID: &service.ID})
	if err == nil {
		t.Fatal("List with an incomplete API invoke scope must fail closed")
	}
	_, err = h.List(ctx, ListRequest{APIServiceID: &service.ID, APIRouteID: &foreignRoute.ID})
	if err == nil {
		t.Fatal("List with a Route from another Service must fail closed")
	}
}

func TestListHTTPBindsAPIRouteInvokeScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, ctx, db := setupTokenUpdateTest(t)
	setScope(ctx, true, 1)
	service, route := seedTokenListInvokeScope(t, db)
	role := seedTokenListInvokeRole(t, db, models.APIResourceService, service.ID)
	owner := models.User{Username: "http-picker-owner", GroupID: 7, Status: consts.StatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatalf("seed HTTP picker owner: %v", err)
	}
	allowed := seedTokenListExplicit(t, db, "http-allowed", owner.ID, consts.StatusEnabled, -1)
	otherAllowed := seedTokenListExplicit(t, db, "http-other-allowed", owner.ID, consts.StatusEnabled, -1)
	seedTokenListExplicit(t, db, "http-denied", owner.ID, consts.StatusEnabled, -1)
	for _, token := range []models.Token{allowed, otherAllowed} {
		if err := db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error; err != nil {
			t.Fatalf("bind HTTP picker token: %v", err)
		}
	}

	router := gin.New()
	router.GET("/tokens", func(c *gin.Context) {
		c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: true, UserID: 1})
	}, api.Adapt(api.NewAdapter(nil, nil, ctx.App), api.BindQuery, h.List))

	path := "/tokens?usable_only=true&api_service_id=" + strconv.FormatUint(uint64(service.ID), 10) +
		"&api_route_id=" + strconv.FormatUint(uint64(route.ID), 10) +
		"&token_id=" + strconv.FormatUint(uint64(allowed.ID), 10) + "&page=1&page_size=1"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d: %s", path, recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response api.PaginatedResponse[models.Token]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode scoped Token response: %v", err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].ID != allowed.ID {
		t.Fatalf("HTTP scoped response = %#v, want only allowed Token", response)
	}
}

// Break caught: collapsing route lookup failures into one internal error makes
// a missing Route look like a server outage and a cross-Service scope look
// retryable. The public Token Picker API needs stable 404/400/500 semantics.
func TestListHTTPMapsAPIRouteInvokeScopeErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       func(models.APIService, models.APIRoute) string
		failRoutes bool
		wantStatus int
	}{
		{
			name: "missing route",
			path: func(service models.APIService, _ models.APIRoute) string {
				return "/tokens?token_id=1&api_service_id=" + strconv.FormatUint(uint64(service.ID), 10) + "&api_route_id=999999"
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "cross service route",
			path: func(service models.APIService, foreignRoute models.APIRoute) string {
				return "/tokens?token_id=1&api_service_id=" + strconv.FormatUint(uint64(service.ID), 10) + "&api_route_id=" + strconv.FormatUint(uint64(foreignRoute.ID), 10)
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "route query failure",
			path: func(service models.APIService, foreignRoute models.APIRoute) string {
				return "/tokens?api_service_id=" + strconv.FormatUint(uint64(service.ID), 10) + "&api_route_id=" + strconv.FormatUint(uint64(foreignRoute.ID), 10)
			},
			failRoutes: true,
			wantStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h, ctx, db := setupTokenUpdateTest(t)
			setScope(ctx, true, 1)
			service, _ := seedTokenListInvokeScope(t, db)
			_, foreignRoute := seedTokenListInvokeScope(t, db)
			if tc.failRoutes {
				const callbackName = "test:fail_token_invoke_route_query"
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement.Table == "api_routes" {
						_ = tx.AddError(errors.New("forced API Route query failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callbackName)) })
			}
			router := gin.New()
			router.GET("/tokens", func(c *gin.Context) {
				c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: true, UserID: 1})
			}, api.Adapt(api.NewAdapter(nil, nil, ctx.App), api.BindQuery, h.List))

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path(service, foreignRoute), nil))
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
		})
	}
}

func seedTokenListInvokeScope(t *testing.T, db *gorm.DB) (models.APIService, models.APIRoute) {
	t.Helper()
	var count int64
	if err := db.Model(&models.APIService{}).Count(&count).Error; err != nil {
		t.Fatalf("count API services: %v", err)
	}
	service := models.APIService{Slug: "token-list-" + strconv.FormatInt(count+1, 10), Name: "Token List", Status: consts.StatusEnabled}
	if err := db.Create(&service).Error; err != nil {
		t.Fatalf("seed Token list service: %v", err)
	}
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	if err := db.Create(&backend).Error; err != nil {
		t.Fatalf("seed Token list backend: %v", err)
	}
	route := models.APIRoute{
		APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast",
		Protocols: datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP}, Status: consts.StatusEnabled,
	}
	if err := db.Create(&route).Error; err != nil {
		t.Fatalf("seed Token list route: %v", err)
	}
	return service, route
}

func seedTokenListInvokeRole(t *testing.T, db *gorm.DB, resource models.APIResource, resourceID uint) models.Role {
	t.Helper()
	role := models.Role{Key: "token-list-role-" + strconv.FormatUint(uint64(resourceID), 10) + string(resource), Name: "Token List Invoker", Status: consts.StatusEnabled}
	if err := db.Create(&role).Error; err != nil {
		t.Fatalf("seed Token list role: %v", err)
	}
	permission := models.Permission{Resource: resource, ResourceID: resourceID, Action: models.APIPermissionInvoke}
	if err := db.Create(&permission).Error; err != nil {
		t.Fatalf("seed Token list permission: %v", err)
	}
	if err := db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error; err != nil {
		t.Fatalf("seed Token list role permission: %v", err)
	}
	return role
}

func seedTokenListExplicit(t *testing.T, db *gorm.DB, name string, userID uint, status int, expiredAt int64) models.Token {
	t.Helper()
	token := models.Token{
		Name: name, Key: "sk-" + name, UserID: userID, Status: status,
		ExpiredAt: expiredAt, APIRoleMode: models.APIRoleModeExplicit,
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed explicit Token %q: %v", name, err)
	}
	if status == consts.StatusDisabled {
		if err := db.Model(&token).Update("status", status).Error; err != nil {
			t.Fatalf("disable explicit Token %q: %v", name, err)
		}
	}
	return token
}

func TestGetHTTPReturnsServerTimeWithoutChangingOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, ctx, db := setupTokenUpdateTest(t)
	now := int64(1_800_000_000)
	h.clock = func() time.Time { return time.Unix(now, 750_000_000) }
	owned := seedListToken(t, db, "owned", 1, consts.StatusEnabled, now)
	foreign := seedListToken(t, db, "foreign", 2, consts.StatusEnabled, now)

	tests := []struct {
		name       string
		isAdmin    bool
		userID     uint
		tokenID    uint
		wantStatus int
	}{
		{name: "ordinary owner", userID: 1, tokenID: owned.ID, wantStatus: http.StatusOK},
		{name: "ordinary foreign owner", userID: 1, tokenID: foreign.ID, wantStatus: http.StatusNotFound},
		{name: "administrator foreign owner", isAdmin: true, userID: 1, tokenID: foreign.ID, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/tokens/:id", func(c *gin.Context) {
				c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: tt.isAdmin, UserID: tt.userID})
			}, api.Adapt(api.NewAdapter(nil, nil, ctx.App), api.BindURI, h.Get))

			recorder := httptest.NewRecorder()
			path := "/tokens/" + strconv.FormatUint(uint64(tt.tokenID), 10)
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != tt.wantStatus {
				t.Fatalf("GET %s status = %d, want %d: %s", path, recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if got := recorder.Header().Get("X-Vaala-Server-Time-Ms"); got != "1800000000750" {
				t.Fatalf("X-Vaala-Server-Time-Ms = %q, want 1800000000750", got)
			}
		})
	}
}
