package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestAnnouncementIdentityPaginationAndDetailAuthorization(t *testing.T) {
	srv := setupTestMaster(t)
	group := models.UserGroup{Name: "identity-announcements"}
	require.NoError(t, srv.DB.Create(&group).Error)
	user := models.User{Username: "identity-reader", GroupID: group.ID, Role: consts.RoleUser, Status: consts.StatusEnabled}
	other := models.User{Username: "other-reader", GroupID: group.ID, Role: consts.RoleUser, Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&user).Error)
	require.NoError(t, srv.DB.Create(&other).Error)
	token, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	otherToken, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, other.ID, other.Role, other.Username, "", "")
	require.NoError(t, err)
	admin, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, consts.RoleAdmin, user.Username, "", "")
	require.NoError(t, err)
	excludedGroup := models.UserGroup{Name: "excluded-announcements"}
	require.NoError(t, srv.DB.Create(&excludedGroup).Error)
	mutation := dao.NewAdminMutation(dao.NewContext(srv.App)).Announcement()
	rows := make([]models.Announcement, 23)
	for i := range rows {
		rows[i] = models.Announcement{Title: fmt.Sprintf("notice-%d", i), Content: "body only in detail", ShowForGuests: i%2 == 0, CreatedAt: 100}
		if i == len(rows)-2 {
			rows[i].GroupIDs = []uint{group.ID}
		}
		// Guest visibility is independent of the signed-in recipient group.
		if i == 0 {
			rows[i].GroupIDs = []uint{excludedGroup.ID}
		}
		require.NoError(t, mutation.Create(&rows[i]))
	}
	private := rows[len(rows)-2]
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		consts.ClaimUserID: user.ID, consts.ClaimRole: user.Role, consts.ClaimExp: time.Now().Add(-time.Hour).Unix(),
	}).SignedString([]byte(srv.Cfg.Master.JWTSecret))
	require.NoError(t, err)
	privatePath := fmt.Sprintf("/api/announcements/%d", private.ID)
	for _, credentials := range []string{"invalid-bearer", expired} {
		for _, path := range []string{"/api/announcements", privatePath} {
			response := reqHelper(srv, credentials, http.MethodGet, path, nil)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		}
	}
	for _, query := range []string{"page_size=101", "page_size=-1", "page_size=0", "page_size=not-a-number", "page=0", "page=-1", "page=not-a-number", "page=9223372036854775807&page_size=100"} {
		for _, identity := range []struct{ credentials, path string }{
			{"", "/api/announcements"},
			{token, "/api/announcements"},
			{admin, "/api/admin/announcements"},
		} {
			response := reqHelper(srv, identity.credentials, http.MethodGet, identity.path+"?"+query, nil)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		}
	}
	response := reqHelper(srv, "", http.MethodGet, "/api/public/announcements", nil)
	require.Equal(t, http.StatusNotFound, response.Code, "obsolete public route must be removed")
	for _, credentials := range []string{"", token} {
		response = reqHelper(srv, credentials, http.MethodGet, "/api/announcements", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var first models.AnnouncementPage[models.ReceivedAnnouncementSummary]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &first))
		require.Len(t, first.Items, 10)
		wantTotal := int64(22)
		if credentials == "" {
			wantTotal = 12
		}
		require.Equal(t, wantTotal, first.Total)
		require.Equal(t, 1, first.Page)
		require.Equal(t, 10, first.PageSize)
		require.NotContains(t, response.Body.String(), "\"content\"")
		if credentials == "" {
			require.NotContains(t, response.Body.String(), "\"seen\"")
		} else {
			require.Contains(t, response.Body.String(), "\"seen\":false")
		}
	}
	response = reqHelper(srv, admin, http.MethodGet, "/api/admin/announcements", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var adminPage models.AnnouncementPage[models.AnnouncementSummary]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &adminPage))
	require.Len(t, adminPage.Items, 10)
	require.EqualValues(t, 23, adminPage.Total)
	response = reqHelper(srv, admin, http.MethodGet, "/api/admin/announcements?page_size=100", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "\"content\"")
	response = reqHelper(srv, admin, http.MethodGet, fmt.Sprintf("/api/admin/announcements/%d", private.ID), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var detail models.Announcement
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &detail))
	require.Equal(t, private.Content, detail.Content)
	require.Equal(t, []uint{group.ID}, detail.GroupIDs)

	var ids []uint
	for number := 1; ; number++ {
		response = reqHelper(srv, token, http.MethodGet, fmt.Sprintf("/api/announcements?page_size=7&page=%d", number), nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var page models.AnnouncementPage[models.ReceivedAnnouncementSummary]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
		for _, row := range page.Items {
			ids = append(ids, row.ID)
		}
		if int64(len(ids)) >= page.Total {
			break
		}
	}
	require.Len(t, ids, len(rows)-1)
	for i := range ids {
		require.Equal(t, rows[len(rows)-1-i].ID, ids[i])
	}
	// Arbitrary page/size changes never override the current audience.
	for _, credentials := range []string{"", token} {
		for _, number := range []int{1, 3, 100} {
			response = reqHelper(srv, credentials, http.MethodGet, fmt.Sprintf("/api/announcements?page_size=5&page=%d", number), nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var visible models.AnnouncementPage[models.ReceivedAnnouncementSummary]
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &visible))
			for _, row := range visible.Items {
				if credentials == "" {
					require.True(t, row.ShowForGuests)
					require.NotEqual(t, private.ID, row.ID)
				} else {
					require.NotEqual(t, rows[0].ID, row.ID)
				}
			}
		}
	}
	response = reqHelper(srv, token, http.MethodGet, privatePath, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = reqHelper(srv, "", http.MethodGet, privatePath, nil)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	response = reqHelper(srv, token, http.MethodPost, privatePath+"/seen", map[string]any{"revision": 1})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = reqHelper(srv, otherToken, http.MethodGet, privatePath, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var received models.ReceivedAnnouncement
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &received))
	require.False(t, received.Seen, "shared body cache must not leak another recipient's read state")

	guestPath := fmt.Sprintf("/api/announcements/%d", rows[0].ID)
	response = reqHelper(srv, "", http.MethodGet, guestPath, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	// Client scope/user/group parameters do not override authenticated identity.
	for _, scope := range []string{"public", "admin", "invalid"} {
		response = reqHelper(srv, token, http.MethodGet, guestPath+"?scope="+scope, nil)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	require.NoError(t, srv.DB.Model(&user).Update("group_id", group.ID+2000).Error)
	response = reqHelper(srv, token, http.MethodGet, privatePath+fmt.Sprintf("?user_id=%d&group_id=%d", other.ID, group.ID), nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = reqHelper(srv, token, http.MethodGet, fmt.Sprintf("/api/announcements?user_id=%d&group_id=%d", other.ID, group.ID), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page models.AnnouncementPage[models.ReceivedAnnouncementSummary]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	for _, row := range page.Items {
		require.NotEqual(t, private.ID, row.ID)
	}
	rows[0].ShowForGuests = false
	require.NoError(t, mutation.Update(&rows[0], false))
	response = reqHelper(srv, "", http.MethodGet, guestPath, nil)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	require.NoError(t, srv.DB.Delete(&other).Error)
	response = reqHelper(srv, otherToken, http.MethodGet, privatePath, nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = reqHelper(srv, otherToken, http.MethodGet, "/api/announcements", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"items":[],"total":0,"page":1,"page_size":10}`, response.Body.String())
}
