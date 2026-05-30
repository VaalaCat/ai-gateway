package script

import (
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdate_NotFound(t *testing.T) {
	h, ctx, _ := setupScriptTest(t)
	req := UpdateRequest{ID: "99999"}
	req.SetBodyMap(map[string]any{"enabled": false})
	_, err := h.Update(ctx, req)
	require.Error(t, err)
	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	assert.Equal(t, 404, apiErr.Status)
}

func TestUpdate_CodeCompileError(t *testing.T) {
	h, ctx, _ := setupScriptTest(t)
	created, err := h.Create(ctx, CreateRequest{Name: "s", Code: "function onRequest(c){}"})
	require.NoError(t, err)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(created.Value.ID), 10)}
	req.SetBodyMap(map[string]any{"code": "function onRequest( {"})
	_, err = h.Update(ctx, req)
	require.Error(t, err)
	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	assert.Equal(t, 400, apiErr.Status)
}
