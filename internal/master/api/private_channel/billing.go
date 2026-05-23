package private_channel

import (
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// BillingRangeRequest 是三个 byok billing endpoint 共用的查询参数。
// from/to 是 YYYY-MM-DD（UTC 日历日，inclusive）。
type BillingRangeRequest struct {
	From string `form:"from"`
	To   string `form:"to"`
}

// DailySeriesItem 是 overview daily_series 的单点。
type DailySeriesItem struct {
	Date             string `json:"date"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
}

// BillingOverviewResponse 聚合当前 user 名下所有 BYOK channel 的 KPI + 时间序列。
type BillingOverviewResponse struct {
	TotalRequests int64             `json:"total_requests"`
	TotalSuccess  int64             `json:"total_success"`
	TotalFailed   int64             `json:"total_failed"`
	TotalCost     int64             `json:"total_cost"`
	TotalTokens           int64             `json:"total_tokens"`
	TotalPromptTokens     int64             `json:"total_prompt_tokens"`
	TotalCompletionTokens int64             `json:"total_completion_tokens"`
	TotalCacheReadTokens  int64             `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64             `json:"total_cache_write_tokens"`
	SuccessRate           float64           `json:"success_rate"`
	DailySeries   []DailySeriesItem `json:"daily_series"`
}

// ByChannelItem 是 by-channel breakdown 的单行。
type ByChannelItem struct {
	PrivateChannelID uint    `json:"private_channel_id"`
	ChannelName      string  `json:"channel_name"`
	ChannelType      int     `json:"channel_type"`
	RequestCount     int64   `json:"request_count"`
	SuccessCount     int64   `json:"success_count"`
	FailedCount      int64   `json:"failed_count"`
	SuccessRate      float64 `json:"success_rate"`
	TotalTokens      int64   `json:"total_tokens"`
	TotalCost        int64   `json:"total_cost"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	InputCost        int64   `json:"input_cost"`
	OutputCost       int64   `json:"output_cost"`
}

// ByChannelResponse 列表载荷。
type ByChannelResponse struct {
	Items []ByChannelItem `json:"items"`
}

// ByModelItem 是 by-model breakdown 的单行。
type ByModelItem struct {
	ModelName    string  `json:"model_name"`
	RequestCount int64   `json:"request_count"`
	SuccessCount int64   `json:"success_count"`
	FailedCount  int64   `json:"failed_count"`
	SuccessRate      float64 `json:"success_rate"`
	TotalTokens      int64   `json:"total_tokens"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	InputCost        int64   `json:"input_cost"`
	OutputCost       int64   `json:"output_cost"`
	TotalCost        int64   `json:"total_cost"`
}

// ByModelResponse 列表载荷。
type ByModelResponse struct {
	Items []ByModelItem `json:"items"`
}

// BillingOverview 聚合 KPI + daily series；by-channel/by-model 复用同一 DAO。
//
// 数据流：DAO ListPrivateChannelDailyByOwner → 按 date 折叠 → KPI 求和。
// daily 表存的是已 settle 的 (date, private_channel_id) 行；同一天多个 channel
// 会展开为多行，因此 handler 必须额外 group by date 才能得到时间序列。
func (h *Handler) BillingOverview(c *app.Context, req BillingRangeRequest) (BillingOverviewResponse, error) {
	if c.UserInfo == nil {
		return BillingOverviewResponse{}, api.UnauthorizedError("not authenticated")
	}
	from, to, err := normalizeBillingRange(req.From, req.To)
	if err != nil {
		return BillingOverviewResponse{}, api.BadRequestError("invalid date range", err)
	}

	rows, err := byokDailyRows(c, c.UserInfo.UserID, from, to)
	if err != nil {
		return BillingOverviewResponse{}, err
	}

	// 时间序列按 date 折叠（同一天可能跨多个 private_channel）。
	bucket := make(map[string]*DailySeriesItem, len(rows))
	order := make([]string, 0, len(rows))
	var resp BillingOverviewResponse
	for i := range rows {
		r := &rows[i]
		resp.TotalRequests += r.RequestCount
		resp.TotalSuccess += r.SuccessCount
		resp.TotalFailed += r.FailedCount
		resp.TotalCost += r.TotalCost
		resp.TotalTokens += r.PromptTokens + r.CompletionTokens
		resp.TotalPromptTokens += r.PromptTokens
		resp.TotalCompletionTokens += r.CompletionTokens
		resp.TotalCacheReadTokens += r.CacheReadTokens
		resp.TotalCacheWriteTokens += r.CacheWriteTokens

		day, ok := bucket[r.Date]
		if !ok {
			day = &DailySeriesItem{Date: r.Date}
			bucket[r.Date] = day
			order = append(order, r.Date)
		}
		day.RequestCount += r.RequestCount
		day.SuccessCount += r.SuccessCount
		day.FailedCount += r.FailedCount
		day.PromptTokens += r.PromptTokens
		day.CompletionTokens += r.CompletionTokens
		day.CacheReadTokens += r.CacheReadTokens
		day.CacheWriteTokens += r.CacheWriteTokens
		day.InputCost += r.InputCost
		day.OutputCost += r.OutputCost
		day.TotalCost += r.TotalCost
	}
	resp.SuccessRate = safeSuccessRate(resp.TotalSuccess, resp.TotalRequests)
	resp.DailySeries = make([]DailySeriesItem, 0, len(order))
	for _, d := range order {
		resp.DailySeries = append(resp.DailySeries, *bucket[d])
	}
	return resp, nil
}

// BillingByChannel 按 private_channel_id 折叠 daily 行，返回单 channel 维度的 KPI。
func (h *Handler) BillingByChannel(c *app.Context, req BillingRangeRequest) (ByChannelResponse, error) {
	if c.UserInfo == nil {
		return ByChannelResponse{}, api.UnauthorizedError("not authenticated")
	}
	from, to, err := normalizeBillingRange(req.From, req.To)
	if err != nil {
		return ByChannelResponse{}, api.BadRequestError("invalid date range", err)
	}

	rows, err := byokDailyRows(c, c.UserInfo.UserID, from, to)
	if err != nil {
		return ByChannelResponse{}, err
	}

	bucket := make(map[uint]*ByChannelItem, len(rows))
	order := make([]uint, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		item, ok := bucket[r.PrivateChannelID]
		if !ok {
			item = &ByChannelItem{
				PrivateChannelID: r.PrivateChannelID,
				ChannelName:      r.ChannelName,
				ChannelType:      r.ChannelType,
			}
			bucket[r.PrivateChannelID] = item
			order = append(order, r.PrivateChannelID)
		}
		item.RequestCount += r.RequestCount
		item.SuccessCount += r.SuccessCount
		item.FailedCount += r.FailedCount
		item.TotalTokens += r.PromptTokens + r.CompletionTokens
		item.TotalCost += r.TotalCost
		item.PromptTokens += r.PromptTokens
		item.CompletionTokens += r.CompletionTokens
		item.CacheReadTokens += r.CacheReadTokens
		item.CacheWriteTokens += r.CacheWriteTokens
		item.InputCost += r.InputCost
		item.OutputCost += r.OutputCost
	}
	// 稳定排序：total_cost 倒序，相同则 private_channel_id 升序。
	items := make([]ByChannelItem, 0, len(order))
	for _, id := range order {
		it := bucket[id]
		it.SuccessRate = safeSuccessRate(it.SuccessCount, it.RequestCount)
		items = append(items, *it)
	}
	sortByChannelDesc(items)
	return ByChannelResponse{Items: items}, nil
}

// BillingByModel 调用 usage_logs 聚合（daily 表无 model_name 列）。
func (h *Handler) BillingByModel(c *app.Context, req BillingRangeRequest) (ByModelResponse, error) {
	if c.UserInfo == nil {
		return ByModelResponse{}, api.UnauthorizedError("not authenticated")
	}
	from, to, err := normalizeBillingRange(req.From, req.To)
	if err != nil {
		return ByModelResponse{}, api.BadRequestError("invalid date range", err)
	}

	q := dao.NewAdminQuery(dao.NewContext(c.App))
	rows, err := q.Billing().ListPrivateChannelByModelByOwner(c.UserInfo.UserID, dao.ChannelBillingListFilter{
		StartDate: from,
		EndDate:   to,
	})
	if err != nil {
		return ByModelResponse{}, api.InternalError("byok by-model query failed", err)
	}

	items := make([]ByModelItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ByModelItem{
			ModelName:        r.ModelName,
			RequestCount:     r.RequestCount,
			SuccessCount:     r.SuccessCount,
			FailedCount:      r.FailedCount,
			SuccessRate:      safeSuccessRate(r.SuccessCount, r.RequestCount),
			TotalTokens:      r.PromptTokens + r.CompletionTokens,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			CacheReadTokens:  r.CacheReadTokens,
			CacheWriteTokens: r.CacheWriteTokens,
			InputCost:        r.InputCost,
			OutputCost:       r.OutputCost,
			TotalCost:        r.TotalCost,
		})
	}
	return ByModelResponse{Items: items}, nil
}

// byokDailyRows 是 overview / by-channel 复用的取数封装。
func byokDailyRows(c *app.Context, ownerID uint, from, to string) ([]dao.ChannelBillingDailyItem, error) {
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	rows, err := q.Billing().ListPrivateChannelDailyByOwner(ownerID, dao.ChannelBillingListFilter{
		StartDate: from,
		EndDate:   to,
	})
	if err != nil {
		return nil, api.InternalError("byok billing query failed", err)
	}
	return rows, nil
}

// normalizeBillingRange 验证 from/to 是 YYYY-MM-DD 且 from <= to。
// 空串保留为空（不过滤该端）。
func normalizeBillingRange(from, to string) (string, string, error) {
	start, err := parseISODate(from)
	if err != nil {
		return "", "", fmt.Errorf("invalid from: %w", err)
	}
	end, err := parseISODate(to)
	if err != nil {
		return "", "", fmt.Errorf("invalid to: %w", err)
	}
	if start != "" && end != "" && start > end {
		return "", "", fmt.Errorf("from %s is after to %s", start, end)
	}
	return start, end, nil
}

func parseISODate(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}

func safeSuccessRate(success, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(success) / float64(total)
}

// sortByChannelDesc 按 total_cost 倒序排（相同则 private_channel_id 升序）。
// 用插入排序保持稳定，结果数量 ~= 单 user BYOK channel 数，量级很小。
func sortByChannelDesc(items []ByChannelItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			a, b := items[j-1], items[j]
			if a.TotalCost > b.TotalCost {
				break
			}
			if a.TotalCost == b.TotalCost && a.PrivateChannelID < b.PrivateChannelID {
				break
			}
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}
