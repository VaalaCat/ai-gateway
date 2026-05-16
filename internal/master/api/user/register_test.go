package user_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func postRegister(t *testing.T, srv interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/register", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	return w
}

func TestRegister_WritesEmail(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})

	w := postRegister(t, srv.Router, map[string]string{
		"username": "alice", "email": "Alice@Example.com", "password": "pw1pw1pw1",
	})
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var u models.User
	srv.DB.Where("username = ?", "alice").First(&u)
	if u.Email != "alice@example.com" {
		t.Fatalf("expected lower-cased email stored, got %q", u.Email)
	}
}

func TestRegister_EmailTaken(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	srv.DB.Create(&models.User{Username: "bob", Email: "bob@example.com", Password: "x", Status: 1, GroupID: 1})

	w := postRegister(t, srv.Router, map[string]string{
		"username": "alice", "email": "BOB@example.com", "password": "pw1pw1pw1",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRegister_BadEmail(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})

	w := postRegister(t, srv.Router, map[string]string{
		"username": "alice", "email": "not-an-email", "password": "pw1pw1pw1",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}
