package insights

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// Context 是 InsightProvider 收到的执行上下文。
// 抽出来一个最小接口而不是直接传 (q, scope, app),
// 方便测试时 mock,也避免每个 provider method 都重复 4 个参数。
type Context interface {
	DAO() dao.AdminQuery
	Scope() dao.Scope
	RequestScope() *middleware.RequestScope
}

// InsightProvider 是 entity 维度的查询契约。
// 每个 entity-type (agent/channel/model/...) 一个 provider 实例,实现下面这 6 个方法,
// 由 handler 拼装成统一的 GetResponse。
//
// 设计要点:
//  1. Meta 是入口:返回 404 NotFound 时 handler 立刻终止 (entity 不存在);
//     其他方法的错误一般被静默吞掉 (子查询 best-effort)。
//  2. StageLatency 允许返回 nil (前端 null);不是所有 entity-type 都有 stage 时延概念。
//  3. RecentErrors 给定 limit;provider 自己决定是否复用,以及如何过滤。
type InsightProvider interface {
	Meta(ctx Context, id string) (EntityMeta, error)
	Summary(ctx Context, id string, r dao.ObsRange) (SummaryKpis, error)
	Trend(ctx Context, id string, r dao.ObsRange) ([]dao.TimeBucket, error)
	StageLatency(ctx Context, id string, r dao.ObsRange) (*dao.StageLatency, error)
	Breakdown(ctx Context, id string, r dao.ObsRange) (Breakdown, error)
	RecentErrors(ctx Context, id string, r dao.ObsRange, limit int) ([]ErrorSample, error)
}

// registry 是 type → provider 的映射;
// stub 的 type 仍然占位,handler 在执行前先用类型断言挑出 stub 类型,统一返回 501 NotImplemented。
// 这样可以与 "type 不存在 → 404" 形成清晰区分。
var registry = map[string]InsightProvider{
	"agent":   agentInsightProvider{},
	"channel": stubProvider{name: "channel"},
	"model":   stubProvider{name: "model"},
	"user":    stubProvider{name: "user"},
	"token":   stubProvider{name: "token"},
}

// providerCtx 是 Context 的默认实现。
// 除了对外暴露的 DAO/Scope,还存了 *gorm.DB 给 provider 内部走 raw 聚合
// (有些 entity-scoped 查询用 dao 已封好的 method 表达不出来,
// 如 by agent_id 的 hourly trend);通过 rawDB() 内部方法访问。
type providerCtx struct {
	q     dao.AdminQuery
	s     dao.Scope
	scope *middleware.RequestScope
	db    *gorm.DB
}

func (p *providerCtx) DAO() dao.AdminQuery                    { return p.q }
func (p *providerCtx) Scope() dao.Scope                       { return p.s }
func (p *providerCtx) RequestScope() *middleware.RequestScope { return p.scope }
func (p *providerCtx) rawDB() *gorm.DB                        { return p.db }

// newProviderCtx 由 handler 构造 providerCtx;
// admin/user scope 都转成 dao.Scope (Phase 1 主要 admin 用,user scope 检查放在 handler 里)。
func newProviderCtx(application app.Application, scope *middleware.RequestScope) Context {
	q := dao.NewAdminQuery(dao.NewContext(application))
	s := dao.Scope{}
	if scope != nil {
		s = dao.Scope{IsAdmin: scope.IsAdmin, UserID: scope.UserID}
	}
	return &providerCtx{q: q, s: s, scope: scope, db: application.GetDB()}
}

// parseObsRange 是 insights 端点的统一 query 缺省值解析。
// (与 stats.parseObsRange 同语义, 故意复制一份避免跨 package 依赖。)
func parseObsRange(start, end int64, gran string) dao.ObsRange {
	if end <= 0 {
		end = time.Now().UTC().Unix()
	}
	if start <= 0 {
		start = end - 86400
	}
	g := dao.GranDay
	if gran == "hour" {
		g = dao.GranHour
	}
	return dao.ObsRange{Start: start, End: end, Gran: g}
}
