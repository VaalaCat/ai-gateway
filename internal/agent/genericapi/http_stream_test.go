package genericapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLocalHTTPStreamStripsUnsafeResponseHeadersAndTrailers(t *testing.T) {
	recorder := httptest.NewRecorder()
	upstream := &http.Response{
		StatusCode: http.StatusCreated,
		Header: http.Header{
			"Connection":       []string{"X-Hop"},
			"X-Hop":            []string{"secret"},
			"X-Vaala-Internal": []string{"secret"},
			"Forwarded":        []string{"for=secret"},
			"Content-Encoding": []string{"gzip"},
			"X-Safe":           []string{"preserved"},
		},
		Trailer: http.Header{
			"X-Response-Final": []string{"safe-final"},
			"X-Vaala-Final":    []string{"secret"},
			"Content-Length":   []string{"9"},
		},
		Body: io.NopCloser(strings.NewReader("encoded-as-is")),
	}

	result, err := (HTTPStream{}).Copy(recorder, upstream)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "gzip", recorder.Header().Get("Content-Encoding"))
	require.Equal(t, "preserved", recorder.Header().Get("X-Safe"))
	require.Empty(t, recorder.Header().Get("Connection"))
	require.Empty(t, recorder.Header().Get("X-Hop"))
	require.Empty(t, recorder.Header().Get("X-Vaala-Internal"))
	require.Empty(t, recorder.Header().Get("Forwarded"))
	response := recorder.Result()
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "safe-final", response.Trailer.Get("X-Response-Final"))
	require.Empty(t, response.Trailer.Get("X-Vaala-Final"))
	require.Empty(t, response.Trailer.Get("Content-Length"))
	require.Equal(t, int64(len("encoded-as-is")), result.ResponseBytes)
}

func TestLocalHTTPStreamRejectsNilAndZeroStatusBeforeCommit(t *testing.T) {
	tests := []struct {
		name     string
		client   http.ResponseWriter
		upstream *http.Response
	}{
		{name: "nil client", upstream: &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}},
		{name: "nil upstream", client: httptest.NewRecorder()},
		{name: "zero status", client: httptest.NewRecorder(), upstream: &http.Response{Body: http.NoBody}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (HTTPStream{}).Copy(test.client, test.upstream)
			require.Error(t, err)
		})
	}
}

func TestLocalHTTPStreamHandlesEmptyBodyBoundary(t *testing.T) {
	recorder := httptest.NewRecorder()
	result, err := (HTTPStream{}).Copy(recorder, &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     http.Header{"X-Empty": []string{"true"}},
		Body:       http.NoBody,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "true", recorder.Header().Get("X-Empty"))
	require.Zero(t, result.ResponseBytes)
}

func TestLocalHTTPStreamCommitsNilAndNoBodyWithTrailerDeclarations(t *testing.T) {
	for _, test := range []struct {
		name string
		body io.ReadCloser
	}{
		{name: "nil body"},
		{name: "http no body", body: http.NoBody},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			result, err := (HTTPStream{}).Copy(ginContext.Writer, &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Upstream": []string{"committed"}},
				Trailer:    http.Header{"X-Response-Trailer": []string{"final"}},
				Body:       test.body,
			})

			require.NoError(t, err)
			require.True(t, ginContext.Writer.Written(), "header-only upstream responses must commit the Gin writer")
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "committed", recorder.Header().Get("X-Upstream"))
			require.Equal(t, []string{"X-Response-Trailer"}, recorder.Header().Values("Trailer"))
			require.Equal(t, "final", recorder.Header().Get("X-Response-Trailer"))
			require.Equal(t, http.StatusOK, result.UpstreamStatus)
			require.Zero(t, result.ResponseBytes)
		})
	}
}
