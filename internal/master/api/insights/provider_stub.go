package insights

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

// stubProvider 是占位 InsightProvider:所有方法直接返回 501 NotImplemented。
// 它存在的意义是把 "未知 type (404)" 和 "type 暂未实现 (501)" 区分开;
// 后续把某个 type 接上真实 DAO 时,改 registry 里这一行即可。
type stubProvider struct{ name string }

func (s stubProvider) notImplemented() error {
	return api.ErrorWithCode(501, "NotImplemented", "insight type not implemented: "+s.name, map[string]any{"type": s.name})
}

func (s stubProvider) Meta(Context, string) (EntityMeta, error) {
	return EntityMeta{}, s.notImplemented()
}

func (s stubProvider) Summary(Context, string, dao.ObsRange) (SummaryKpis, error) {
	return SummaryKpis{}, s.notImplemented()
}

func (s stubProvider) Trend(Context, string, dao.ObsRange) ([]dao.TimeBucket, error) {
	return nil, s.notImplemented()
}

func (s stubProvider) StageLatency(Context, string, dao.ObsRange) (*dao.StageLatency, error) {
	return nil, s.notImplemented()
}

func (s stubProvider) Breakdown(Context, string, dao.ObsRange) (Breakdown, error) {
	return Breakdown{}, s.notImplemented()
}

func (s stubProvider) RecentErrors(Context, string, dao.ObsRange, int) ([]ErrorSample, error) {
	return nil, s.notImplemented()
}
