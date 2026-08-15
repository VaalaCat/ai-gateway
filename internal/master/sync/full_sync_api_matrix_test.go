package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type apiFullSyncMatrixFixture struct {
	entity       string
	seed501      func(*testing.T, dao.AdminMutation, *gorm.DB)
	deleteBehind func(*testing.T, *gorm.DB)
	insertLate   func(*testing.T, dao.AdminMutation, *gorm.DB)
	dropSource   any
	idField      string
}

func TestAPIFullSyncEntityMatrix(t *testing.T) {
	for _, fixture := range apiFullSyncMatrixFixtures() {
		fixture := fixture
		t.Run(fixture.entity, func(t *testing.T) {
			t.Run("empty ready snapshot", func(t *testing.T) {
				q, _, db := setupSyncDBWithDatabase(t)
				if fixture.entity == events.EntityUserGroupAPIRoleSet {
					require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.UserGroup{}).Error)
				}
				version := int64(31)
				response := callAPIFullSyncMatrix(t, q, fixture.entity, &version, protocol.FullSyncRequest{PageSize: 500})
				assertAPIFullSyncSnapshot(t, response, 0, 0, 0, false, 31, 31)
				require.JSONEq(t, `[]`, string(response.Items))
			})

			t.Run("501 rows keep snapshot across delete and insert", func(t *testing.T) {
				q, m, db := setupSyncDBWithDatabase(t)
				fixture.seed501(t, m, db)
				version := int64(31)
				first := callAPIFullSyncMatrix(t, q, fixture.entity, &version, protocol.FullSyncRequest{PageSize: 500})
				assertAPIFullSyncSnapshot(t, first, 501, 500, 501, true, 31, 31)
				require.Len(t, decodeAPIFullSyncIDs(t, first.Items, fixture.idField), 500)

				fixture.deleteBehind(t, db)
				fixture.insertLate(t, m, db)
				version = 32
				second := callAPIFullSyncMatrix(t, q, fixture.entity, &version, protocol.FullSyncRequest{
					PageSize: 500, AfterID: first.LastID,
					SnapshotMaxID: first.SnapshotMaxID, BaseVersion: first.BaseVersion,
				})
				assertAPIFullSyncSnapshot(t, second, 500, 501, 501, false, 31, 32)
				require.Equal(t, []uint{501}, decodeAPIFullSyncIDs(t, second.Items, fixture.idField))
			})

			t.Run("source DAO error", func(t *testing.T) {
				q, _, db := setupSyncDBWithDatabase(t)
				require.NoError(t, db.Migrator().DropTable(fixture.dropSource))
				version := int64(31)
				handler, ok := NewAPIFullSyncRegistry(nil, func() int64 { return version }).Resolve(fixture.entity)
				require.True(t, ok)
				_, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{PageSize: 500})
				require.Error(t, err)
			})
		})
	}
}

func TestAPIFullSyncEntitySpecificFailures(t *testing.T) {
	t.Run("upstream projection", func(t *testing.T) {
		q, m := setupSyncDB(t)
		require.NoError(t, m.APIService().Create(&models.APIService{Slug: "svc", Name: "Service", Status: 1}))
		require.NoError(t, m.APIBackend().Create(&models.APIBackend{APIServiceID: 1, Name: "primary"}))
		require.NoError(t, m.APIUpstream().Create(&models.APIUpstream{
			BackendID: 1, Name: "bad", BaseURL: "https://example.com", Weight: 1,
			AuthType: models.APIUpstreamAuthBearer, CredentialCiphertext: "bad", Status: 1,
		}))
		assertAPIFullSyncHandlerError(t, q, events.EntityAPIUpstream)
	})

	for _, tc := range []struct {
		name   string
		entity string
		drop   any
	}{
		{name: "role permission association", entity: events.EntityAPIRole, drop: &models.RolePermission{}},
		{name: "group binding association", entity: events.EntityUserGroupAPIRoleSet, drop: &models.RoleBinding{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, m, db := setupSyncDBWithDatabase(t)
			if tc.entity == events.EntityAPIRole {
				require.NoError(t, m.APIRBAC().CreateRole(&models.Role{Key: "reader", Name: "Reader", Status: 1}))
			}
			require.NoError(t, db.Migrator().DropTable(tc.drop))
			assertAPIFullSyncHandlerError(t, q, tc.entity)
		})
	}
}

func TestAPIRoleFullSyncExcludesDisabledRowsFromSnapshot(t *testing.T) {
	q, m := setupSyncDB(t)
	var roleIDs []uint
	for _, role := range []models.Role{
		{Key: "enabled-one", Name: "Enabled one", Status: consts.StatusEnabled},
		{Key: "disabled", Name: "Disabled", Status: consts.StatusEnabled},
		{Key: "enabled-two", Name: "Enabled two", Status: consts.StatusEnabled},
	} {
		role := role
		require.NoError(t, m.APIRBAC().CreateRole(&role))
		roleIDs = append(roleIDs, role.ID)
	}
	require.NoError(t, m.APIRBAC().UpdateRole(roleIDs[1], map[string]any{"status": consts.StatusDisabled}))
	version := int64(7)
	response := callAPIFullSyncMatrix(t, q, events.EntityAPIRole, &version, protocol.FullSyncRequest{PageSize: 500})
	assertAPIFullSyncSnapshot(t, response, 2, 3, 3, false, 7, 7)
	require.Equal(t, []uint{1, 3}, decodeAPIFullSyncIDs(t, response.Items, "id"))
}

func callAPIFullSyncMatrix(
	t *testing.T,
	q dao.AdminQuery,
	entity string,
	version *int64,
	request protocol.FullSyncRequest,
) protocol.FullSyncResponse {
	t.Helper()
	handler, ok := NewAPIFullSyncRegistry(nil, func() int64 { return *version }).Resolve(entity)
	require.True(t, ok)
	request.Entity = entity
	response, err := handler.FullSync(context.Background(), q, request)
	require.NoError(t, err)
	return response
}

func assertAPIFullSyncSnapshot(
	t *testing.T,
	response protocol.FullSyncResponse,
	total int64,
	lastID, snapshotMaxID uint,
	hasMore bool,
	baseVersion, version int64,
) {
	t.Helper()
	require.Equal(t, total, response.Total)
	require.Equal(t, lastID, response.LastID)
	require.Equal(t, hasMore, response.HasMore)
	require.True(t, response.Keyset)
	require.Equal(t, snapshotMaxID, response.SnapshotMaxID)
	require.Equal(t, baseVersion, response.BaseVersion)
	require.Equal(t, version, response.Version)
	require.Equal(t, protocol.APIFullSyncSnapshotContractV1, response.SnapshotContract)
}

func decodeAPIFullSyncIDs(t *testing.T, data []byte, idField string) []uint {
	t.Helper()
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(data, &rows))
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = uint(row[idField].(float64))
	}
	return ids
}

func assertAPIFullSyncHandlerError(t *testing.T, q dao.AdminQuery, entity string) {
	t.Helper()
	handler, ok := NewAPIFullSyncRegistry(nil, func() int64 { return 1 }).Resolve(entity)
	require.True(t, ok)
	_, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{PageSize: 500})
	require.Error(t, err)
}

func apiFullSyncMatrixFixtures() []apiFullSyncMatrixFixture {
	return []apiFullSyncMatrixFixture{
		apiServiceMatrixFixture(),
		apiRouteMatrixFixture(),
		apiUpstreamMatrixFixture(),
		apiRoleMatrixFixture(),
		userGroupRoleSetMatrixFixture(),
	}
}

func apiServiceMatrixFixture() apiFullSyncMatrixFixture {
	return apiFullSyncMatrixFixture{
		entity: events.EntityAPIService, idField: "id", dropSource: &models.APIService{},
		seed501: func(t *testing.T, _ dao.AdminMutation, db *gorm.DB) {
			rows := make([]models.APIService, 501)
			for i := range rows {
				rows[i] = models.APIService{Slug: fmt.Sprintf("service-%03d", i+1), Name: "Service", Status: 1}
			}
			require.NoError(t, db.CreateInBatches(rows, 100).Error)
		},
		deleteBehind: func(t *testing.T, db *gorm.DB) { require.NoError(t, db.Delete(&models.APIService{}, 1).Error) },
		insertLate: func(t *testing.T, m dao.AdminMutation, _ *gorm.DB) {
			require.NoError(t, m.APIService().Create(&models.APIService{Slug: "service-late", Name: "Late", Status: 1}))
		},
	}
}

func apiRouteMatrixFixture() apiFullSyncMatrixFixture {
	return apiFullSyncMatrixFixture{
		entity: events.EntityAPIRoute, idField: "id", dropSource: &models.APIRoute{},
		seed501: func(t *testing.T, m dao.AdminMutation, db *gorm.DB) {
			require.NoError(t, m.APIService().Create(&models.APIService{Slug: "route-service", Name: "Service", Status: 1}))
			require.NoError(t, m.APIBackend().Create(&models.APIBackend{APIServiceID: 1, Name: "primary"}))
			rows := make([]models.APIRoute, 501)
			for i := range rows {
				rows[i] = models.APIRoute{APIServiceID: 1, BackendID: 1, Slug: fmt.Sprintf("route-%03d", i+1), Status: 1}
			}
			require.NoError(t, db.CreateInBatches(rows, 100).Error)
		},
		deleteBehind: func(t *testing.T, db *gorm.DB) { require.NoError(t, db.Delete(&models.APIRoute{}, 1).Error) },
		insertLate: func(t *testing.T, m dao.AdminMutation, _ *gorm.DB) {
			require.NoError(t, m.APIRoute().Create(&models.APIRoute{APIServiceID: 1, BackendID: 1, Slug: "route-late", Status: 1}))
		},
	}
}

func apiUpstreamMatrixFixture() apiFullSyncMatrixFixture {
	return apiFullSyncMatrixFixture{
		entity: events.EntityAPIUpstream, idField: "id", dropSource: &models.APIUpstream{},
		seed501: func(t *testing.T, m dao.AdminMutation, db *gorm.DB) {
			require.NoError(t, m.APIService().Create(&models.APIService{Slug: "upstream-service", Name: "Service", Status: 1}))
			require.NoError(t, m.APIBackend().Create(&models.APIBackend{APIServiceID: 1, Name: "primary"}))
			rows := make([]models.APIUpstream, 501)
			for i := range rows {
				rows[i] = models.APIUpstream{BackendID: 1, Name: fmt.Sprintf("upstream-%03d", i+1), BaseURL: "https://example.com", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: 1}
			}
			require.NoError(t, db.CreateInBatches(rows, 100).Error)
		},
		deleteBehind: func(t *testing.T, db *gorm.DB) { require.NoError(t, db.Delete(&models.APIUpstream{}, 1).Error) },
		insertLate: func(t *testing.T, m dao.AdminMutation, _ *gorm.DB) {
			require.NoError(t, m.APIUpstream().Create(&models.APIUpstream{BackendID: 1, Name: "upstream-late", BaseURL: "https://example.com", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: 1}))
		},
	}
}

func apiRoleMatrixFixture() apiFullSyncMatrixFixture {
	return apiFullSyncMatrixFixture{
		entity: events.EntityAPIRole, idField: "id", dropSource: &models.Role{},
		seed501: func(t *testing.T, _ dao.AdminMutation, db *gorm.DB) {
			rows := make([]models.Role, 501)
			for i := range rows {
				rows[i] = models.Role{Key: fmt.Sprintf("role-%03d", i+1), Name: "Role", Status: consts.StatusEnabled}
			}
			require.NoError(t, db.CreateInBatches(rows, 100).Error)
		},
		deleteBehind: func(t *testing.T, db *gorm.DB) { require.NoError(t, db.Delete(&models.Role{}, 1).Error) },
		insertLate: func(t *testing.T, m dao.AdminMutation, _ *gorm.DB) {
			require.NoError(t, m.APIRBAC().CreateRole(&models.Role{Key: "role-late", Name: "Late", Status: 1}))
		},
	}
}

func userGroupRoleSetMatrixFixture() apiFullSyncMatrixFixture {
	return apiFullSyncMatrixFixture{
		entity: events.EntityUserGroupAPIRoleSet, idField: "principal_id", dropSource: &models.UserGroup{},
		seed501: func(t *testing.T, _ dao.AdminMutation, db *gorm.DB) {
			rows := make([]models.UserGroup, 500)
			for i := range rows {
				rows[i] = models.UserGroup{Name: fmt.Sprintf("matrix-group-%03d", i+2)}
			}
			require.NoError(t, db.CreateInBatches(rows, 100).Error)
		},
		deleteBehind: func(t *testing.T, db *gorm.DB) { require.NoError(t, db.Delete(&models.UserGroup{}, 1).Error) },
		insertLate: func(t *testing.T, m dao.AdminMutation, _ *gorm.DB) {
			require.NoError(t, m.UserGroup().Create(&models.UserGroup{Name: "matrix-group-late"}))
		},
	}
}
