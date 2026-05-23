package stats

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
)

// parseObsRange 应用 observability 端点的统一 query 缺省值：
// end<=0 → now; start<=0 → end-86400; gran!="hour" → GranDay。
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

// toDaoScope 把 middleware.RequestScope 翻译为 dao.Scope；nil 视作未授权空 scope。
func toDaoScope(s *middleware.RequestScope) dao.Scope {
	if s == nil {
		return dao.Scope{}
	}
	return dao.Scope{IsAdmin: s.IsAdmin, UserID: s.UserID}
}
