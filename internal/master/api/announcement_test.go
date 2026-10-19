package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementsVisibilitySeenAndAdminIsolation(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	admin := loginAsAdmin(t, srv, "admin", "admin123")
	group := models.UserGroup{Name: "announcement-team", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&group).Error)
	user := models.User{Username: "announcement-user", GroupID: group.ID, Role: consts.RoleUser, Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&user).Error)
	userToken, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)

	create := func(title string, ids []uint, guests any) models.Announcement {
		t.Helper()
		body := map[string]any{"title": title, "content": "<script>alert('plain text')</script>", "group_ids": ids}
		if guests != nil {
			body["show_for_guests"] = guests
		}
		w := reqHelper(srv, admin, http.MethodPost, "/api/admin/announcements", body)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		var row models.Announcement
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &row))
		return row
	}
	global := create("global", []uint{}, false)
	private := create("team only", []uint{group.ID}, false)
	public := create("guest default", []uint{1}, nil)
	require.False(t, global.ShowForGuests)
	require.False(t, private.ShowForGuests)
	require.True(t, public.ShowForGuests)

	w := reqHelper(srv, "", http.MethodGet, "/api/public/announcements", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var publicRows []models.Announcement
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &publicRows))
	require.Len(t, publicRows, 1)
	require.Equal(t, public.ID, publicRows[0].ID)
	require.Empty(t, publicRows[0].GroupIDs)
	require.NotContains(t, w.Body.String(), "team only")

	list := func() []models.ReceivedAnnouncement {
		t.Helper()
		w := reqHelper(srv, userToken, http.MethodGet, "/api/announcements", nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var rows []models.ReceivedAnnouncement
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
		return rows
	}
	rows := list()
	require.Len(t, rows, 2)
	require.Equal(t, private.ID, rows[0].ID)
	require.False(t, rows[0].Seen)
	require.Equal(t, global.ID, rows[1].ID)

	for range 2 {
		w := reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", private.ID), map[string]any{"revision": private.Revision})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	var count int64
	require.NoError(t, srv.DB.Model(&models.AnnouncementSeen{}).Where("user_id = ? AND announcement_id = ?", user.ID, private.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.True(t, list()[0].Seen)
	w = reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", public.ID), map[string]any{"revision": public.Revision})
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	w = reqHelper(srv, userToken, http.MethodPost, "/api/announcements/999999/seen", map[string]any{"revision": 1})
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())

	w = reqHelper(srv, admin, http.MethodPut, fmt.Sprintf("/api/admin/announcements/%d", private.ID), map[string]any{
		"title": "edited", "content": "updated body", "group_ids": []uint{group.ID}, "show_for_guests": false, "pinned": false,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, list()[0].Seen, "editing must not reset receipts")

	// Pinning changes list priority without reopening an already read notice.
	w = reqHelper(srv, admin, http.MethodPut, fmt.Sprintf("/api/admin/announcements/%d", private.ID), map[string]any{
		"title": "pinned", "content": "updated body", "group_ids": []uint{group.ID}, "show_for_guests": false, "pinned": true,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, list()[0].Seen)
	require.EqualValues(t, 1, list()[0].Revision)

	for revision := uint(2); revision <= 3; revision++ {
		w = reqHelper(srv, admin, http.MethodPut, fmt.Sprintf("/api/admin/announcements/%d", private.ID), map[string]any{
			"title": "reannounced", "content": "new body", "group_ids": []uint{group.ID}, "show_for_guests": false, "pinned": true, "reannounce": true,
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		rows = list()
		require.EqualValues(t, revision, rows[0].Revision)
		require.False(t, rows[0].Seen)
		for _, stale := range []uint{revision - 1, revision + 1} {
			w = reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", private.ID), map[string]any{"revision": stale})
			require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
			require.False(t, list()[0].Seen)
		}
		for range 2 {
			w = reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", private.ID), map[string]any{"revision": revision})
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		}
		w = reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", private.ID), map[string]any{"revision": revision - 1})
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		require.True(t, list()[0].Seen, "a delayed old acknowledgement must not overwrite the new receipt")
	}
	for _, body := range []any{nil, map[string]any{}, map[string]any{"revision": 0}, map[string]any{"revision": -1}} {
		w = reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", private.ID), body)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	}

	// A previously minted token must immediately follow the database membership.
	require.NoError(t, srv.DB.Model(&user).Update("group_id", 1).Error)
	rows = list()
	require.Len(t, rows, 2)
	require.Equal(t, public.ID, rows[0].ID)
	require.Equal(t, global.ID, rows[1].ID)
	w = reqHelper(srv, userToken, http.MethodPost, fmt.Sprintf("/api/announcements/%d/seen", private.ID), map[string]any{"revision": private.Revision})
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/announcements"},
		{http.MethodPost, "/api/admin/announcements"},
		{http.MethodPut, fmt.Sprintf("/api/admin/announcements/%d", private.ID)},
		{http.MethodDelete, fmt.Sprintf("/api/admin/announcements/%d", private.ID)},
	} {
		w := reqHelper(srv, userToken, route.method, route.path, nil)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		w = reqHelper(srv, "", route.method, route.path, nil)
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	}
	w = reqHelper(srv, "", http.MethodGet, "/api/announcements", nil)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

	w = reqHelper(srv, admin, http.MethodDelete, fmt.Sprintf("/api/admin/announcements/%d", private.ID), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	for _, model := range []any{&models.AnnouncementGroup{}, &models.AnnouncementSeen{}} {
		require.NoError(t, srv.DB.Model(model).Where("announcement_id = ?", private.ID).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestAnnouncementsRejectInvalidTargetsAndContent(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	admin := loginAsAdmin(t, srv, "admin", "admin123")
	for _, body := range []map[string]any{
		{"title": "valid", "content": "body", "group_ids": []uint{999999}},
		{"title": "valid", "content": "body", "group_ids": []uint{1, 1}},
		{"title": "valid", "content": "body", "group_ids": []int{-1}},
		{"title": "valid", "content": "body", "group_ids": []uint{0}},
		{"title": "valid", "content": "body", "group_ids": nil},
		{"title": "valid", "content": "body"},
		{"title": "   ", "content": "body", "group_ids": []uint{}},
		{"title": "valid", "content": "\n\t", "group_ids": []uint{}},
		{"title": strings.Repeat("中", 201), "content": "body", "group_ids": []uint{}},
		{"title": "valid", "content": strings.Repeat("中", 20001), "group_ids": []uint{}},
	} {
		w := reqHelper(srv, admin, http.MethodPost, "/api/admin/announcements", body)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	}
	var count int64
	require.NoError(t, srv.DB.Model(&models.Announcement{}).Count(&count).Error)
	require.Zero(t, count, "invalid targets must not create a global announcement")

	w := reqHelper(srv, admin, http.MethodPost, "/api/admin/announcements", map[string]any{
		"title": "targeted", "content": "body", "group_ids": []uint{1}, "show_for_guests": true,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var row models.Announcement
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &row))
	for _, body := range []map[string]any{
		{"title": "edited", "content": "body", "show_for_guests": false, "pinned": false},
		{"title": "edited", "content": "body", "group_ids": []uint{1}, "pinned": false},
		{"title": "edited", "content": "body", "group_ids": []uint{999999}, "show_for_guests": false, "pinned": false},
		{"title": "edited", "content": "body", "group_ids": []uint{1}, "show_for_guests": false},
	} {
		w = reqHelper(srv, admin, http.MethodPut, fmt.Sprintf("/api/admin/announcements/%d", row.ID), body)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	}
	w = reqHelper(srv, admin, http.MethodPut, fmt.Sprintf("/api/admin/announcements/%d", row.ID), map[string]any{
		"title": "edited", "content": "body", "group_ids": []uint{1}, "show_for_guests": false, "pinned": false,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var persisted models.Announcement
	require.NoError(t, srv.DB.First(&persisted, row.ID).Error)
	require.False(t, persisted.ShowForGuests)
	w = reqHelper(srv, "", http.MethodGet, "/api/public/announcements", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, "[]", w.Body.String())
}
