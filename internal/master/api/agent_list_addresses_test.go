package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
)

func TestAgentsListIncludesHTTPAddressesForAdmin(t *testing.T) {
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

	wCreate := doReq("POST", "/api/admin/agents", map[string]any{
		"name":           "agent-with-address",
		"http_addresses": `[{"url":"http://127.0.0.1:9000","tag":"manual"}]`,
	})
	if wCreate.Code != 201 {
		t.Fatalf("create agent failed: %d %s", wCreate.Code, wCreate.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(wCreate.Body.Bytes(), &created); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	agentID, _ := created["agent_id"].(string)
	if agentID == "" {
		t.Fatalf("expected agent_id in create response: %#v", created)
	}

	// Inject an auto-detected address and verify manual config still has priority.
	setHubAutoHTTPAddresses(t, srv.Hub, agentID, []agentproxy.Address{
		{URL: "http://10.0.0.5:8139", Tag: "auto-detected"},
	})

	wList := doReq("GET", "/api/admin/agents", nil)
	if wList.Code != 200 {
		t.Fatalf("list agents failed: %d %s", wList.Code, wList.Body.String())
	}

	var listResp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("parse list response: %v", err)
	}
	if len(listResp.Data) == 0 {
		t.Fatal("expected at least one agent in list")
	}

	row := listResp.Data[0]
	httpAddresses, ok := row["http_addresses"].(string)
	if !ok || httpAddresses == "" {
		t.Fatalf("expected non-empty http_addresses for admin list response, got: %#v", row["http_addresses"])
	}

	configuredHTTPAddresses, _ := row["configured_http_addresses"].(string)
	if configuredHTTPAddresses != `[{"url":"http://127.0.0.1:9000","tag":"manual"}]` {
		t.Fatalf("unexpected configured_http_addresses: %q", configuredHTTPAddresses)
	}

	effectiveHTTPAddresses, _ := row["effective_http_addresses"].(string)
	if effectiveHTTPAddresses != `[{"url":"http://127.0.0.1:9000","tag":"manual"}]` {
		t.Fatalf("unexpected effective_http_addresses: %q", effectiveHTTPAddresses)
	}
	if httpAddresses != effectiveHTTPAddresses {
		t.Fatalf("legacy http_addresses should match effective_http_addresses, got %q vs %q", httpAddresses, effectiveHTTPAddresses)
	}
}
