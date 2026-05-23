package sync

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeAgentRow struct {
	ID       string
	LastSeen int64
}

func TestEnrichLastSeen(t *testing.T) {
	tr := newTrackerForTest(t)
	tr.Touch("a", 500)
	tr.Touch("b", 100) // 内存比 DB 旧 → 应取 DB

	items := []fakeAgentRow{
		{ID: "a", LastSeen: 200}, // DB 旧，内存新 → 取 500
		{ID: "b", LastSeen: 400}, // DB 新，内存旧 → 取 400
		{ID: "c", LastSeen: 300}, // 内存未命中 → 保留 300
	}

	EnrichLastSeen(tr, items,
		func(it fakeAgentRow) string { return it.ID },
		func(it fakeAgentRow) int64 { return it.LastSeen },
		func(it *fakeAgentRow, ts int64) { it.LastSeen = ts },
	)

	require.Equal(t, int64(500), items[0].LastSeen, "内存大于 DB 取内存")
	require.Equal(t, int64(400), items[1].LastSeen, "DB 大于内存取 DB")
	require.Equal(t, int64(300), items[2].LastSeen, "内存未命中保留 DB 值")
}

func TestEnrichLastSeen_EmptyInput(t *testing.T) {
	tr := newTrackerForTest(t)
	var items []fakeAgentRow
	EnrichLastSeen(tr, items,
		func(it fakeAgentRow) string { return it.ID },
		func(it fakeAgentRow) int64 { return it.LastSeen },
		func(it *fakeAgentRow, ts int64) { it.LastSeen = ts },
	)
	require.Empty(t, items)
}

func TestEnrichLastSeen_NilTracker(t *testing.T) {
	// boundary: tracker=nil 时不 panic、items 不变
	items := []fakeAgentRow{{ID: "a", LastSeen: 100}}
	EnrichLastSeen(nil, items,
		func(it fakeAgentRow) string { return it.ID },
		func(it fakeAgentRow) int64 { return it.LastSeen },
		func(it *fakeAgentRow, ts int64) { it.LastSeen = ts },
	)
	require.Equal(t, int64(100), items[0].LastSeen)
}
