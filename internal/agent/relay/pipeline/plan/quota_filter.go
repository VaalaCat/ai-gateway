package plan

type quotaFilter struct{}

func (quotaFilter) Name() string { return "quota" }

func (quotaFilter) Apply(fctx *FilterContext, in []ScoredCandidate) ([]ScoredCandidate, DropCode) {
	cache := fctx.Rctx.Agent.GetCache()
	ui := fctx.Rctx.Input.UserInfo
	if ui == nil || ui.UserID == 0 { // 系统 token:不限
		return in, DropNone
	}
	mc := cache.GetModelConfig(fctx.RealModel)
	if !modelIsPriced(mc) { // 未定价 → 放行,不读余额
		return in, DropNone
	}
	byokMode := cache.Settings().BYOKBillingMode
	var free, paid []ScoredCandidate
	for _, c := range in {
		if ChannelConsumesQuota(c.Channel, c.Source, byokMode, mc) {
			paid = append(paid, c)
		} else {
			free = append(free, c)
		}
	}
	if len(paid) == 0 { // 候选全免费 → 放行,不读余额
		return in, DropNone
	}
	u := cache.GetUser(fctx.Rctx.Request.Context(), ui.UserID)
	if u == nil { // 读不到余额 → 乐观放行(降级)
		return in, DropNone
	}
	if u.Quota > cache.Settings().MinQuotaReserve { // 余额足:全留
		return in, DropNone
	}
	if len(free) == 0 { // 没钱且无免费候选
		return nil, DropInsufficientQuota
	}
	return free, DropNone // 没钱:只留免费
}
