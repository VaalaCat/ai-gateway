package user_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func seedUserAndToken(t *testing.T, srv *master.Server, username, email string) (*models.User, string) {
	t.Helper()
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pw1pw1pw1"), bcrypt.DefaultCost)
	u := &models.User{
		Username: username, Email: email, Password: string(hashed),
		PasswordSet: true, Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1,
	}
	if err := srv.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, u.ID, u.Role, u.Username, u.DisplayName, u.AvatarURL)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return u, tok
}

func putProfile(t *testing.T, srv *master.Server, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/profile", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+tok)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	return w
}

func TestUpdateProfile_SuccessAll(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	u, tok := seedUserAndToken(t, srv, "alice_up", "alice_up@x.com")

	body := `{"email":"new@x.com","display_name":"Alice 张三","avatar_url":"https://example.com/a.png"}`
	w := putProfile(t, srv, tok, body)
	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{"new@x.com", "Alice 张三", "https://example.com/a.png"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body missing %q: %s", want, w.Body.String())
		}
	}

	var got models.User
	srv.DB.First(&got, u.ID)
	if got.Email != "new@x.com" || got.DisplayName != "Alice 张三" || got.AvatarURL != "https://example.com/a.png" {
		t.Errorf("DB row mismatch: %+v", got)
	}
}

func TestUpdateProfile_PartialDisplayName(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	u, tok := seedUserAndToken(t, srv, "bob_up", "bob_up@x.com")

	w := putProfile(t, srv, tok, `{"display_name":"Bobby"}`)
	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got models.User
	srv.DB.First(&got, u.ID)
	if got.DisplayName != "Bobby" || got.Email != "bob_up@x.com" {
		t.Errorf("partial mismatch: %+v", got)
	}
}

func TestUpdateProfile_ClearAvatar(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	u, tok := seedUserAndToken(t, srv, "carol_up", "carol_up@x.com")
	srv.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("avatar_url", "https://old/p.png")

	w := putProfile(t, srv, tok, `{"avatar_url":""}`)
	if w.Code != 200 {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	var got models.User
	srv.DB.First(&got, u.ID)
	if got.AvatarURL != "" {
		t.Errorf("AvatarURL not cleared: %q", got.AvatarURL)
	}
}

func TestUpdateProfile_EmailSameAsSelf_Noop(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	_, tok := seedUserAndToken(t, srv, "dave_up", "dave_up@x.com")

	w := putProfile(t, srv, tok, `{"email":"dave_up@x.com"}`)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateProfile_EmailTaken_409(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	_, _ = seedUserAndToken(t, srv, "first_up", "taken_up@x.com")
	_, tok := seedUserAndToken(t, srv, "second_up", "second_up@x.com")

	w := putProfile(t, srv, tok, `{"email":"taken_up@x.com"}`)
	if w.Code != 409 {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "email_taken") {
		t.Errorf("body missing email_taken: %s", w.Body.String())
	}
}

func TestUpdateProfile_DisplayNameTooLong_400(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	_, tok := seedUserAndToken(t, srv, "evan_up", "evan_up@x.com")

	body := `{"display_name":"` + strings.Repeat("a", 65) + `"}`
	w := putProfile(t, srv, tok, body)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateProfile_AvatarNotURL_400(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	_, tok := seedUserAndToken(t, srv, "fred_up", "fred_up@x.com")

	w := putProfile(t, srv, tok, `{"avatar_url":"not-a-url"}`)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateProfile_AvatarTooLong_400(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	_, tok := seedUserAndToken(t, srv, "gina_up", "gina_up@x.com")

	tooLong := "https://example.com/" + strings.Repeat("a", 500)
	body := `{"avatar_url":"` + tooLong + `"}`
	w := putProfile(t, srv, tok, body)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateProfile_NoAuth_401(t *testing.T) {
	srv := setupMasterForUserAdmin(t)

	req := httptest.NewRequest("PUT", "/api/profile", bytes.NewReader([]byte(`{"display_name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateProfileMutationObservesRequestCancellation(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	_, tok := seedUserAndToken(t, srv, "cancel_profile", "cancel_profile@x.com")
	updateEntered := make(chan struct{}, 1)
	updateCanceled := make(chan error, 1)
	releaseUpdate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUpdate) }) }
	t.Cleanup(release)
	if err := srv.DB.Callback().Update().Before("gorm:update").Register("test:block_profile_update", func(tx *gorm.DB) {
		if tx.Statement.Table != "users" {
			return
		}
		select {
		case updateEntered <- struct{}{}:
		default:
		}
		select {
		case <-tx.Statement.Context.Done():
			cause := context.Cause(tx.Statement.Context)
			updateCanceled <- cause
			_ = tx.AddError(cause)
		case <-releaseUpdate:
		}
	}); err != nil {
		t.Fatal(err)
	}

	cause := errors.New("profile request canceled")
	requestCtx, cancel := context.WithCancelCause(context.Background())
	req := httptest.NewRequest("PUT", "/api/profile", bytes.NewReader([]byte(`{"display_name":"Canceled"}`)))
	req = req.WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+tok)
	w := httptest.NewRecorder()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		srv.Router.ServeHTTP(w, req)
	}()
	<-updateEntered
	cancel(cause)

	select {
	case got := <-updateCanceled:
		if !errors.Is(got, cause) {
			t.Fatalf("mutation cancellation cause = %v, want %v", got, cause)
		}
	case <-time.After(100 * time.Millisecond):
		release()
		<-requestDone
		t.Fatal("profile mutation did not observe request cancellation")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("profile handler remained blocked after mutation cancellation")
	}
}
