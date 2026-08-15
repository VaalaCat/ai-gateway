package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestPrepareDegradedCreatesClosedRecoveryDatabaseAndBacklog(t *testing.T) {
	root := newFixtureTestRoot(t, "degraded")
	require.NoError(t, prepare(root, ":8240", "http://localhost:8241", true))

	logInfo, err := os.Stat(filepath.Join(root, "log.db"))
	require.NoError(t, err)
	require.True(t, logInfo.Mode().IsRegular())
	require.Zero(t, logInfo.Mode().Perm())
	for _, suffix := range []string{"-wal", "-shm"} {
		_, err := os.Stat(filepath.Join(root, "log.db") + suffix)
		require.ErrorIs(t, err, os.ErrNotExist)
	}

	snapshot, err := deliveryqueue.ReadSnapshot[masterlogqueue.LogBatch](filepath.Join(root, "log_backlog.snapshot.gz"))
	require.NoError(t, err)
	require.Len(t, snapshot.Items, 2)
	core, err := masterdatabase.NewConnector().OpenExistingCorePath(filepath.Join(root, "core.db"))
	require.NoError(t, err)
	var tokens []models.Token
	require.NoError(t, core.Find(&tokens).Error)
	tokenOwners := make(map[uint]uint, len(tokens))
	for _, token := range tokens {
		tokenOwners[token.ID] = token.UserID
	}
	coreSQL, err := core.DB()
	require.NoError(t, err)
	require.NoError(t, coreSQL.Close())

	require.NoError(t, os.Chmod(filepath.Join(root, "log.db"), 0o600))
	db, err := masterdatabase.NewConnector().OpenExistingLogPath(filepath.Join(root, "log.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var tokenDailyCount, channelDailyCount int64
	require.NoError(t, db.Table("token_daily_billings").Count(&tokenDailyCount).Error)
	require.NoError(t, db.Table("channel_daily_billings").Count(&channelDailyCount).Error)
	require.Equal(t, int64(21), tokenDailyCount)
	require.Equal(t, int64(56), channelDailyCount)
	var tokenDailyRows []models.TokenDailyBilling
	require.NoError(t, db.Find(&tokenDailyRows).Error)
	for _, row := range tokenDailyRows {
		require.Equal(t, tokenOwners[row.TokenID], row.UserID)
	}
	var requestLogCount int64
	require.NoError(t, db.Table("request_logs").
		Where("agent_id = ? AND status = 0", "fixture-agent-1").Count(&requestLogCount).Error)
	require.Equal(t, int64(12), requestLogCount)
	var requestLogs []models.RequestLog
	require.NoError(t, db.Where("agent_id = ?", "fixture-agent-1").Find(&requestLogs).Error)
	for _, row := range requestLogs {
		require.Equal(t, tokenOwners[row.TokenID], row.UserID)
	}
	batches := make([]masterlogqueue.LogBatch, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		batches = append(batches, item.Item.Value)
	}
	require.NoError(t, (&masterlogqueue.LogBatchWriter{DBFinder: func() *gorm.DB { return db }}).Write(t.Context(), batches))
}

func TestPrepareSeedsLoginReadyIsolatedUsers(t *testing.T) {
	root := newFixtureTestRoot(t, "login-users")
	require.NoError(t, prepare(root, ":8140", "http://localhost:8141", false))
	_, err := config.LoadMaster(filepath.Join(root, "config.yaml"))
	require.NoError(t, err)
	core, err := masterdatabase.NewConnector().OpenExistingCorePath(filepath.Join(root, "core.db"))
	require.NoError(t, err)
	sqlDB, err := core.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, username := range []string{"fixture-alice", "fixture-bob"} {
		var user models.User
		require.NoError(t, core.Where("username = ?", username).First(&user).Error)
		require.Equal(t, consts.RoleUser, user.Role)
		require.Equal(t, consts.StatusEnabled, user.Status)
		require.Equal(t, uint(1), user.GroupID)
		require.True(t, user.PasswordSet)
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(fixtureUserPassword)))
		require.Error(t, bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("wrong-password")))
	}
}

func TestPrepareSeedsRouteWorkspaceFixture(t *testing.T) {
	root := newFixtureTestRoot(t, "route-workspace")
	require.NoError(t, prepare(root, ":8140", "http://localhost:8141", false))
	core, err := masterdatabase.NewConnector().OpenExistingCorePath(filepath.Join(root, "core.db"))
	require.NoError(t, err)
	sqlDB, err := core.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var service models.APIService
	require.NoError(t, core.First(&service, 101).Error)
	require.Equal(t, "route-workspace-e2e", service.Slug)
	require.Equal(t, "Route Workspace E2E", service.Name)

	var backend models.APIBackend
	require.NoError(t, core.First(&backend, 201).Error)
	require.Equal(t, service.ID, backend.APIServiceID)
	require.Equal(t, "Primary Target", backend.Name)

	var route models.APIRoute
	require.NoError(t, core.First(&route, 401).Error)
	require.Equal(t, service.ID, route.APIServiceID)
	require.Equal(t, backend.ID, route.BackendID)
	require.Equal(t, "responsive-route-workspace", route.Slug)
	require.Equal(t, []models.APIProtocol{models.APIProtocolHTTP}, []models.APIProtocol(route.Protocols))
	require.Equal(t, []string{"GET"}, []string(route.AllowedMethods))

	var upstreams []models.APIUpstream
	require.NoError(t, core.Where("backend_id = ?", backend.ID).Order("id").Find(&upstreams).Error)
	require.Len(t, upstreams, 2)
	require.Equal(t, []uint{301, 302}, []uint{upstreams[0].ID, upstreams[1].ID})
	require.Equal(t, []string{"Primary Endpoint", "Disabled Backup"}, []string{upstreams[0].Name, upstreams[1].Name})
	require.Equal(t, []int{consts.StatusEnabled, consts.StatusDisabled}, []int{upstreams[0].Status, upstreams[1].Status})
}

func TestValidateFixtureRootAcceptsDedicatedDirectTempChild(t *testing.T) {
	root := newFixtureTestRoot(t, "valid")
	canonicalTemp, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)

	got, err := validateFixtureRoot(root)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(canonicalTemp, filepath.Base(root)), got)
}

func TestValidateFixtureRootCanonicalizesSymlinkParent(t *testing.T) {
	realTemp, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)
	aliasParent := newFixtureParentAlias(t)

	for _, existing := range []bool{false, true} {
		name := "non-existing root"
		if existing {
			name = "existing root"
		}
		t.Run(name, func(t *testing.T) {
			basename := fmt.Sprintf("ai-gateway-chart-e2e-canonical-%t-%d", existing, time.Now().UnixNano())
			canonicalRoot := filepath.Join(realTemp, basename)
			aliasRoot := filepath.Join(aliasParent, basename)
			t.Cleanup(func() { _ = os.RemoveAll(canonicalRoot) })
			if existing {
				require.NoError(t, os.Mkdir(canonicalRoot, 0o700))
			}

			got, validateErr := validateFixtureRoot(aliasRoot)

			require.NoError(t, validateErr)
			require.Equal(t, canonicalRoot, got)
		})
	}
}

func TestValidateFixtureRootRejectsUnsafeBoundaries(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	for name, root := range map[string]string{
		"empty": "", "relative": "ai-gateway-chart-e2e-relative", "filesystem root": string(os.PathSeparator),
		"temp root": os.TempDir(), "cwd": cwd, "home": home,
		"wrong basename":    filepath.Join(os.TempDir(), "production-data"),
		"nested temp child": filepath.Join(os.TempDir(), "nested", "ai-gateway-chart-e2e-nested"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := validateFixtureRoot(root)
			require.Error(t, err)
		})
	}
}

func TestResetFixtureRootRejectsWorkingDirectoryInsideRootBeforeRemoval(t *testing.T) {
	originalCWD, err := os.Getwd()
	require.NoError(t, err)
	for _, nested := range []bool{false, true} {
		name := "root"
		if nested {
			name = "nested checkout"
		}
		t.Run(name, func(t *testing.T) {
			root := newFixtureTestRoot(t, "contains-cwd")
			require.NoError(t, os.Mkdir(root, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600))
			sentinel := filepath.Join(root, "must-survive.txt")
			require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))
			workingDirectory := root
			if nested {
				workingDirectory = filepath.Join(root, "checkout")
				require.NoError(t, os.Mkdir(workingDirectory, 0o700))
			}

			resetErr := func() error {
				require.NoError(t, os.Chdir(workingDirectory))
				defer func() { require.NoError(t, os.Chdir(originalCWD)) }()
				_, err := resetFixtureRoot(root)
				return err
			}()

			require.ErrorContains(t, resetErr, "working directory")
			require.FileExists(t, sentinel)
		})
	}
}

func TestResetFixtureRootRejectsNestedWorkingDirectoryThroughSymlinkParentBeforeRemoval(t *testing.T) {
	originalCWD, err := os.Getwd()
	require.NoError(t, err)
	root := newFixtureTestRoot(t, "alias-contains-cwd")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600))
	sentinel := filepath.Join(root, "must-survive.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))
	workingDirectory := filepath.Join(root, "checkout")
	require.NoError(t, os.Mkdir(workingDirectory, 0o700))
	aliasRoot := filepath.Join(newFixtureParentAlias(t), filepath.Base(root))

	resetErr := func() error {
		require.NoError(t, os.Chdir(workingDirectory))
		defer func() { require.NoError(t, os.Chdir(originalCWD)) }()
		_, err := resetFixtureRoot(aliasRoot)
		return err
	}()

	require.ErrorContains(t, resetErr, "working directory")
	require.FileExists(t, sentinel)
}

func TestResetFixtureRootRejectsHomeInsideRootBeforeRemoval(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "root is home"
		if nested {
			name = "root contains home"
		}
		t.Run(name, func(t *testing.T) {
			root := newFixtureTestRoot(t, "contains-home")
			require.NoError(t, os.Mkdir(root, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600))
			sentinel := filepath.Join(root, "must-survive.txt")
			require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))
			home := root
			if nested {
				home = filepath.Join(root, "home")
				require.NoError(t, os.Mkdir(home, 0o700))
			}
			t.Setenv("HOME", home)

			_, resetErr := resetFixtureRoot(root)

			require.ErrorContains(t, resetErr, "home")
			require.FileExists(t, sentinel)
		})
	}
}

func TestResetFixtureRootRejectsNestedHomeThroughSymlinkParentBeforeRemoval(t *testing.T) {
	root := newFixtureTestRoot(t, "alias-contains-home")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, fixtureMarkerName), []byte(fixtureMarkerContents), 0o600))
	sentinel := filepath.Join(root, "must-survive.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))
	home := filepath.Join(root, "home")
	require.NoError(t, os.Mkdir(home, 0o700))
	t.Setenv("HOME", home)
	aliasRoot := filepath.Join(newFixtureParentAlias(t), filepath.Base(root))

	_, resetErr := resetFixtureRoot(aliasRoot)

	require.ErrorContains(t, resetErr, "home")
	require.FileExists(t, sentinel)
}

func TestValidateFixtureRootRejectsSymlink(t *testing.T) {
	target := newFixtureTestRoot(t, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(os.TempDir(), fmt.Sprintf("ai-gateway-chart-e2e-link-%d", time.Now().UnixNano()))
	require.NoError(t, os.Symlink(target, link))
	t.Cleanup(func() { _ = os.Remove(link) })

	_, err := validateFixtureRoot(link)

	require.ErrorContains(t, err, "symlink")
}

func TestPrepareRefusesExistingDirectoryWithoutMarker(t *testing.T) {
	root := newFixtureTestRoot(t, "unmarked")
	require.NoError(t, os.Mkdir(root, 0o700))
	payload := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(payload, []byte("keep"), 0o600))

	err := prepare(root, ":8140", "http://localhost:8141", false)

	require.ErrorContains(t, err, "marker")
	got, readErr := os.ReadFile(payload)
	require.NoError(t, readErr)
	require.Equal(t, "keep", string(got))
}

func TestPrepareRebuildsDirectoryWithMatchingMarker(t *testing.T) {
	root := newFixtureTestRoot(t, "marked")
	require.NoError(t, prepare(root, ":8140", "http://localhost:8141", false))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stale.txt"), []byte("stale"), 0o600))

	require.NoError(t, prepare(root, ":8140", "http://localhost:8141", false))

	_, err := os.Stat(filepath.Join(root, "stale.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	marker, err := os.ReadFile(filepath.Join(root, fixtureMarkerName))
	require.NoError(t, err)
	require.Equal(t, fixtureMarkerContents, string(marker))
}

func newFixtureTestRoot(t *testing.T, suffix string) string {
	t.Helper()
	root := filepath.Join(os.TempDir(), fmt.Sprintf("ai-gateway-chart-e2e-%s-%d", suffix, time.Now().UnixNano()))
	require.True(t, strings.HasPrefix(filepath.Base(root), "ai-gateway-chart-e2e"))
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(root, "log.db"), 0o600)
		_ = os.RemoveAll(root)
	})
	return root
}

func newFixtureParentAlias(t *testing.T) string {
	t.Helper()
	realTemp, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)
	alias := filepath.Join(os.TempDir(), fmt.Sprintf("ai-gateway-chart-e2e-parent-alias-%d", time.Now().UnixNano()))
	require.NoError(t, os.Symlink(realTemp, alias))
	t.Cleanup(func() { _ = os.Remove(alias) })
	return alias
}
