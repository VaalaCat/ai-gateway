package user_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func postLogin(t *testing.T, srv interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, identifier, password string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"username": identifier, "password": password})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	return w
}

func TestLogin_ByEmail(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pw1pw1pw1"), bcrypt.DefaultCost)
	srv.DB.Create(&models.User{
		Username: "alice", Email: "alice@example.com", Password: string(hashed),
		PasswordSet: true, Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1,
	})

	w := postLogin(t, srv.Router, "ALICE@example.com", "pw1pw1pw1")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogin_ByUsernameStillWorks(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pw1pw1pw1"), bcrypt.DefaultCost)
	srv.DB.Create(&models.User{
		Username: "bob", Email: "bob@example.com", Password: string(hashed),
		PasswordSet: true, Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1,
	})

	w := postLogin(t, srv.Router, "bob", "pw1pw1pw1")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
}

func TestLogin_BadCredentials(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pw1pw1pw1"), bcrypt.DefaultCost)
	srv.DB.Create(&models.User{Username: "c", Password: string(hashed), PasswordSet: true, Status: 1, GroupID: 1})

	w := postLogin(t, srv.Router, "c", "WRONG")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
