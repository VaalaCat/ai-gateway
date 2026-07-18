package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncPushParamsTopLevelSchemaPreservesOpaqueData(t *testing.T) {
	want := SyncPushParams{
		Entity:  "agent",
		Action:  "update",
		Data:    []byte(`{"opaque_payload":{"nested":true}}`),
		Version: 17,
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)
	requireTopLevelJSONFields(t, raw, "entity", "action", "data", "version")

	var got SyncPushParams
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got, "the protocol envelope must preserve opaque Data bytes")
}

func TestFullSyncResponseTopLevelSchemaPreservesOpaqueItems(t *testing.T) {
	tests := []struct {
		name       string
		want       FullSyncResponse
		fieldNames []string
	}{
		{
			name: "zero optional pagination fields",
			want: FullSyncResponse{
				Items:   []byte(`[{"opaque_payload":"page"}]`),
				Total:   1,
				HasMore: false,
				Version: 18,
			},
			fieldNames: []string{"items", "total", "has_more", "version"},
		},
		{
			name: "nonzero keyset pagination fields",
			want: FullSyncResponse{
				Items:         []byte(`[{"opaque_payload":"keyset"}]`),
				Total:         25,
				Page:          2,
				HasMore:       true,
				Version:       19,
				Keyset:        true,
				LastID:        23,
				SnapshotMaxID: 25,
				BaseVersion:   16,
			},
			fieldNames: []string{"items", "total", "page", "has_more", "version", "keyset", "last_id", "snapshot_max_id", "base_version"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.want)
			require.NoError(t, err)
			requireTopLevelJSONFields(t, raw, test.fieldNames...)

			var got FullSyncResponse
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, test.want, got, "the protocol envelope must preserve opaque Items bytes")
		})
	}
}

func requireTopLevelJSONFields(t *testing.T, raw []byte, want ...string) {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	got := make([]string, 0, len(fields))
	for name := range fields {
		got = append(got, name)
	}
	require.ElementsMatch(t, want, got)
}
