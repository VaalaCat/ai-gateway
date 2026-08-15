package api_access_grant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterapi "github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type effectiveAccessFinderContract interface {
	Find(context.Context, PrincipalRef, uint) (EffectiveAccess, error)
}

var _ effectiveAccessFinderContract = (*EffectiveAccessFinder)(nil)

// Break caught: widening the brief's finder result to an HTTP response couples
// lower-layer callers to configured/source presentation fields.
func TestEffectiveAccessFinderPublicContractReturnsOnlyEffectiveAccess(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID}))

	got, err := NewEffectiveAccessFinder(dao.NewAdminQuery(fx.ctx)).Find(context.Background(), principal, fx.serviceA.ID)
	require.NoError(t, err)
	require.Equal(t, EffectiveAccess{Scope: GrantScopeRoutes, RouteIDs: []uint{fx.routeA1.ID}}, got)
}

// Break caught: dropping any one of the direct managed, direct custom, or
// inherited group role sets would make the effective projection incomplete.
func TestEffectiveAccessFinderMergesManagedCustomAndUserGroupSources(t *testing.T) {
	fx := newGrantFixture(t)
	group := models.UserGroup{Name: "effective-group", Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&group).Error)
	require.NoError(t, fx.db.Model(&models.User{}).Where("id = ?", fx.user.ID).Update("group_id", group.ID).Error)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID}))
	seedAccessRole(t, fx.db, "direct-custom", models.APIRoleKindCustom, consts.StatusEnabled,
		principal, models.APIResourceRoute, fx.routeA2.ID)
	seedAccessRole(t, fx.db, "group-service", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalUserGroup, ID: group.ID}, models.APIResourceService, fx.serviceA.ID)
	seedAccessRole(t, fx.db, "disabled-service", models.APIRoleKindCustom, consts.StatusDisabled,
		principal, models.APIResourceService, fx.serviceB.ID)

	got, err := NewEffectiveAccessFinder(dao.NewAdminQuery(fx.ctx)).FindResponse(context.Background(), principal, fx.serviceA.ID)
	require.NoError(t, err)
	require.Equal(t, EffectiveAccess{Scope: GrantScopeService, RouteIDs: []uint{}}, got.Effective)
	require.Equal(t, []AccessSource{AccessSourceManaged, AccessSourceCustomRole, AccessSourceUserGroup}, got.Sources)
	require.Equal(t, &ConfiguredGrant{
		PrincipalType: models.APIPrincipalUser, PrincipalID: fx.user.ID, APIServiceID: fx.serviceA.ID,
		Scope: GrantScopeRoutes, RouteIDs: []uint{fx.routeA1.ID},
	}, got.Configured)

	blocked, err := NewEffectiveAccessFinder(dao.NewAdminQuery(fx.ctx)).FindResponse(context.Background(), principal, fx.serviceB.ID)
	require.NoError(t, err)
	require.Equal(t, EffectiveAccess{Scope: GrantScopeRoutes, RouteIDs: []uint{}}, blocked.Effective)
	require.Empty(t, blocked.Sources)
}

// Break caught: resolving all tokens through their owner would let explicit
// tokens inherit access, while resolving all tokens directly would break the
// established inherit mode.
func TestEffectiveAccessFinderMatchesExplicitAndInheritedTokenRoleSemantics(t *testing.T) {
	fx := newGrantFixture(t)
	group := models.UserGroup{Name: "token-group", Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&group).Error)
	require.NoError(t, fx.db.Model(&models.User{}).Where("id = ?", fx.user.ID).Update("group_id", group.ID).Error)
	seedAccessRole(t, fx.db, "owner-group-service", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalUserGroup, ID: group.ID}, models.APIResourceService, fx.serviceA.ID)
	seedAccessRole(t, fx.db, "historical-inherit-token-binding", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalToken, ID: fx.inheritToken.ID}, models.APIResourceService, fx.serviceB.ID)
	require.NoError(t, fx.replace(
		PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID},
		fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID},
	))

	finder := NewEffectiveAccessFinder(dao.NewAdminQuery(fx.ctx))
	explicit, err := finder.FindResponse(context.Background(), PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID}, fx.serviceA.ID)
	require.NoError(t, err)
	require.Equal(t, EffectiveAccess{Scope: GrantScopeRoutes, RouteIDs: []uint{fx.routeA1.ID}}, explicit.Effective)
	require.Equal(t, []AccessSource{AccessSourceManaged}, explicit.Sources)

	inherited, err := finder.FindResponse(context.Background(), PrincipalRef{Type: models.APIPrincipalToken, ID: fx.inheritToken.ID}, fx.serviceA.ID)
	require.NoError(t, err)
	require.Equal(t, EffectiveAccess{Scope: GrantScopeService, RouteIDs: []uint{}}, inherited.Effective)
	require.Equal(t, []AccessSource{AccessSourceUserGroup}, inherited.Sources)
	require.Nil(t, inherited.Configured)

	ignoredDirect, err := finder.FindResponse(context.Background(), PrincipalRef{Type: models.APIPrincipalToken, ID: fx.inheritToken.ID}, fx.serviceB.ID)
	require.NoError(t, err)
	require.Equal(t, EffectiveAccess{Scope: GrantScopeRoutes, RouteIDs: []uint{}}, ignoredDirect.Effective)
	require.Empty(t, ignoredDirect.Sources)
}

// Break caught: accepting a zero/missing principal or service would turn a
// malformed detail request into a misleading empty-access response.
func TestEffectiveAccessFinderRejectsInvalidOrMissingSubjects(t *testing.T) {
	fx := newGrantFixture(t)
	finder := NewEffectiveAccessFinder(dao.NewAdminQuery(fx.ctx))

	_, err := finder.Find(context.Background(), PrincipalRef{Type: models.APIPrincipalUser}, fx.serviceA.ID)
	require.Error(t, err)
	_, err = finder.Find(context.Background(), PrincipalRef{Type: models.APIPrincipalUser, ID: 999_999}, fx.serviceA.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = finder.Find(context.Background(), PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}, 0)
	require.Error(t, err)
	_, err = finder.Find(context.Background(), PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}, 999_999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// Break caught: omitting a list filter, applying it after pagination, or
// losing the managed configuration while computing effective access would
// return the wrong admin-facing rows.
func TestListAccessGrantsHTTPFiltersPaginatesAndProjectsEffectiveAccess(t *testing.T) {
	fx := newGrantFixture(t)
	group := models.UserGroup{Name: "weather operators", Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&group).Error)
	require.NoError(t, fx.db.Model(&models.User{}).Where("id = ?", fx.user.ID).Update("group_id", group.ID).Error)
	userRef := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(userRef, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID}))
	seedAccessRole(t, fx.db, "weather-custom", models.APIRoleKindCustom, consts.StatusEnabled,
		userRef, models.APIResourceRoute, fx.routeA2.ID)
	seedAccessRole(t, fx.db, "group-weather-service", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalUserGroup, ID: group.ID}, models.APIResourceService, fx.serviceA.ID)
	require.NoError(t, fx.replace(
		PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID},
		fx.serviceB.ID, GrantScopeService, nil,
	))
	router := accessGrantReadRouter(fx)

	response := grantHTTPRequest(router, http.MethodGet, fmt.Sprintf(
		"/grants?principal_type=user&principal_id=%d&api_service_id=%d&search=weather&page=1&page_size=1",
		fx.user.ID, fx.serviceA.ID,
	), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page masterapi.PaginatedResponse[AccessGrantResponse]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 1, page.PageSize)
	require.Equal(t, []AccessGrantResponse{{
		PrincipalType:  models.APIPrincipalUser,
		PrincipalID:    fx.user.ID,
		PrincipalLabel: fx.user.Username,
		APIServiceID:   fx.serviceA.ID,
		APIServiceName: fx.serviceA.Name,
		Configured: &ConfiguredGrant{
			PrincipalType: models.APIPrincipalUser, PrincipalID: fx.user.ID, APIServiceID: fx.serviceA.ID,
			Scope: GrantScopeRoutes, RouteIDs: []uint{fx.routeA1.ID},
		},
		Effective: EffectiveAccess{Scope: GrantScopeService, RouteIDs: []uint{}},
		Sources:   []AccessSource{AccessSourceManaged, AccessSourceCustomRole, AccessSourceUserGroup},
	}}, page.Data)

	effectiveResponse := grantHTTPRequest(router, http.MethodGet, fmt.Sprintf(
		"/grants/effective?principal_type=token&principal_id=%d&api_service_id=%d",
		fx.explicitToken.ID, fx.serviceB.ID,
	), nil)
	require.Equal(t, http.StatusOK, effectiveResponse.Code, effectiveResponse.Body.String())
	var effective AccessGrantResponse
	require.NoError(t, json.Unmarshal(effectiveResponse.Body.Bytes(), &effective))
	require.Equal(t, EffectiveAccess{Scope: GrantScopeService, RouteIDs: []uint{}}, effective.Effective)
	require.Equal(t, []AccessSource{AccessSourceManaged}, effective.Sources)

	invalid := grantHTTPRequest(router, http.MethodGet, "/grants?principal_id=1", nil)
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	missing := grantHTTPRequest(router, http.MethodGet, fmt.Sprintf(
		"/grants/effective?principal_type=user&principal_id=999999&api_service_id=%d", fx.serviceA.ID,
	), nil)
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}

// Break caught: looking up labels in the browser once per table cell turns one
// paginated API response into linear user/group/token/service fetches. The
// list projection must return only safe display labels, never token keys or
// user email addresses.
func TestListAccessGrantsProjectsSafeDisplayLabels(t *testing.T) {
	fx := newGrantFixture(t)
	fx.user.DisplayName = "Grant owner"
	fx.user.Email = "owner@example.test"
	require.NoError(t, fx.db.Model(&models.User{}).Where("id = ?", fx.user.ID).Updates(map[string]any{"display_name": fx.user.DisplayName, "email": fx.user.Email}).Error)
	group := models.UserGroup{Name: "Grant operators", Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&group).Error)
	require.NoError(t, fx.replace(PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}, fx.serviceA.ID, GrantScopeService, nil))
	require.NoError(t, fx.replace(PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID}, fx.serviceA.ID, GrantScopeService, nil))
	require.NoError(t, fx.replace(PrincipalRef{Type: models.APIPrincipalUserGroup, ID: group.ID}, fx.serviceA.ID, GrantScopeService, nil))

	rows, total, err := NewGrantListFinder(fx.db, dao.NewAdminQuery(fx.ctx)).List(context.Background(), dao.ListOptions{Page: 1, PageSize: 20}, grantListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	labels := make(map[models.APIPrincipalType]string, len(rows))
	for _, row := range rows {
		labels[row.PrincipalType] = row.PrincipalLabel
		require.Equal(t, fx.serviceA.Name, row.APIServiceName)
	}
	require.Equal(t, "Grant owner", labels[models.APIPrincipalUser])
	require.Equal(t, fx.explicitToken.Name, labels[models.APIPrincipalToken])
	require.Equal(t, group.Name, labels[models.APIPrincipalUserGroup])
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), fx.explicitToken.Key)
	require.NotContains(t, string(encoded), fx.user.Email)
}

// Break caught: projecting each row through the single-item finder would add
// user/token/binding/role/permission queries for every page row.
func TestListAccessGrantsUsesConstantQueryCount(t *testing.T) {
	counts := make(map[int]int)
	for _, rowCount := range []int{1, 8} {
		t.Run(fmt.Sprintf("rows_%d", rowCount), func(t *testing.T) {
			fx := newGrantFixture(t)
			for i := 0; i < rowCount; i++ {
				user := models.User{Username: fmt.Sprintf("bulk-user-%02d", i), Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
				require.NoError(t, fx.db.Create(&user).Error)
				require.NoError(t, fx.replace(
					PrincipalRef{Type: models.APIPrincipalUser, ID: user.ID}, fx.serviceA.ID, GrantScopeService, nil,
				))
			}
			queries := 0
			callbackName := fmt.Sprintf("test:count_access_grant_list_%d", rowCount)
			require.NoError(t, fx.db.Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) { queries++ }))
			t.Cleanup(func() { _ = fx.db.Callback().Query().Remove(callbackName) })

			response := grantHTTPRequest(accessGrantReadRouter(fx), http.MethodGet,
				fmt.Sprintf("/grants?search=bulk-user&page=1&page_size=%d", rowCount), nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var page masterapi.PaginatedResponse[AccessGrantResponse]
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
			require.Len(t, page.Data, rowCount)
			t.Logf("rows=%d queries=%d", rowCount, queries)
			counts[rowCount] = queries
		})
	}
	require.Equal(t, counts[1], counts[8])
}

// Break caught: discovering keys before filtering disabled roles lets a
// disabled-only binding consume total/page slots and push enabled grants onto
// later pages. A key with both enabled and disabled bindings must remain once.
func TestListAccessGrantsPagesOnlyKeysBackedByEnabledRoles(t *testing.T) {
	fx := newGrantFixture(t)
	disabledOnly := models.User{Username: "disabled-only", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	mixed := models.User{Username: "mixed-role-status", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	enabledOnly := models.User{Username: "enabled-only", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, fx.db.Create(&disabledOnly).Error)
	require.NoError(t, fx.db.Create(&mixed).Error)
	require.NoError(t, fx.db.Create(&enabledOnly).Error)
	seedAccessRole(t, fx.db, "disabled-only-role", models.APIRoleKindCustom, consts.StatusDisabled,
		PrincipalRef{Type: models.APIPrincipalUser, ID: disabledOnly.ID}, models.APIResourceService, fx.serviceA.ID)
	seedAccessRole(t, fx.db, "mixed-disabled-role", models.APIRoleKindCustom, consts.StatusDisabled,
		PrincipalRef{Type: models.APIPrincipalUser, ID: mixed.ID}, models.APIResourceService, fx.serviceA.ID)
	seedAccessRole(t, fx.db, "mixed-enabled-role", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalUser, ID: mixed.ID}, models.APIResourceRoute, fx.routeA1.ID)
	seedAccessRole(t, fx.db, "enabled-only-role", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalUser, ID: enabledOnly.ID}, models.APIResourceService, fx.serviceA.ID)
	finder := NewGrantListFinder(fx.db, dao.NewAdminQuery(fx.ctx))

	page1, total, err := finder.List(context.Background(), dao.ListOptions{Page: 1, PageSize: 1}, grantListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, page1, 1)
	require.Equal(t, mixed.ID, page1[0].PrincipalID)
	page2, total, err := finder.List(context.Background(), dao.ListOptions{Page: 2, PageSize: 1}, grantListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, page2, 1)
	require.Equal(t, enabledOnly.ID, page2[0].PrincipalID)
	page3, total, err := finder.List(context.Background(), dao.ListOptions{Page: 3, PageSize: 1}, grantListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Empty(t, page3)
}

// Break caught: treating an unknown Token api_role_mode like an empty role set
// or returning a batch-only error contract would diverge from RoleSetFinder.
func TestListAccessGrantsRejectsInvalidTokenRoleMode(t *testing.T) {
	fx := newGrantFixture(t)
	seedAccessRole(t, fx.db, "invalid-token-mode-role", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID}, models.APIResourceService, fx.serviceA.ID)
	invalidMode := models.APIRoleMode("invalid")
	require.NoError(t, fx.db.Exec("PRAGMA ignore_check_constraints = ON").Error)
	require.NoError(t, fx.db.Model(&models.Token{}).Where("id = ?", fx.explicitToken.ID).Update("api_role_mode", invalidMode).Error)
	tokenType := models.APIPrincipalToken

	_, _, err := NewGrantListFinder(fx.db, dao.NewAdminQuery(fx.ctx)).List(
		context.Background(), dao.ListOptions{Page: 1, PageSize: 20}, grantListFilter{PrincipalType: &tokenType},
	)
	require.Error(t, err)
	require.ErrorContains(t, err, fmt.Sprintf("token %d", fx.explicitToken.ID))
	require.ErrorContains(t, err, "invalid mode")
	require.ErrorContains(t, err, fmt.Sprintf("mode %q", invalidMode))
	query := dao.NewAdminQuery(fx.ctx)
	_, roleSetErr := apirbac.NewRoleSetFinder(query.User(), query.Token(), query.APIRBAC()).FindToken(
		context.Background(), fx.explicitToken.ID,
	)
	require.Error(t, roleSetErr)
	require.EqualError(t, err, roleSetErr.Error())
}

// Break caught: a missing inherit owner previously became a zero-value User
// and silently inherited the default group's bindings; a different error from
// RoleSetFinder would leave the batch and single-principal paths inconsistent.
func TestListAccessGrantsRejectsInheritedTokenWithMissingOwner(t *testing.T) {
	fx := newGrantFixture(t)
	orphan := models.Token{UserID: 999_999, Key: "orphan-inherit", Name: "orphan", APIRoleMode: models.APIRoleModeInherit}
	require.NoError(t, fx.db.Create(&orphan).Error)
	seedAccessRole(t, fx.db, "orphan-token-role", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalToken, ID: orphan.ID}, models.APIResourceService, fx.serviceA.ID)
	tokenType := models.APIPrincipalToken

	_, _, err := NewGrantListFinder(fx.db, dao.NewAdminQuery(fx.ctx)).List(
		context.Background(), dao.ListOptions{Page: 1, PageSize: 20}, grantListFilter{PrincipalType: &tokenType},
	)
	require.Error(t, err)
	require.ErrorContains(t, err, fmt.Sprintf("token %d", orphan.ID))
	require.ErrorContains(t, err, fmt.Sprintf("owner %d", orphan.UserID))
	require.ErrorContains(t, err, "does not exist")
	query := dao.NewAdminQuery(fx.ctx)
	_, roleSetErr := apirbac.NewRoleSetFinder(query.User(), query.Token(), query.APIRBAC()).FindToken(
		context.Background(), orphan.ID,
	)
	require.Error(t, roleSetErr)
	require.EqualError(t, err, roleSetErr.Error())
}

// Break caught: batch projection must keep normal inherit Tokens on the owner
// user+group role set while explicit Tokens use only Token bindings.
func TestListAccessGrantsPreservesInheritedAndExplicitTokenRoleSets(t *testing.T) {
	fx := newGrantFixture(t)
	group := models.UserGroup{Name: "batch-token-group", Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&group).Error)
	require.NoError(t, fx.db.Model(&models.User{}).Where("id = ?", fx.user.ID).Update("group_id", group.ID).Error)
	seedAccessRole(t, fx.db, "batch-owner-group", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalUserGroup, ID: group.ID}, models.APIResourceService, fx.serviceA.ID)
	seedAccessRole(t, fx.db, "batch-inherit-token-direct", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalToken, ID: fx.inheritToken.ID}, models.APIResourceRoute, fx.routeA1.ID)
	seedAccessRole(t, fx.db, "batch-explicit-token-direct", models.APIRoleKindCustom, consts.StatusEnabled,
		PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID}, models.APIResourceRoute, fx.routeA2.ID)
	tokenType := models.APIPrincipalToken

	rows, total, err := NewGrantListFinder(fx.db, dao.NewAdminQuery(fx.ctx)).List(
		context.Background(), dao.ListOptions{Page: 1, PageSize: 20}, grantListFilter{PrincipalType: &tokenType},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, []AccessGrantResponse{
		{
			PrincipalType: models.APIPrincipalToken, PrincipalID: fx.inheritToken.ID, APIServiceID: fx.serviceA.ID,
			PrincipalLabel: fx.inheritToken.Name, APIServiceName: fx.serviceA.Name,
			Effective: EffectiveAccess{Scope: GrantScopeService, RouteIDs: []uint{}},
			Sources:   []AccessSource{AccessSourceUserGroup},
		},
		{
			PrincipalType: models.APIPrincipalToken, PrincipalID: fx.explicitToken.ID, APIServiceID: fx.serviceA.ID,
			PrincipalLabel: fx.explicitToken.Name, APIServiceName: fx.serviceA.Name,
			Effective: EffectiveAccess{Scope: GrantScopeRoutes, RouteIDs: []uint{fx.routeA2.ID}},
			Sources:   []AccessSource{AccessSourceCustomRole},
		},
	}, rows)
}

// Break caught: rebuilding the full Agent role index inside the row loop makes
// CPU work grow as page rows times compiled roles even though every row reads
// the same immutable role snapshot.
func TestListAccessGrantsBuildsInvokeProjectorOncePerPage(t *testing.T) {
	for _, rowCount := range []int{1, 100} {
		t.Run(fmt.Sprintf("rows_%d", rowCount), func(t *testing.T) {
			fx := newGrantFixture(t)
			for i := 0; i < rowCount; i++ {
				user := models.User{Username: fmt.Sprintf("cpu-row-%03d", i), Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
				require.NoError(t, fx.db.Create(&user).Error)
				seedAccessRole(t, fx.db, fmt.Sprintf("cpu-role-%03d", i), models.APIRoleKindCustom, consts.StatusEnabled,
					PrincipalRef{Type: models.APIPrincipalUser, ID: user.ID}, models.APIResourceService, fx.serviceA.ID)
			}
			finder := NewGrantListFinder(fx.db, dao.NewAdminQuery(fx.ctx))
			builds := 0
			finder.buildProjector = func(roles []models.Role, compiled []protocol.SyncedAPIRole) (*accessProjector, error) {
				builds++
				return newAccessProjector(roles, compiled)
			}

			rows, total, err := finder.List(context.Background(), dao.ListOptions{Page: 1, PageSize: 100}, grantListFilter{Search: "cpu-row-"})
			require.NoError(t, err)
			require.EqualValues(t, rowCount, total)
			require.Len(t, rows, rowCount)
			require.Equal(t, 1, builds)
		})
	}
}

func accessGrantReadRouter(fx *grantFixture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	application := app.NewApplication()
	application.SetCoreDB(fx.db)
	handler := &Handler{App: application}
	router := gin.New()
	adapter := masterapi.NewAdapter(nil, nil, application)
	router.GET("/grants", masterapi.Adapt(adapter, masterapi.BindQuery, handler.List))
	router.GET("/grants/effective", masterapi.Adapt(adapter, masterapi.BindQuery, handler.Effective))
	return router
}

func seedAccessRole(
	t *testing.T,
	db *gorm.DB,
	key string,
	kind models.APIRoleKind,
	status int,
	principal PrincipalRef,
	resource models.APIResource,
	resourceID uint,
) models.Role {
	t.Helper()
	role := models.Role{Key: key, Name: key, Kind: kind, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	if status != consts.StatusEnabled {
		require.NoError(t, db.Model(&models.Role{}).Where("id = ?", role.ID).Update("status", status).Error)
		role.Status = status
	}
	permission := models.Permission{Resource: resource, ResourceID: resourceID, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Where("resource = ? AND resource_id = ? AND action = ?", resource, resourceID, models.APIPermissionInvoke).
		FirstOrCreate(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, db.Create(&models.RoleBinding{
		PrincipalType: principal.Type, PrincipalID: principal.ID, RoleID: role.ID,
	}).Error)
	return role
}
