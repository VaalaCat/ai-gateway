package user_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func postRegister(t *testing.T, srv interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, body any) *httptest.ResponseRecorder {
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

func TestRegister_InviteRequired_MissingCode(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"})
	w := postRegister(t, srv.Router, map[string]string{"username": "alice", "email": "a@x.com", "password": "pw1pw1pw1"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegister_InviteRequired_ValidCode(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"})
	srv.DB.Create(&models.InviteCode{Code: "GOOD", CreatorID: 1, MaxUses: 2, UsedCount: 0})
	w := postRegister(t, srv.Router, map[string]string{"username": "alice", "email": "a@x.com", "password": "pw1pw1pw1", "invite_code": "GOOD"})
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("expected success, got %d %s", w.Code, w.Body.String())
	}
	var ic models.InviteCode
	srv.DB.Where("code = ?", "GOOD").First(&ic)
	if ic.UsedCount != 1 {
		t.Fatalf("used_count = %d, want 1", ic.UsedCount)
	}
	var u models.User
	srv.DB.Where("username = ?", "alice").First(&u)
	var red int64
	srv.DB.Model(&models.InviteRedemption{}).Where("invitee_id = ?", u.ID).Count(&red)
	if red != 1 {
		t.Fatalf("redemption rows = %d, want 1", red)
	}
}

func TestRegister_InviteRequired_InvalidCode(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"})
	w := postRegister(t, srv.Router, map[string]string{"username": "alice", "email": "a@x.com", "password": "pw1pw1pw1", "invite_code": "NOPE"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegister_InviteRequired_Exhausted(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"})
	srv.DB.Create(&models.InviteCode{Code: "USED", CreatorID: 1, MaxUses: 1, UsedCount: 1})
	w := postRegister(t, srv.Router, map[string]string{"username": "alice", "email": "a@x.com", "password": "pw1pw1pw1", "invite_code": "USED"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegister_InviteDisabled_IgnoresCode(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"})
	require.NoError(t, srv.DB.Create(&models.UserGroup{ID: 8, Name: "ignored"}).Error)
	require.NoError(t, srv.DB.Create(&models.InviteCode{Code: "WHATEVER", CreatorID: 1, MaxUses: 1, GroupID: 8}).Error)
	// invite_enabled 未设置 => false
	w := postRegister(t, srv.Router, map[string]string{"username": "alice", "email": "a@x.com", "password": "pw1pw1pw1", "invite_code": "WHATEVER"})
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("expected success, got %d %s", w.Code, w.Body.String())
	}
	var user models.User
	require.NoError(t, srv.DB.Where("username = ?", "alice").First(&user).Error)
	require.Equal(t, models.DefaultUserGroupID, user.GroupID)
	var code models.InviteCode
	require.NoError(t, srv.DB.Where("code = ?", "WHATEVER").First(&code).Error)
	require.Zero(t, code.UsedCount)
}

func TestRegister_InviteSelectsGroupNotClientInput(t *testing.T) {
	for _, groupID := range []uint{0, models.DefaultUserGroupID, 8} {
		t.Run(strconv.FormatUint(uint64(groupID), 10), func(t *testing.T) {
			srv := setupMasterForUserAdmin(t)
			require.NoError(t, srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"}).Error)
			require.NoError(t, srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"}).Error)
			require.NoError(t, srv.DB.Create(&models.UserGroup{ID: 8, Name: "selected"}).Error)
			require.NoError(t, srv.DB.Create(&models.InviteCode{Code: "GROUP", CreatorID: 1, MaxUses: 1, GroupID: groupID}).Error)
			w := postRegister(t, srv.Router, map[string]any{
				"username": "group_user", "email": "group@x.com", "password": "pw1pw1pw1", "invite_code": "GROUP", "group_id": 999,
			})
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var user models.User
			require.NoError(t, srv.DB.Where("username = ?", "group_user").First(&user).Error)
			want := groupID
			if want == 0 {
				want = models.DefaultUserGroupID
			}
			require.Equal(t, want, user.GroupID)
		})
	}
}

func TestRegister_InviteUnavailableGroupRollsBack(t *testing.T) {
	for _, state := range []string{"deleted", "disabled", "missing-default"} {
		t.Run(state, func(t *testing.T) {
			srv := setupMasterForUserAdmin(t)
			require.NoError(t, srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"}).Error)
			require.NoError(t, srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"}).Error)
			groupID := uint(8)
			require.NoError(t, srv.DB.Create(&models.UserGroup{ID: groupID, Name: "selected"}).Error)
			if state == "missing-default" {
				groupID = 0
			}
			require.NoError(t, srv.DB.Create(&models.InviteCode{Code: "GROUP", CreatorID: 1, MaxUses: 1, GroupID: groupID}).Error)
			switch state {
			case "deleted":
				require.NoError(t, srv.DB.Delete(&models.UserGroup{}, groupID).Error)
			case "disabled":
				require.NoError(t, srv.DB.Model(&models.UserGroup{}).Where("id = ?", groupID).Update("status", consts.StatusDisabled).Error)
			case "missing-default":
				require.NoError(t, srv.DB.Delete(&models.UserGroup{}, models.DefaultUserGroupID).Error)
			}
			w := postRegister(t, srv.Router, map[string]string{"username": "group_user", "email": "group@x.com", "password": "pw1pw1pw1", "invite_code": "GROUP"})
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assertInviteRegistrationRolledBack(t, srv.DB)
		})
	}
}

func TestRegister_InviteAuditFailureRollsBackUserAndConsumption(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	require.NoError(t, srv.DB.Create(&models.Setting{Key: "registration_enabled", Value: "true"}).Error)
	require.NoError(t, srv.DB.Create(&models.Setting{Key: "invite_enabled", Value: "true"}).Error)
	require.NoError(t, srv.DB.Create(&models.InviteCode{Code: "GROUP", CreatorID: 1, MaxUses: 1}).Error)
	const callback = "test:fail-invite-redemption"
	require.NoError(t, srv.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "invite_redemptions" {
			tx.AddError(errors.New("forced invite audit failure"))
		}
	}))
	t.Cleanup(func() { _ = srv.DB.Callback().Create().Remove(callback) })
	w := postRegister(t, srv.Router, map[string]string{"username": "group_user", "email": "group@x.com", "password": "pw1pw1pw1", "invite_code": "GROUP"})
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assertInviteRegistrationRolledBack(t, srv.DB)
}

func assertInviteRegistrationRolledBack(t *testing.T, db *gorm.DB) {
	t.Helper()
	var code models.InviteCode
	require.NoError(t, db.Where("code = ?", "GROUP").First(&code).Error)
	require.Zero(t, code.UsedCount)
	var users, redemptions int64
	require.NoError(t, db.Model(&models.User{}).Where("username = ?", "group_user").Count(&users).Error)
	require.NoError(t, db.Model(&models.InviteRedemption{}).Where("code = ?", "GROUP").Count(&redemptions).Error)
	require.Zero(t, users)
	require.Zero(t, redemptions)
}
