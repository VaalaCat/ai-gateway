package master

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	apisystem "github.com/VaalaCat/ai-gateway/internal/master/api/system"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterhistorybackfill "github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestHistoryMigrationCompletesRestartsDeletesSourceAndRestarts(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	createLegacyDatabase(t, cfg.Master.LegacyDBPath)

	first := startHistoryMigrationServer(t, cfg)
	require.Eventually(t, func() bool {
		return first.server.HistoryBackfillWorker.Status().State == masterhistorybackfill.StateCaughtUp
	}, 5*time.Second, 10*time.Millisecond)
	token := loginHistoryMigrationAdmin(t, first.baseURL)
	requestHistoryMigrationAPI(t, first.baseURL, token, http.MethodPost,
		"/api/admin/system/history-backfill/complete", map[string]bool{"confirm": true}, nil)
	completed := first.server.HistoryBackfillWorker.Status()
	require.Equal(t, masterhistorybackfill.StateCompleted, completed.State)
	require.Positive(t, completed.Billing.ProcessedRows)
	require.Positive(t, completed.Requests.ProcessedRows)
	first.stop(t)

	second := startHistoryMigrationServer(t, cfg)
	restarted := second.server.HistoryBackfillWorker.Status()
	require.Equal(t, masterhistorybackfill.StateCompleted, restarted.State)
	require.Equal(t, completed.Billing, restarted.Billing)
	require.Equal(t, completed.Requests, restarted.Requests)
	require.Equal(t, completed.Traces, restarted.Traces)
	require.FileExists(t, cfg.Master.LegacyDBPath)
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath+"-wal", []byte("wal"), 0o600))
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath+"-shm", []byte("shm"), 0o600))
	token = loginHistoryMigrationAdmin(t, second.baseURL)
	requestHistoryMigrationAPI(t, second.baseURL, token, http.MethodDelete,
		"/api/admin/system/history-backfill/source?confirmation=DELETE", nil, nil)
	require.Equal(t, masterhistorybackfill.StateSourceDeleted, second.server.HistoryBackfillWorker.Status().State)
	for _, path := range []string{cfg.Master.LegacyDBPath, cfg.Master.LegacyDBPath + "-wal", cfg.Master.LegacyDBPath + "-shm"} {
		require.NoFileExists(t, path)
	}
	require.NoError(t, masterdatabase.QuickCheck(second.server.DB))
	require.NoError(t, masterdatabase.QuickCheck(second.server.App.GetLogDB()))
	second.stop(t)

	third := startHistoryMigrationServer(t, cfg)
	require.Equal(t, masterhistorybackfill.StateSourceDeleted, third.server.HistoryBackfillWorker.Status().State)
	require.NoFileExists(t, cfg.Master.LegacyDBPath)
	third.stop(t)
}

func TestV5LegacyArtifactDeleteLeavesHistoryMigrationUnchanged(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	legacy := openMigratedCore(t, cfg.Master.LegacyDBPath)
	require.NoError(t, legacy.Create(&models.User{ID: 7, Username: "v5", Quota: 100}).Error)
	require.NoError(t, legacy.Create(&models.BillingLog{RequestID: "v5-billing", CreatedAt: 1}).Error)
	closeGormDatabase(legacy)
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	require.NoError(t, os.WriteFile(artifactPath, []byte("independent legacy artifact"), 0o600))
	manifest := fmt.Sprintf(`{"paths":{"backup_core":%q}}`, artifactPath)
	require.NoError(t, os.WriteFile(masterdatabase.SplitManifestPath(cfg.Master.LegacyDBPath), []byte(manifest), 0o600))

	running := startHistoryMigrationServer(t, cfg)
	require.Eventually(t, func() bool {
		return running.server.HistoryBackfillWorker.Status().State == masterhistorybackfill.StateCaughtUp
	}, 5*time.Second, 10*time.Millisecond)
	before := running.server.HistoryBackfillWorker.Status()
	token := loginHistoryMigrationAdmin(t, running.baseURL)
	var stats apisystem.StatsResponse
	requestHistoryMigrationAPI(t, running.baseURL, token, http.MethodGet, "/api/admin/system/stats", nil, &stats)
	require.Equal(t, artifactPath, stats.Storage.LegacyArtifact.Path)
	require.True(t, stats.Storage.LegacyArtifact.Exists)
	require.True(t, stats.Storage.LegacyArtifact.Available)
	require.True(t, stats.Storage.LegacyArtifact.CanDelete)

	requestHistoryMigrationAPI(t, running.baseURL, token, http.MethodDelete,
		"/api/admin/system/history-backfill/legacy-artifact?confirmation=DELETE", nil, nil)
	require.NoFileExists(t, artifactPath)
	after := running.server.HistoryBackfillWorker.Status()
	require.Equal(t, before.State, after.State)
	require.Equal(t, before.SourceKind, after.SourceKind)
	require.Equal(t, before.SourcePath, after.SourcePath)
	require.Equal(t, before.Billing, after.Billing)
	require.Equal(t, before.Requests, after.Requests)
	require.Equal(t, before.Traces, after.Traces)
	require.FileExists(t, cfg.Master.LegacyDBPath)
	running.stop(t)
}

type runningHistoryMigrationServer struct {
	server  *Server
	baseURL string
	runDone <-chan error
}

func startHistoryMigrationServer(t *testing.T, cfg *config.MasterRuntimeConfig) *runningHistoryMigrationServer {
	t.Helper()
	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	serveStarted := make(chan struct{})
	srv.afterHTTPServeStarted = func() { close(serveStarted) }
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-serveStarted:
	case err := <-runDone:
		t.Fatalf("master stopped before HTTP readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("master did not become HTTP ready")
	}
	listenAddress, ok := srv.ListenAddress()
	require.True(t, ok)
	baseURL := "http://" + listenAddress
	response, err := http.Get(baseURL + "/ping")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	return &runningHistoryMigrationServer{server: srv, baseURL: baseURL, runDone: runDone}
}

func loginHistoryMigrationAdmin(t *testing.T, baseURL string) string {
	t.Helper()
	var response struct {
		Token string `json:"token"`
	}
	requestHistoryMigrationAPI(t, baseURL, "", http.MethodPost, "/api/login", map[string]string{
		"username": "admin", "password": "admin123",
	}, &response)
	require.NotEmpty(t, response.Token)
	return response.Token
}

func requestHistoryMigrationAPI(t *testing.T, baseURL, token, method, path string, body, response any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(data))
	if response != nil {
		require.NoError(t, json.Unmarshal(data, response))
	}
}

func (s *runningHistoryMigrationServer) stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, s.server.Shutdown(ctx))
	require.ErrorIs(t, <-s.runDone, http.ErrServerClosed)
}
