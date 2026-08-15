package api_request_log

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
	"github.com/stretchr/testify/require"
)

func TestListRequestFilterRejectsWindowOver365Days(t *testing.T) {
	_, err := listFilter(ListRequest{TimeWindowQuery: listfilter.TimeWindowQuery{Start: 0, End: 366 * 86_400}})
	require.Error(t, err)
	var apiErr *api.APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 400, apiErr.Status)
}

func TestListRequestFilterMapsExactTokenStatusAndTime(t *testing.T) {
	filter, err := listFilter(ListRequest{
		TimeWindowQuery: listfilter.TimeWindowQuery{Start: 1_000, End: 2_000},
		TokenID:         "12", StatusCode: "502",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1_000, filter.Start)
	require.EqualValues(t, 2_000, filter.End)
	require.NotNil(t, filter.TokenID)
	require.EqualValues(t, 12, *filter.TokenID)
	require.NotNil(t, filter.StatusCode)
	require.Equal(t, 502, *filter.StatusCode)
}

func TestListRequestFilterLeavesEmptyFiltersUnchanged(t *testing.T) {
	filter, err := listFilter(ListRequest{})
	require.NoError(t, err)
	require.Zero(t, filter.Start)
	require.Zero(t, filter.End)
	require.Nil(t, filter.TokenID)
	require.Nil(t, filter.StatusCode)
}

func TestListRequestFilterStatusCodeBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		want    int
		wantErr bool
	}{
		{name: "no response status", status: "0", want: 0},
		{name: "below HTTP range", status: "99", wantErr: true},
		{name: "HTTP lower boundary", status: "100", want: 100},
		{name: "extended upper boundary", status: "999", want: 999},
		{name: "above extended range", status: "1000", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := listFilter(ListRequest{StatusCode: tt.status})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, filter.StatusCode)
			require.Equal(t, tt.want, *filter.StatusCode)
		})
	}
}
