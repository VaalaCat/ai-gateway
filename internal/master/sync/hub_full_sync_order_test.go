package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appcontainer "github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	fullSyncRecipientAgentID = "full-sync-recipient"
	fullSyncSubjectAgentID   = "full-sync-subject"
	fullSyncRuntimeAddress   = "http://203.0.113.20:8140"
	fullSyncManualAddress    = `[{"url":"https://manual.example/v1","tag":"public"}]`
	fullSyncDatabaseAddress  = `[{"url":"http://database.example:9000","tag":"auto-detected"}]`
)

type agentFullSyncFixture struct {
	hub              *Hub
	db               *gorm.DB
	client           *websocket.Conn
	sourceConn       *ws.Conn
	sourceGeneration uint64
}

func TestAgentFullSyncReturnsOnlyDatabaseConfiguredAddresses(t *testing.T) {
	for _, tc := range []struct {
		name               string
		databaseAddresses  string
		runtimeListenPorts []int
		wantAddresses      string
	}{
		{
			name:               "empty database address ignores runtime overlay",
			runtimeListenPorts: []int{8140},
			wantAddresses:      "",
		},
		{
			name:               "manual database address wins over runtime overlay",
			databaseAddresses:  fullSyncManualAddress,
			runtimeListenPorts: []int{8140},
			wantAddresses:      fullSyncManualAddress,
		},
		{
			name:               "runtime tombstone preserves original database value",
			databaseAddresses:  fullSyncDatabaseAddress,
			runtimeListenPorts: []int{8140, 0},
			wantAddresses:      fullSyncDatabaseAddress,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAgentFullSyncFixture(t, tc.databaseAddresses, tc.runtimeListenPorts...)
			response := callAgentFullSync(t, fixture.client, 1)
			agent := findFullSyncAgent(t, response.Items, fullSyncSubjectAgentID)

			require.Equal(t, protocol.AgentFullSyncSnapshotContractV1, response.SnapshotContract)
			require.Empty(t, agent.Secret)
			if tc.wantAddresses == "" {
				require.Empty(t, agent.HTTPAddresses)
				return
			}
			require.JSONEq(t, tc.wantAddresses, agent.HTTPAddresses)
		})
	}
}

func TestAgentFullSyncBaseVersion(t *testing.T) {
	t.Run("first page captures base before database query", func(t *testing.T) {
		fixture := newAgentFullSyncFixture(t, "")
		var version atomic.Int64
		version.Store(10)
		fixture.hub.GetVersion = version.Load

		queryEntered := make(chan struct{})
		releaseQuery := make(chan struct{})
		var queryOnce gosync.Once
		var releaseOnce gosync.Once
		release := func() { releaseOnce.Do(func() { close(releaseQuery) }) }
		t.Cleanup(release)
		require.NoError(t, fixture.db.Callback().Row().Before("gorm:row").Register(
			"test:block_agent_full_sync_max_id",
			func(tx *gorm.DB) {
				queryOnce.Do(func() {
					close(queryEntered)
					select {
					case <-releaseQuery:
					case <-tx.Statement.Context.Done():
					}
				})
			},
		))

		writeFullSyncRequest(t, fixture.client, 101, protocol.FullSyncRequest{
			Entity: events.EntityAgent, PageSize: 1,
		})
		waitFullSyncFixtureSignal(t, queryEntered, "Agent full-sync database query")
		version.Store(11)
		release()
		response := readFullSyncResponse(t, fixture.client, 101)

		require.Equal(t, int64(10), response.BaseVersion)
		require.Equal(t, int64(11), response.Version)
		require.Equal(t, protocol.AgentFullSyncSnapshotContractV1, response.SnapshotContract)
		require.Zero(t, response.Page)
		require.True(t, response.Keyset)
		require.True(t, response.HasMore)
	})

	t.Run("later keyset page preserves request base", func(t *testing.T) {
		fixture := newAgentFullSyncFixture(t, "")
		var version atomic.Int64
		version.Store(12)
		fixture.hub.GetVersion = version.Load

		response := callFullSync(t, fixture.client, 102, protocol.FullSyncRequest{
			Entity: events.EntityAgent, PageSize: 1, AfterID: 1, SnapshotMaxID: 2, BaseVersion: 10,
		})

		require.Equal(t, int64(10), response.BaseVersion)
		require.Equal(t, int64(12), response.Version)
		require.Equal(t, protocol.AgentFullSyncSnapshotContractV1, response.SnapshotContract)
		require.Zero(t, response.Page)
		require.True(t, response.Keyset)
		require.False(t, response.HasMore)
	})

	t.Run("later keyset page preserves a valid zero base", func(t *testing.T) {
		fixture := newAgentFullSyncFixture(t, "")
		var version atomic.Int64
		version.Store(12)
		fixture.hub.GetVersion = version.Load

		response := callFullSync(t, fixture.client, 104, protocol.FullSyncRequest{
			Entity: events.EntityAgent, PageSize: 1, AfterID: 1, SnapshotMaxID: 2, BaseVersion: 0,
		})

		require.Zero(t, response.BaseVersion)
		require.Equal(t, int64(12), response.Version)
		require.Equal(t, protocol.AgentFullSyncSnapshotContractV1, response.SnapshotContract)
		require.Zero(t, response.Page)
		require.True(t, response.Keyset)
	})

	t.Run("legacy offset page does not advertise snapshot contract", func(t *testing.T) {
		fixture := newAgentFullSyncFixture(t, "")
		response := callFullSync(t, fixture.client, 105, protocol.FullSyncRequest{
			Entity: events.EntityAgent, Page: 1, PageSize: 1,
		})

		require.False(t, response.Keyset)
		require.Empty(t, response.SnapshotContract)
		require.Zero(t, response.BaseVersion)
		require.Equal(t, 1, response.Page)
	})

	t.Run("non Agent entity keeps zero base", func(t *testing.T) {
		fixture := newAgentFullSyncFixture(t, "")
		var version atomic.Int64
		version.Store(20)
		fixture.hub.GetVersion = version.Load

		response := callFullSync(t, fixture.client, 103, protocol.FullSyncRequest{
			Entity: events.EntityToken, Page: 1, PageSize: 1, BaseVersion: 19,
		})

		require.Zero(t, response.BaseVersion)
		require.Equal(t, int64(20), response.Version)
		require.Empty(t, response.SnapshotContract)
	})
}

func newAgentFullSyncFixture(t *testing.T, databaseAddresses string, runtimeListenPorts ...int) *agentFullSyncFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.Agent{}, &models.Token{}))
	require.NoError(t, db.Create([]models.Agent{
		{AgentID: fullSyncRecipientAgentID, Secret: "recipient-secret", Name: "recipient", Status: 1},
		{
			AgentID: fullSyncSubjectAgentID, Secret: "subject-secret", Name: "subject", Status: 1,
			HTTPAddresses: databaseAddresses,
		},
	}).Error)

	application := appcontainer.NewApplication()
	application.SetDB(db)
	hub := NewHub(application, zap.NewNop(), nil, func() int64 { return 42 }, nil, HubOptions{MasterInstanceID: "master-a"})
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	server := httptest.NewServer(router)

	header := http.Header{}
	header.Set(consts.HeaderXAgentID, fullSyncRecipientAgentID)
	header.Set(consts.HeaderXAgentSecret, "recipient-secret")
	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/ws/agent",
		header,
	)
	require.NoError(t, err)
	waitFullSyncFixtureSignal(t, hub.sessionChange, "recipient control session install")

	sourceConn := &ws.Conn{}
	sourceGeneration, _, _ := hub.installControlSession(
		fullSyncSubjectAgentID,
		sourceConn,
		"203.0.113.20:2000",
	)
	require.NotZero(t, sourceGeneration)
	for _, listenPort := range runtimeListenPorts {
		hub.updateAutoDetectedAddress(fullSyncSubjectAgentID, sourceConn, sourceGeneration, listenPort)
	}
	if len(runtimeListenPorts) > 0 && runtimeListenPorts[len(runtimeListenPorts)-1] <= 0 {
		require.Empty(t, hub.GetAgentAddresses(fullSyncSubjectAgentID, ""))
	} else if len(runtimeListenPorts) > 0 {
		require.JSONEq(t, fmt.Sprintf(`[{"url":%q,"tag":"auto-detected"}]`, fullSyncRuntimeAddress),
			marshalAddresses(t, hub.GetAgentAddresses(fullSyncSubjectAgentID, "")))
	}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	fixture := &agentFullSyncFixture{
		hub:              hub,
		db:               db,
		client:           client,
		sourceConn:       sourceConn,
		sourceGeneration: sourceGeneration,
	}
	t.Cleanup(func() {
		hub.removeControlSession(fullSyncSubjectAgentID, sourceConn, sourceGeneration)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hub.Close(ctx)
		_ = client.Close()
		server.Close()
		_ = sqlDB.Close()
	})
	return fixture
}

func callAgentFullSync(t *testing.T, conn *websocket.Conn, id int64) protocol.FullSyncResponse {
	t.Helper()
	return callFullSync(t, conn, id, protocol.FullSyncRequest{
		Entity: events.EntityAgent, PageSize: protocol.FullSyncMaxPageSize,
	})
}

func callFullSync(
	t *testing.T,
	conn *websocket.Conn,
	id int64,
	request protocol.FullSyncRequest,
) protocol.FullSyncResponse {
	t.Helper()
	writeFullSyncRequest(t, conn, id, request)
	return readFullSyncResponse(t, conn, id)
}

func writeFullSyncRequest(t *testing.T, conn *websocket.Conn, id int64, request protocol.FullSyncRequest) {
	t.Helper()
	req, err := jsonrpc.NewRequest(consts.RPCSyncFullSync, request, &id)
	require.NoError(t, err)
	require.NoError(t, conn.WriteJSON(req))
}

func readFullSyncResponse(t *testing.T, conn *websocket.Conn, id int64) protocol.FullSyncResponse {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	var rpcResponse jsonrpc.Response
	require.NoError(t, conn.ReadJSON(&rpcResponse))
	require.Nil(t, rpcResponse.Error)
	require.NotNil(t, rpcResponse.ID)
	require.Equal(t, id, *rpcResponse.ID)
	var response protocol.FullSyncResponse
	require.NoError(t, json.Unmarshal(rpcResponse.Result, &response))
	return response
}

func findFullSyncAgent(t *testing.T, items json.RawMessage, agentID string) models.Agent {
	t.Helper()
	var agents []models.Agent
	require.NoError(t, json.Unmarshal(items, &agents))
	for _, agent := range agents {
		if agent.AgentID == agentID {
			return agent
		}
	}
	t.Fatalf("Agent full-sync omitted %q", agentID)
	return models.Agent{}
}

func marshalAddresses(t *testing.T, addresses any) string {
	t.Helper()
	data, err := json.Marshal(addresses)
	require.NoError(t, err)
	return string(data)
}

func waitFullSyncFixtureSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
