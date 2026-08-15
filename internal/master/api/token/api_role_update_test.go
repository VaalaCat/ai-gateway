package token

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeAPIRoleIDsAcceptsOnlyExactlyRepresentableJSONIntegers(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{name: "null", raw: nil},
		{name: "zero", raw: []any{float64(0)}},
		{name: "negative", raw: []any{float64(-1)}},
		{name: "fraction", raw: []any{1.5}},
		{name: "non array", raw: "1"},
		{name: "non number", raw: []any{"1"}},
		{name: "two to the fifty three", raw: []any{float64(9007199254740992)}},
		{name: "larger than two to the fifty three", raw: []any{float64(9007199254740994)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeAPIRoleIDs(tc.raw)
			require.Error(t, err)
		})
	}

	got, err := normalizeAPIRoleIDs([]any{
		float64(9007199254740991), float64(2), float64(2), float64(1),
	})
	require.NoError(t, err)
	require.Equal(t, []uint{1, 2, 9007199254740991}, got)
	empty, err := normalizeAPIRoleIDs([]any{})
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}
