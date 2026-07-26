package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

type AdminChannelQuery interface {
	GetByID(id uint) (*models.Channel, error)
	List(opts ListOptions, filter ChannelListFilter) ([]models.Channel, int64, error)
	ListAll() ([]models.Channel, error)
	ListByTag(tag string) ([]models.Channel, error)
	ListEnabled() ([]models.Channel, error)
	ChannelWindowUsage(channelID uint, wf WindowFilter) (ChannelUsage, error)
}

type AdminChannelMutation interface {
	Create(channel *models.Channel) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
}

type adminChannelQuery struct{ ctx *baseContext }
type adminChannelMutation struct{ ctx *baseContext }

func (q *adminChannelQuery) GetByID(id uint) (*models.Channel, error) {
	var channel models.Channel
	err := q.ctx.GetCoreDB().First(&channel, id).Error
	return &channel, err
}

func (q *adminChannelQuery) List(opts ListOptions, filter ChannelListFilter) ([]models.Channel, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.Channel{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ? OR models LIKE ?", like, like)
	}
	if filter.Type != nil {
		db = db.Where("type = ?", *filter.Type)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var channels []models.Channel
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&channels).Error
	return channels, total, err
}

func (q *adminChannelQuery) ListAll() ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetCoreDB().Find(&channels).Error
	return channels, err
}

func (q *adminChannelQuery) ListByTag(tag string) ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetCoreDB().Where("tag = ?", tag).Find(&channels).Error
	return channels, err
}

func (q *adminChannelQuery) ListEnabled() ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetCoreDB().Where("status = 1").Find(&channels).Error
	return channels, err
}

func (m *adminChannelMutation) Create(channel *models.Channel) error {
	return m.ctx.GetCoreDB().Create(channel).Error
}

func (m *adminChannelMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetCoreDB().Model(&models.Channel{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminChannelMutation) Delete(id uint) error {
	return m.ctx.GetCoreDB().Delete(&models.Channel{}, id).Error
}

// ChannelUsage 是某渠道某窗口的用量汇总。BilledCost=对用户结算后(SUM total_cost),
// RawCost=折扣前原价(SUM raw_cost),供限额按口径取数。
type ChannelUsage struct {
	Calls      int64
	BilledCost int64
	RawCost    int64
}

func (q *adminChannelQuery) ChannelWindowUsage(channelID uint, wf WindowFilter) (ChannelUsage, error) {
	db := q.ctx.GetCoreDB().Model(&models.ChannelDailyBilling{}).
		Where("channel_id = ? AND private_channel_id = 0", channelID)
	switch wf.Kind {
	case "since":
		db = db.Where("date >= ?", wf.SinceDate)
	case "month":
		db = db.Where("date LIKE ?", wf.MonthPrefix+"%")
	case "all":
		// 无日期过滤
	}
	var row struct {
		Calls      int64
		BilledCost int64
		RawCost    int64
	}
	err := db.Select("COALESCE(SUM(request_count),0) AS calls, " +
		"COALESCE(SUM(total_cost),0) AS billed_cost, " +
		"COALESCE(SUM(raw_cost),0) AS raw_cost").
		Scan(&row).Error
	return ChannelUsage{Calls: row.Calls, BilledCost: row.BilledCost, RawCost: row.RawCost}, err
}
