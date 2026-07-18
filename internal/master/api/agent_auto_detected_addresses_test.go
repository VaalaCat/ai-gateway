package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"
	"unsafe"

	apiagent "github.com/VaalaCat/ai-gateway/internal/master/api/agent"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
)

type staticAgentControlSource map[string]connectivity.ControlSessionFact

func (s staticAgentControlSource) GetControlSession(agentID string) (connectivity.ControlSessionFact, bool) {
	fact, ok := s[agentID]
	return fact, ok
}

type staticOperationAgentFinder struct{ agent models.Agent }

func (f staticOperationAgentFinder) FindAgent(context.Context, string) (models.Agent, error) {
	return f.agent, nil
}

type recordingProbeOperator struct {
	sourceID   string
	generation uint64
	scope      protocol.ProbeScope
}

func (o *recordingProbeOperator) EnqueueManualSession(_ context.Context, sourceID string, generation uint64, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	o.sourceID, o.generation, o.scope = sourceID, generation, scope
	return protocol.ProbeAck{ProbeID: "probe-1", State: "queued"}, nil
}

func setHubAutoHTTPAddresses(t *testing.T, hub *msync.Hub, agentID string, addrs []agentproxy.Address) {
	t.Helper()

	field := reflect.ValueOf(hub).Elem().FieldByName("autoHTTPAddrs")
	if !field.IsValid() {
		t.Fatal("hub.autoHTTPAddrs not found")
	}
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	if field.IsNil() {
		field.Set(reflect.MakeMap(field.Type()))
	}
	field.SetMapIndex(reflect.ValueOf(agentID), reflect.ValueOf(addrs))
}

func TestAgentDetailIncludesAutoDetectedHTTPAddresses(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	loginW := httptest.NewRecorder()
	loginReq, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(loginW, loginReq)
	if loginW.Code != 200 {
		t.Fatalf("login failed: %d %s", loginW.Code, loginW.Body.String())
	}
	var loginResp map[string]string
	if err := json.Unmarshal(loginW.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("parse login response: %v", err)
	}
	jwtToken := loginResp["token"]
	if jwtToken == "" {
		t.Fatal("no token in login response")
	}

	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	wCreate := doReq("POST", "/api/admin/agents", map[string]any{"name": "agent-auto-detail"})
	if wCreate.Code != 201 {
		t.Fatalf("create agent failed: %d %s", wCreate.Code, wCreate.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(wCreate.Body.Bytes(), &created); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	agentID, _ := created["agent_id"].(string)
	idFloat, ok := created["id"].(float64)
	if !ok || agentID == "" {
		t.Fatalf("invalid create response: %#v", created)
	}
	agentDBID := int(idFloat)

	setHubAutoHTTPAddresses(t, srv.Hub, agentID, []agentproxy.Address{
		{URL: "http://10.0.0.2:7001", Tag: "auto-detected"},
	})

	wDetail := doReq("GET", "/api/admin/agents/"+strconv.Itoa(agentDBID)+"/detail", nil)
	if wDetail.Code != 200 {
		t.Fatalf("detail failed: %d %s", wDetail.Code, wDetail.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(wDetail.Body.Bytes(), &detail); err != nil {
		t.Fatalf("parse detail response: %v", err)
	}

	rawHTTPAddresses, ok := detail["http_addresses"].(string)
	if !ok || rawHTTPAddresses == "" {
		t.Fatalf("expected non-empty detail http_addresses, got: %#v", detail["http_addresses"])
	}
	configuredHTTPAddresses, _ := detail["configured_http_addresses"].(string)
	if configuredHTTPAddresses != "" {
		t.Fatalf("configured_http_addresses should remain empty for auto-only agent, got: %q", configuredHTTPAddresses)
	}
	effectiveHTTPAddresses, _ := detail["effective_http_addresses"].(string)
	if effectiveHTTPAddresses == "" {
		t.Fatal("expected non-empty effective_http_addresses")
	}
	if effectiveHTTPAddresses != rawHTTPAddresses {
		t.Fatalf("legacy http_addresses should match effective_http_addresses, got %q vs %q", rawHTTPAddresses, effectiveHTTPAddresses)
	}
	var addrs []agentproxy.Address
	if err := json.Unmarshal([]byte(rawHTTPAddresses), &addrs); err != nil {
		t.Fatalf("parse detail http_addresses: %v", err)
	}
	if len(addrs) != 1 || addrs[0].URL != "http://10.0.0.2:7001" || addrs[0].Tag != "auto-detected" {
		t.Fatalf("unexpected merged addresses: %#v", addrs)
	}
}

func TestConnectivityEnqueuesSchedulerProbeWithCurrentGeneration(t *testing.T) {
	srv := setupTestMaster(t)

	source := models.Agent{AgentID: "source-agent", Secret: "sec-source", Name: "source", Status: 1}
	if err := srv.DB.Create(&source).Error; err != nil {
		t.Fatalf("create source agent: %v", err)
	}
	connections := connectivity.NewService("address-test", connectivity.Sources{Control: staticAgentControlSource{
		source.AgentID: {Generation: 1, ConnectedAt: time.Now().Unix()},
	}}, connectivity.Options{})
	probes := &recordingProbeOperator{}
	h := &apiagent.Handler{
		Hub: srv.Hub, Connections: connections,
		Operations: masteroperations.NewService(t.Context(), staticOperationAgentFinder{agent: source}, masteroperations.Sources{
			Connections: connections, Probes: probes,
		}),
	}

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ctx := &app.Context{Context: ginCtx, App: srv.App, UserInfo: &app.UserInfo{Role: 2}, OwnerContext: t.Context()}

	ack, err := h.CheckConnectivity(ctx, apiagent.ConnectivityRequest{ID: strconv.Itoa(int(source.ID))})
	if err != nil {
		t.Fatalf("check connectivity failed: %v", err)
	}
	if ack.Body.ProbeID != "probe-1" || ack.Body.State != "queued" {
		t.Fatalf("unexpected probe ack: %#v", ack)
	}
	if probes.sourceID != source.AgentID || probes.generation != 1 {
		t.Fatalf("probe lease = (%q, %d), want (%q, 1)", probes.sourceID, probes.generation, source.AgentID)
	}
	if !reflect.DeepEqual(probes.scope, protocol.ProbeScope{Kind: "all_enabled"}) {
		t.Fatalf("probe scope = %#v, want all_enabled", probes.scope)
	}
}
