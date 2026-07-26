package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaskingTailMasksSensitivePatternsAcrossEveryChunkSplit(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "bearer", body: `before Bearer AAA{secret-tail}[]{}","tail":"ok"`, want: `before Bearer ***","tail":"ok"`},
		{name: "key", body: `before Key KEY[]{secret-tail} after`, want: `before Key *** after`},
		{name: "sk", body: `before sk-abcdefghijklmnopqrstuvwxyz after`, want: `before sk-*** after`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for split := 0; split <= len(tc.body); split++ {
				capture := NewMaskingTail(1024)
				_, err := capture.Write([]byte(tc.body[:split]))
				require.NoError(t, err)
				_, err = capture.Write([]byte(tc.body[split:]))
				require.NoError(t, err)
				require.Equal(t, tc.want, capture.String(), "split=%d", split)
				require.Equal(t, int64(len(tc.body)), capture.TotalSeen())
			}
		})
	}
}

func TestMaskingTailPreservesFalsePrefixes(t *testing.T) {
	const body = `Bear BearerX Keynote sk-short ordinary`
	capture := NewMaskingTail(1024)
	_, err := capture.Write([]byte(body))
	require.NoError(t, err)
	require.Equal(t, body, capture.String())
}

func TestMaskingTailKeepsUnstructuredTailWithinLimit(t *testing.T) {
	const tail = "REAL-TAIL"
	body := strings.Repeat("a", 100) + tail
	capture := NewMaskingTail(16)
	n, err := capture.Write([]byte(body))
	require.NoError(t, err)
	require.Equal(t, len(body), n)
	require.Len(t, capture.Bytes(), 16)
	require.True(t, strings.HasSuffix(capture.String(), tail))
	require.Equal(t, int64(len(body)), capture.TotalSeen())
	require.True(t, capture.Truncated())
}

func TestMaskingTailSetsTotalSeenLowerBoundWithoutChangingTail(t *testing.T) {
	capture := NewMaskingTail(16)
	_, err := capture.WriteString("retained-tail")
	require.NoError(t, err)

	capture.SetTotalSeenLowerBound(100)
	capture.SetTotalSeenLowerBound(10)

	require.Equal(t, "retained-tail", capture.String())
	require.Equal(t, int64(100), capture.TotalSeen())
	require.True(t, capture.Truncated())
}

func TestMaskingTailAllocationsDoNotGrowWithPatternCount(t *testing.T) {
	const pattern = "Bearer secret,Key secret;sk-abcdefghijklmnopqrst "
	shortBody := strings.Repeat(pattern, 100)
	longBody := strings.Repeat(pattern, 10_000)
	allocations := func(body string) float64 {
		return testing.AllocsPerRun(5, func() {
			capture := NewMaskingTail(128)
			_, err := capture.WriteString(body)
			require.NoError(t, err)
			_ = capture.Bytes()
		})
	}

	shortAllocs := allocations(shortBody)
	longAllocs := allocations(longBody)
	t.Logf("allocations: short=%f long=%f", shortAllocs, longAllocs)
	require.LessOrEqual(t, longAllocs, shortAllocs+4, "short=%f long=%f", shortAllocs, longAllocs)
}
