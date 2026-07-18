package model_routing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func createOwnedToken(t *testing.T, srv *master.Server, adminJWT string, userID uint, name string) models.Token {
	t.Helper()
	w := routingRequest(t, srv, adminJWT, http.MethodPost, "/api/tokens", map[string]any{
		"user_id": userID,
		"name":    name,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var token models.Token
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &token))
	return token
}

func createTokenRouting(t *testing.T, srv *master.Server, jwt string, tokenID uint, name string) (*httptest.ResponseRecorder, models.ModelRouting) {
	t.Helper()
	w := routingRequest(t, srv, jwt, http.MethodPost, fmt.Sprintf("/api/tokens/%d/model-routings", tokenID), map[string]any{
		"name": name, "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	var routing models.ModelRouting
	_ = json.Unmarshal(w.Body.Bytes(), &routing)
	return w, routing
}

func routingRequest(t *testing.T, srv *master.Server, jwt, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	return w
}

func TestTokenRoutingCRUDIsStrictlyScopedByToken(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o")
	aliceID, aliceJWT := registerAndLoginUser(t, srv, "token_route_alice", "password123")
	firstToken := createOwnedToken(t, srv, adminJWT, aliceID, "first")
	secondToken := createOwnedToken(t, srv, adminJWT, aliceID, "second")

	firstW, firstRouting := createTokenRouting(t, srv, aliceJWT, firstToken.ID, "smart")
	secondW, secondRouting := createTokenRouting(t, srv, aliceJWT, secondToken.ID, "smart")
	require.Equal(t, http.StatusOK, firstW.Code, firstW.Body.String())
	require.Equal(t, http.StatusOK, secondW.Code, secondW.Body.String())
	require.NotZero(t, firstRouting.ID)
	require.NotZero(t, secondRouting.ID)
	require.Equal(t, firstToken.ID, firstRouting.TokenID)
	require.Equal(t, secondToken.ID, secondRouting.TokenID)

	list := routingRequest(t, srv, aliceJWT, http.MethodGet, fmt.Sprintf("/api/tokens/%d/model-routings", firstToken.ID), nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var page struct {
		Data []models.ModelRouting `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	require.Equal(t, firstRouting.ID, page.Data[0].ID)

	wrongPath := routingRequest(t, srv, aliceJWT, http.MethodGet,
		fmt.Sprintf("/api/tokens/%d/model-routings/%d", firstToken.ID, secondRouting.ID), nil)
	require.Equal(t, http.StatusNotFound, wrongPath.Code)
	flatAdmin := routingRequest(t, srv, adminJWT, http.MethodGet,
		fmt.Sprintf("/api/admin/model-routings/%d", firstRouting.ID), nil)
	require.Equal(t, http.StatusNotFound, flatAdmin.Code)

	updated := routingRequest(t, srv, aliceJWT, http.MethodPut,
		fmt.Sprintf("/api/tokens/%d/model-routings/%d", firstToken.ID, firstRouting.ID), map[string]any{"remark": "updated"})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	deleted := routingRequest(t, srv, aliceJWT, http.MethodDelete,
		fmt.Sprintf("/api/tokens/%d/model-routings/%d", firstToken.ID, firstRouting.ID), nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
}

func TestTokenRoutingOwnershipAndOwnerFields(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o")
	aliceID, aliceJWT := registerAndLoginUser(t, srv, "token_route_owner", "password123")
	_, bobJWT := registerAndLoginUser(t, srv, "token_route_other", "password123")
	token := createOwnedToken(t, srv, adminJWT, aliceID, "owned")

	for _, request := range []struct {
		method string
		body   any
	}{
		{method: http.MethodGet},
		{method: http.MethodPost, body: map[string]any{
			"name": "blocked", "enabled": true,
			"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
		}},
	} {
		w := routingRequest(t, srv, bobJWT, request.method, fmt.Sprintf("/api/tokens/%d/model-routings", token.ID), request.body)
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	}

	w := routingRequest(t, srv, aliceJWT, http.MethodPost, fmt.Sprintf("/api/tokens/%d/model-routings", token.ID), map[string]any{
		"name": "bad-owner", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var apiError map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &apiError))
	require.Equal(t, "invalid_scope_owner", apiError["code"])

	adminCreate := routingRequest(t, srv, adminJWT, http.MethodPost,
		fmt.Sprintf("/api/admin/tokens/%d/model-routings", token.ID), map[string]any{
			"name": "admin-route", "enabled": true,
			"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
		})
	require.Equal(t, http.StatusOK, adminCreate.Code, adminCreate.Body.String())
}

func TestDeletingTokenAlsoDeletesTokenRoutings(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o")
	aliceID, aliceJWT := registerAndLoginUser(t, srv, "token_route_delete", "password123")
	token := createOwnedToken(t, srv, adminJWT, aliceID, "deleted")
	w, routing := createTokenRouting(t, srv, aliceJWT, token.ID, "removed")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	deleted := routingRequest(t, srv, aliceJWT, http.MethodDelete, fmt.Sprintf("/api/tokens/%d", token.ID), nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	var routingCount int64
	require.NoError(t, srv.App.GetDB().Model(&models.ModelRouting{}).Where("id = ?", routing.ID).Count(&routingCount).Error)
	require.Zero(t, routingCount)

	get := routingRequest(t, srv, aliceJWT, http.MethodGet, fmt.Sprintf("/api/tokens/%d", token.ID), nil)
	require.Equal(t, http.StatusNotFound, get.Code)
}
