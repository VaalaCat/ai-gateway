package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSetupTestDBClosesOwnedDatabaseAfterTestCleanup(t *testing.T) {
	var ping func() error
	t.Run("fixture", func(t *testing.T) {
		db := setupTestDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Ping())
		ping = sqlDB.Ping
	})

	require.NotNil(t, ping)
	require.Error(t, ping())
}

func TestNormalizeRelayConfigurationDefaultsCreateModeAndPreservesValidStoredURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		mode             string
		uri              string
		defaultEmptyMode bool
		wantMode         string
		wantURI          string
	}{
		{name: "create default", defaultEmptyMode: true, wantMode: "inherit"},
		{name: "custom", mode: "custom", uri: "  WSS://relay.example/tunnel?token=secret  ", wantMode: "custom", wantURI: "wss://relay.example/tunnel?token=secret"},
		{name: "inherit keeps URI", mode: "inherit", uri: "ws://relay.example/standby", wantMode: "inherit", wantURI: "ws://relay.example/standby"},
		{name: "disabled keeps URI", mode: "disabled", uri: "wss://relay.example/standby", wantMode: "disabled", wantURI: "wss://relay.example/standby"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mode, uri, err := normalizeRelayConfiguration(tt.mode, tt.uri, tt.defaultEmptyMode)
			require.NoError(t, err)
			require.Equal(t, tt.wantMode, mode)
			require.Equal(t, tt.wantURI, uri)
		})
	}
}

func TestNormalizeRelayConfigurationRejectsInvalidFinalStateWithoutLeakingURI(t *testing.T) {
	t.Parallel()

	const secret = "agent-relay-secret"
	tests := map[string]struct {
		mode             string
		uri              string
		defaultEmptyMode bool
	}{
		"empty update mode":    {mode: ""},
		"unknown mode":         {mode: "automatic"},
		"custom without URI":   {mode: "custom"},
		"custom invalid URI":   {mode: "custom", uri: "http://relay.example/?token=" + secret},
		"inherit invalid URI":  {mode: "inherit", uri: "wss://user:pass@relay.example/?token=" + secret},
		"disabled invalid URI": {mode: "disabled", uri: "wss://relay.example/?token=" + secret + "#fragment"},
		"whitespace URI":       {mode: "inherit", uri: "   "},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := normalizeRelayConfiguration(tt.mode, tt.uri, tt.defaultEmptyMode)
			require.EqualError(t, err, "invalid relay configuration")
			require.NotContains(t, err.Error(), secret)
			if tt.uri != "" {
				require.NotContains(t, err.Error(), tt.uri)
			}
		})
	}
}

func TestMergeAgentPatchBuildsOnlyExplicitUpdatesIncludingZeroAndEmpty(t *testing.T) {
	t.Parallel()

	current := models.Agent{
		ID:            7,
		AgentID:       "agent-7",
		Secret:        "secret-7",
		Name:          "old name",
		Status:        1,
		HTTPAddresses: "old addresses",
		Tags:          "old tags",
		ProxyURL:      "http://proxy.example",
		RelayMode:     "custom",
		RelayURI:      "wss://relay.example/original",
		PeerRouteMode: "direct_first",
	}

	merged, updates, err := mergeAgentPatch(current, AgentPatch{
		Name:          agentPtr(""),
		Status:        agentPtr(0),
		Tags:          agentPtr(""),
		HTTPAddresses: agentPtr(""),
		ProxyURL:      agentPtr(""),
		RelayMode:     agentPtr("disabled"),
		RelayURI:      agentPtr(""),
		PeerRouteMode: agentPtr("relay_only"),
	})
	require.NoError(t, err)
	require.Equal(t, "", merged.Name)
	require.Zero(t, merged.Status)
	require.Equal(t, "", merged.Tags)
	require.Equal(t, "", merged.HTTPAddresses)
	require.Equal(t, "", merged.ProxyURL)
	require.Equal(t, "disabled", merged.RelayMode)
	require.Equal(t, "", merged.RelayURI)
	require.Equal(t, "relay_only", merged.PeerRouteMode)
	require.Equal(t, "agent-7", merged.AgentID)
	require.Equal(t, "secret-7", merged.Secret)
	require.Equal(t, map[string]any{
		"name":            "",
		"status":          0,
		"tags":            "",
		"http_addresses":  "",
		"proxy_url":       "",
		"relay_mode":      "disabled",
		"relay_uri":       "",
		"peer_route_mode": "relay_only",
	}, updates)
}

func TestNormalizePeerRouteModeDefaultsAndRejectsUnknown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		mode         string
		defaultEmpty bool
		want         string
		wantErr      string
	}{
		{name: "create defaults empty", defaultEmpty: true, want: "direct_first"},
		{name: "direct first", mode: "direct_first", want: "direct_first"},
		{name: "relay only", mode: "relay_only", want: "relay_only"},
		{name: "patch rejects empty", wantErr: "invalid peer route mode"},
		{name: "rejects unknown", mode: "automatic", defaultEmpty: true, wantErr: "invalid peer route mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizePeerRouteMode(test.mode, test.defaultEmpty)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestMergeAgentPatchPreservesOmittedFieldsAndValidatesFinalRelayState(t *testing.T) {
	t.Parallel()

	current := models.Agent{
		ID:            8,
		AgentID:       "agent-8",
		Name:          "kept name",
		Status:        1,
		HTTPAddresses: "kept addresses",
		Tags:          "kept tags",
		ProxyURL:      "http://proxy.example",
		RelayMode:     "custom",
		RelayURI:      "wss://relay.example/kept",
	}

	merged, updates, err := mergeAgentPatch(current, AgentPatch{})
	require.NoError(t, err)
	require.Equal(t, current, merged)
	require.Empty(t, updates)

	merged, updates, err = mergeAgentPatch(current, AgentPatch{RelayMode: agentPtr("custom")})
	require.NoError(t, err)
	require.Equal(t, current.RelayURI, merged.RelayURI)
	require.Equal(t, map[string]any{"relay_mode": "custom"}, updates)

	for _, mode := range []string{"inherit", "disabled"} {
		mode := mode
		t.Run(mode+" keeps stored URI", func(t *testing.T) {
			merged, updates, err := mergeAgentPatch(current, AgentPatch{RelayMode: &mode})
			require.NoError(t, err)
			require.Equal(t, current.RelayURI, merged.RelayURI)
			require.NotContains(t, updates, "relay_uri")
		})
	}
}

func TestMergeAgentPatchRejectsInvalidStatusAndRelayFinalState(t *testing.T) {
	t.Parallel()

	current := models.Agent{Status: 1, RelayMode: "custom", RelayURI: "wss://relay.example/kept"}
	tests := map[string]AgentPatch{
		"invalid status":          {Status: agentPtr(2)},
		"invalid mode":            {RelayMode: agentPtr("automatic")},
		"invalid peer route mode": {PeerRouteMode: agentPtr("direct_only")},
		"clear custom URI":        {RelayURI: agentPtr("")},
		"invalid replacement URI": {RelayURI: agentPtr("http://relay.example/?token=secret")},
	}
	for name, patch := range tests {
		name, patch := name, patch
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := mergeAgentPatch(current, patch)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}

	invalidStored := current
	invalidStored.RelayURI = "garbage?token=secret"
	_, _, err := mergeAgentPatch(invalidStored, AgentPatch{Name: agentPtr("unrelated")})
	require.EqualError(t, err, "invalid relay configuration")
}

func agentPtr[T any](value T) *T {
	return &value
}

func relayURIWithCanonicalStorageOverflow() string {
	const prefix = "wss://relay.example/"
	return prefix + strings.Repeat("a", 2048-len(prefix)-len("界")) + "界"
}

func relayURIAtCanonicalStorageBoundary() (string, string) {
	const prefix = "wss://relay.example/"
	padding := strings.Repeat("a", 2048-len(prefix)-len("%E7%95%8C"))
	return prefix + padding + "界", prefix + padding + "%E7%95%8C"
}

type agentRecordingBus struct {
	*eventbus.MemoryBus
	mu       sync.Mutex
	contexts []context.Context
	events   []eventbus.Event
}

func newAgentRecordingBus() *agentRecordingBus {
	return &agentRecordingBus{MemoryBus: eventbus.NewMemoryBus()}
}

func (b *agentRecordingBus) Publish(ctx context.Context, event eventbus.Event) error {
	b.mu.Lock()
	b.contexts = append(b.contexts, ctx)
	b.events = append(b.events, event)
	b.mu.Unlock()
	return b.MemoryBus.Publish(ctx, event)
}

func (b *agentRecordingBus) snapshotEvents() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]eventbus.Event(nil), b.events...)
}

func newAgentMutationContext(t *testing.T, method string) (*app.Context, *agentRecordingBus) {
	t.Helper()
	c := newTestContext(t, setupTestDB(t))
	c.Request = httptest.NewRequest(method, "/api/admin/agents", nil)
	bus := newAgentRecordingBus()
	c.App.SetEventBus(bus)
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	return c, bus
}

func TestCreateRelayConfigurationDefaultsAndValidatesBeforePersistence(t *testing.T) {
	boundaryRaw, boundaryCanonical := relayURIAtCanonicalStorageBoundary()
	tests := []struct {
		name     string
		mode     string
		uri      string
		wantMode string
		wantURI  string
		wantBad  bool
	}{
		{name: "omitted mode defaults", wantMode: "inherit"},
		{name: "custom", mode: "custom", uri: "  WSS://relay.example/tunnel?token=secret  ", wantMode: "custom", wantURI: "wss://relay.example/tunnel?token=secret"},
		{name: "canonical storage boundary", mode: "custom", uri: boundaryRaw, wantMode: "custom", wantURI: boundaryCanonical},
		{name: "disabled retains valid URI", mode: "disabled", uri: "ws://relay.example/standby", wantMode: "disabled", wantURI: "ws://relay.example/standby"},
		{name: "custom requires URI", mode: "custom", wantBad: true},
		{name: "rejects mode", mode: "automatic", wantBad: true},
		{name: "inherit rejects invalid URI", mode: "inherit", uri: "http://relay.example/?token=secret", wantBad: true},
		{name: "rejects empty fragment delimiter", mode: "custom", uri: "wss://relay.example/tunnel#", wantBad: true},
		{name: "rejects canonical storage overflow", mode: "custom", uri: relayURIWithCanonicalStorageOverflow(), wantBad: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			c, bus := newAgentMutationContext(t, http.MethodPost)
			created, err := (&Handler{}).Create(c, CreateRequest{
				AgentID:   "agent-create",
				Secret:    "secret-create",
				Name:      "created agent",
				RelayMode: tt.mode,
				RelayURI:  tt.uri,
			})
			if tt.wantBad {
				apiErr := requireAPIError(t, err)
				require.Equal(t, http.StatusBadRequest, apiErr.Status)
				require.Equal(t, "invalid relay configuration", apiErr.Message)
				require.NotContains(t, apiErr.Message, "secret")
				var count int64
				require.NoError(t, c.App.GetDB().Model(&models.Agent{}).Count(&count).Error)
				require.Zero(t, count)
				require.Empty(t, bus.events)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantMode, created.Value.RelayMode)
			require.Equal(t, tt.wantURI, created.Value.RelayURI)
			var stored models.Agent
			require.NoError(t, c.App.GetDB().First(&stored, created.Value.ID).Error)
			require.Equal(t, tt.wantMode, stored.RelayMode)
			require.Equal(t, tt.wantURI, stored.RelayURI)
			require.LessOrEqual(t, len(stored.RelayURI), 2048)
		})
	}
}

type agentMutationContextKey struct{}

func TestCreatePublishesCompletePersistedAgentWithRequestContext(t *testing.T) {
	c, bus := newAgentMutationContext(t, http.MethodPost)
	const marker = "agent-create-request"
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), agentMutationContextKey{}, marker))

	created, err := (&Handler{}).Create(c, CreateRequest{
		AgentID:       "event-agent",
		Secret:        "event-secret",
		Name:          "event name",
		HTTPAddresses: "addresses",
		Tags:          "tags",
		ProxyURL:      "http://proxy.example",
		RelayMode:     "custom",
		RelayURI:      "wss://relay.example/event?token=secret",
	})
	require.NoError(t, err)
	var stored models.Agent
	require.NoError(t, c.App.GetDB().First(&stored, created.Value.ID).Error)
	require.Equal(t, stored, created.Value)
	require.Len(t, bus.events, 1)
	require.Equal(t, events.AgentCreateTopic.Value(), bus.events[0].Topic)
	require.Len(t, bus.contexts, 1)
	require.Equal(t, marker, bus.contexts[0].Value(agentMutationContextKey{}))
	var payload models.Agent
	require.NoError(t, json.Unmarshal(bus.events[0].Payload, &payload))
	require.Equal(t, stored, payload)
}

func TestCreateConflictDoesNotExposeDatabaseError(t *testing.T) {
	c, bus := newAgentMutationContext(t, http.MethodPost)
	req := CreateRequest{AgentID: "duplicate-agent", Secret: "secret", Name: "agent"}
	_, err := (&Handler{}).Create(c, req)
	require.NoError(t, err)

	_, err = (&Handler{}).Create(c, req)
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, "create agent failed", apiErr.Message)
	require.NotContains(t, apiErr.Message, "UNIQUE")
	require.NotContains(t, apiErr.Message, "agents")
	require.Len(t, bus.events, 1, "failed create must not publish another event")
}

func TestUpdatePersistsExplicitValuesPreservesOmissionsAndPublishesCompleteAgent(t *testing.T) {
	c, bus := newAgentMutationContext(t, http.MethodPut)
	const marker = "agent-update-request"
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), agentMutationContextKey{}, marker))
	current := models.Agent{
		AgentID:       "update-agent",
		Secret:        "update-secret",
		Name:          "old name",
		Status:        1,
		HTTPAddresses: "old addresses",
		Tags:          "old tags",
		ProxyURL:      "http://proxy.example",
		RelayMode:     "custom",
		RelayURI:      "wss://relay.example/kept",
		PeerRouteMode: "direct_first",
	}
	require.NoError(t, c.App.GetDB().Create(&current).Error)

	updated, err := (&Handler{}).Update(c, UpdateRequest{
		ID: strconv.Itoa(int(current.ID)),
		AgentPatch: AgentPatch{
			Name:          agentPtr(""),
			Status:        agentPtr(0),
			Tags:          agentPtr(""),
			HTTPAddresses: agentPtr(""),
			RelayMode:     agentPtr("disabled"),
			PeerRouteMode: agentPtr("relay_only"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, "", updated.Name)
	require.Zero(t, updated.Status)
	require.Equal(t, "", updated.Tags)
	require.Equal(t, "", updated.HTTPAddresses)
	require.Equal(t, "http://proxy.example", updated.ProxyURL, "omitted proxy_url must be preserved")
	require.Equal(t, "disabled", updated.RelayMode)
	require.Equal(t, "wss://relay.example/kept", updated.RelayURI)
	require.Equal(t, "relay_only", updated.PeerRouteMode)

	var stored models.Agent
	require.NoError(t, c.App.GetDB().First(&stored, current.ID).Error)
	require.Equal(t, stored, updated)
	require.Len(t, bus.events, 1)
	require.Equal(t, events.AgentUpdateTopic.Value(), bus.events[0].Topic)
	require.Equal(t, marker, bus.contexts[0].Value(agentMutationContextKey{}))
	var payload models.Agent
	require.NoError(t, json.Unmarshal(bus.events[0].Payload, &payload))
	require.Equal(t, stored, payload)
}

func TestUpdateExplicitEmptyProxyURLClearsPersistedValue(t *testing.T) {
	c, _ := newAgentMutationContext(t, http.MethodPut)
	current := models.Agent{
		AgentID:   "clear-proxy-agent",
		Name:      "agent",
		Status:    1,
		ProxyURL:  "http://proxy.example",
		RelayMode: "inherit",
	}
	require.NoError(t, c.App.GetDB().Create(&current).Error)

	updated, err := (&Handler{}).Update(c, UpdateRequest{
		ID:         strconv.Itoa(int(current.ID)),
		AgentPatch: AgentPatch{ProxyURL: agentPtr("")},
	})
	require.NoError(t, err)
	require.Empty(t, updated.ProxyURL)
	var stored models.Agent
	require.NoError(t, c.App.GetDB().First(&stored, current.ID).Error)
	require.Empty(t, stored.ProxyURL)
}

func TestUpdatePersistsCanonicalRelayURIAtStorageBoundary(t *testing.T) {
	c, bus := newAgentMutationContext(t, http.MethodPut)
	current := models.Agent{
		AgentID:   "relay-boundary-agent",
		Name:      "agent",
		Status:    1,
		RelayMode: "custom",
		RelayURI:  "wss://relay.example/original",
	}
	require.NoError(t, c.App.GetDB().Create(&current).Error)
	raw, canonical := relayURIAtCanonicalStorageBoundary()

	updated, err := (&Handler{}).Update(c, UpdateRequest{
		ID:         strconv.Itoa(int(current.ID)),
		AgentPatch: AgentPatch{RelayURI: &raw},
	})
	require.NoError(t, err)
	require.Equal(t, canonical, updated.RelayURI)
	require.Len(t, updated.RelayURI, 2048)
	var stored models.Agent
	require.NoError(t, c.App.GetDB().First(&stored, current.ID).Error)
	require.Equal(t, canonical, stored.RelayURI)
	require.Len(t, bus.snapshotEvents(), 1)
}

func TestUpdateRejectsInvalidFinalStateBeforePersistenceOrPublish(t *testing.T) {
	tests := map[string]AgentPatch{
		"invalid status":             {Status: agentPtr(2)},
		"invalid mode":               {RelayMode: agentPtr("automatic")},
		"invalid peer route mode":    {PeerRouteMode: agentPtr("direct_only")},
		"clear custom URI":           {RelayURI: agentPtr("")},
		"invalid replacement URI":    {RelayURI: agentPtr("http://relay.example/?token=secret")},
		"empty fragment delimiter":   {RelayURI: agentPtr("wss://relay.example/tunnel#")},
		"canonical storage overflow": {RelayURI: agentPtr(relayURIWithCanonicalStorageOverflow())},
	}

	for name, patch := range tests {
		name, patch := name, patch
		t.Run(name, func(t *testing.T) {
			c, bus := newAgentMutationContext(t, http.MethodPut)
			current := models.Agent{
				AgentID:   "invalid-update-agent",
				Name:      "unchanged",
				Status:    1,
				RelayMode: "custom",
				RelayURI:  "wss://relay.example/kept",
			}
			require.NoError(t, c.App.GetDB().Create(&current).Error)

			_, err := (&Handler{}).Update(c, UpdateRequest{
				ID:         strconv.Itoa(int(current.ID)),
				AgentPatch: patch,
			})
			apiErr := requireAPIError(t, err)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
			require.NotContains(t, apiErr.Message, "secret")
			var stored models.Agent
			require.NoError(t, c.App.GetDB().First(&stored, current.ID).Error)
			require.Equal(t, current, stored)
			require.Empty(t, bus.events)
		})
	}
}

func TestUpdateRelayPartialPatchesUseCASAcrossSQLiteModes(t *testing.T) {
	tests := map[string]bool{
		"memory single connection": false,
		"file WAL":                 true,
	}

	for name, fileBacked := range tests {
		name, fileBacked := name, fileBacked
		t.Run(name, func(t *testing.T) {
			db := openRelayConcurrencyTestDB(t, fileBacked)
			bus := newAgentRecordingBus()
			t.Cleanup(func() { require.NoError(t, bus.Close()) })

			current := models.Agent{
				AgentID:   "concurrent-relay-agent",
				Name:      "before",
				Status:    1,
				RelayMode: "inherit",
				RelayURI:  "wss://relay.example/original",
			}
			require.NoError(t, db.Create(&current).Error)

			deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.Cleanup(cancel)
			arrived := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseReads := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseReads)
			var initialQueries atomic.Int32
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(
				"test:relay_initial_read_barrier",
				func(tx *gorm.DB) {
					if tx.Statement.Table != "agents" || tx.Error != nil || initialQueries.Add(1) > 2 {
						return
					}
					select {
					case arrived <- struct{}{}:
					case <-deadline.Done():
						tx.AddError(deadline.Err())
						return
					}
					select {
					case <-release:
					case <-deadline.Done():
						tx.AddError(deadline.Err())
					}
				},
			))

			type updateResult struct {
				agent models.Agent
				err   error
			}
			results := make(chan updateResult, 2)
			newUpdateContext := func() *app.Context {
				c := newTestContext(t, db)
				c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/agents/"+strconv.Itoa(int(current.ID)), nil)
				c.App.SetEventBus(bus)
				return c
			}
			modeContext := newUpdateContext()
			uriContext := newUpdateContext()
			runUpdate := func(c *app.Context, patch AgentPatch) {
				agent, err := (&Handler{}).Update(c, UpdateRequest{
					ID:         strconv.Itoa(int(current.ID)),
					AgentPatch: patch,
				})
				results <- updateResult{agent: agent, err: err}
			}
			go runUpdate(modeContext, AgentPatch{RelayMode: agentPtr("custom")})
			go runUpdate(uriContext, AgentPatch{RelayURI: agentPtr("")})

			for i := 0; i < 2; i++ {
				select {
				case <-arrived:
				case <-deadline.Done():
					t.Fatalf("initial agent reads did not reach barrier: %v", deadline.Err())
				}
			}
			releaseReads()

			successes, conflicts := 0, 0
			for i := 0; i < 2; i++ {
				select {
				case result := <-results:
					if result.err == nil {
						successes++
						continue
					}
					apiErr := requireAPIError(t, result.err)
					require.Equal(t, http.StatusConflict, apiErr.Status)
					require.Equal(t, "agent was modified concurrently", apiErr.Message)
					conflicts++
				case <-deadline.Done():
					t.Fatalf("concurrent updates did not finish: %v", deadline.Err())
				}
			}
			require.Equal(t, 1, successes)
			require.Equal(t, 1, conflicts)

			var stored models.Agent
			require.NoError(t, db.First(&stored, current.ID).Error)
			switch stored.RelayMode {
			case "custom":
				require.Equal(t, current.RelayURI, stored.RelayURI)
			case "inherit":
				require.Empty(t, stored.RelayURI)
			default:
				t.Fatalf("invalid final relay configuration: mode=%q uri=%q", stored.RelayMode, stored.RelayURI)
			}
			_, _, err := normalizeRelayConfiguration(stored.RelayMode, stored.RelayURI, false)
			require.NoError(t, err)

			published := bus.snapshotEvents()
			require.Len(t, published, 1)
			require.Equal(t, events.AgentUpdateTopic.Value(), published[0].Topic)
			var payload models.Agent
			require.NoError(t, json.Unmarshal(published[0].Payload, &payload))
			require.Equal(t, stored, payload)
		})
	}
}

func TestUpdateRelayCASMissReturnsNotFoundWhenAgentWasDeleted(t *testing.T) {
	db := openRelayConcurrencyTestDB(t, false)
	bus := newAgentRecordingBus()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	current := models.Agent{
		AgentID:   "deleted-relay-agent",
		Name:      "before",
		Status:    1,
		RelayMode: "inherit",
		RelayURI:  "wss://relay.example/original",
	}
	require.NoError(t, db.Create(&current).Error)

	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRead)
	var initialQueries atomic.Int32
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(
		"test:relay_deleted_read_barrier",
		func(tx *gorm.DB) {
			if tx.Statement.Table != "agents" || tx.Error != nil || initialQueries.Add(1) > 1 {
				return
			}
			arrived <- struct{}{}
			select {
			case <-release:
			case <-deadline.Done():
				tx.AddError(deadline.Err())
			}
		},
	))

	c := newTestContext(t, db)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/agents/"+strconv.Itoa(int(current.ID)), nil)
	c.App.SetEventBus(bus)
	result := make(chan error, 1)
	go func() {
		_, err := (&Handler{}).Update(c, UpdateRequest{
			ID:         strconv.Itoa(int(current.ID)),
			AgentPatch: AgentPatch{RelayMode: agentPtr("custom")},
		})
		result <- err
	}()
	select {
	case <-arrived:
	case <-deadline.Done():
		t.Fatalf("initial agent read did not reach barrier: %v", deadline.Err())
	}
	require.NoError(t, db.Delete(&models.Agent{}, current.ID).Error)
	releaseRead()

	select {
	case err := <-result:
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusNotFound, apiErr.Status)
		require.Equal(t, "not found", apiErr.Message)
	case <-deadline.Done():
		t.Fatalf("deleted-agent update did not finish: %v", deadline.Err())
	}
	require.Empty(t, bus.snapshotEvents())
}

func openRelayConcurrencyTestDB(t *testing.T, fileBacked bool) *gorm.DB {
	t.Helper()
	dsn := ":memory:"
	if fileBacked {
		dsn = filepath.Join(t.TempDir(), "relay-cas.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if fileBacked {
		sqlDB.SetMaxOpenConns(4)
	} else {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close relay concurrency database: %v", err)
		}
	})
	require.NoError(t, models.AutoMigrate(db))
	if fileBacked {
		var journalMode string
		require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error)
		require.Equal(t, "wal", strings.ToLower(journalMode))
		var busyTimeout int
		require.NoError(t, db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error)
		require.Equal(t, 30000, busyTimeout)
	}
	return db
}

func TestUpdateRequestBindURIAndJSONUsesFlatPointerPatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Params = gin.Params{{Key: "id", Value: "42"}}
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/agents/42", bytes.NewBufferString(`{
		"status": 0,
		"proxy_url": "",
		"relay_mode": "custom",
		"relay_uri": "wss://relay.example/tunnel"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	var req UpdateRequest
	require.NoError(t, (api.DefaultRequestBinder{}).Bind(ginCtx, api.BindURIAndJSON, &req))
	require.Equal(t, "42", req.ID)
	require.Nil(t, req.Name)
	require.Nil(t, req.Tags)
	require.Nil(t, req.HTTPAddresses)
	require.NotNil(t, req.Status)
	require.Zero(t, *req.Status)
	require.NotNil(t, req.ProxyURL)
	require.Empty(t, *req.ProxyURL)
	require.Equal(t, "custom", *req.RelayMode)
	require.Equal(t, "wss://relay.example/tunnel", *req.RelayURI)
}
