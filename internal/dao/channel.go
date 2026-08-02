package dao

import (
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

// AutoDisableResult 表示带 revision 条件的自动禁用是否提交成功；
// 只有 BYOK 渠道更新成功时才返回 OwnerID。
type AutoDisableResult struct {
	Updated bool
	OwnerID uint
}

type AdminChannelQuery interface {
	GetByID(id uint) (*models.Channel, error)
	ListByIDs(ids []uint) ([]models.Channel, error)
	List(opts ListOptions, filter ChannelListFilter) ([]models.Channel, int64, error)
	ListAll() ([]models.Channel, error)
	ListByTag(tag string) ([]models.Channel, error)
	ListEnabled() ([]models.Channel, error)
	ChannelWindowUsage(channelID uint, wf WindowFilter) (ChannelUsage, error)
}

type AdminChannelMutation interface {
	Create(channel *models.Channel) error
	Update(id uint, updates map[string]any) error
	UpdateByIDs(ids []uint, assignments map[string]any) (int64, error)
	// ReconcileLimit commits a limit evaluator decision only if every field used
	// to determine ownership still matches the snapshot.
	ReconcileLimit(snapshot models.Channel, status int, limitState models.ChannelDisableState) (bool, error)
	AutoDisable(id uint, revision uint64, state models.ChannelDisableState) (AutoDisableResult, error)
	Delete(id uint) error
}

type adminChannelQuery struct{ ctx *baseContext }
type adminChannelMutation struct{ ctx *baseContext }

func (q *adminChannelQuery) GetByID(id uint) (*models.Channel, error) {
	var channel models.Channel
	err := q.ctx.GetCoreDB().First(&channel, id).Error
	return &channel, err
}

func (q *adminChannelQuery) ListByIDs(ids []uint) ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetCoreDB().Where("id IN ?", ids).Order("id ASC").Find(&channels).Error
	return channels, err
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

func (m *adminChannelMutation) UpdateByIDs(ids []uint, assignments map[string]any) (int64, error) {
	result := m.ctx.GetCoreDB().Model(&models.Channel{}).Where("id IN ?", ids).Updates(assignments)
	return result.RowsAffected, result.Error
}

func (m *adminChannelMutation) ReconcileLimit(snapshot models.Channel, status int, limitState models.ChannelDisableState) (bool, error) {
	limitValues, err := channelLimitValues(snapshot.Limit.Data())
	if err != nil {
		return false, err
	}
	limitStateValues, err := channelDisableStateValues(snapshot.LimitState.Data())
	if err != nil {
		return false, err
	}
	autoBanStateValues, err := channelDisableStateValues(snapshot.AutoBanState.Data())
	if err != nil {
		return false, err
	}
	result := m.ctx.GetCoreDB().Model(&models.Channel{}).
		Where("id = ? AND status = ? AND auto_ban_revision = ? AND `limit` IN ? AND limit_state IN ? AND auto_ban_state IN ?",
			snapshot.ID,
			snapshot.Status,
			snapshot.AutoBanRevision,
			limitValues,
			limitStateValues,
			autoBanStateValues,
		).
		Updates(map[string]any{
			"status":      status,
			"limit_state": datatypes.NewJSONType(limitState),
		})
	return result.RowsAffected == 1, result.Error
}

func (m *adminChannelMutation) AutoDisable(id uint, revision uint64, state models.ChannelDisableState) (AutoDisableResult, error) {
	result := m.ctx.GetCoreDB().Model(&models.Channel{}).
		Where("id = ? AND status = ? AND auto_ban = ? AND auto_ban_revision = ?", id, 1, 1, revision).
		Updates(map[string]any{
			"status":         0,
			"auto_ban_state": datatypes.NewJSONType(state),
		})
	if result.Error != nil {
		return AutoDisableResult{}, result.Error
	}
	if result.RowsAffected > 1 {
		return AutoDisableResult{}, fmt.Errorf("auto-disable channel %d affected %d rows", id, result.RowsAffected)
	}
	return AutoDisableResult{Updated: result.RowsAffected == 1}, nil
}

func channelLimitValues(limit models.ChannelLimit) ([]string, error) {
	encoded, err := json.Marshal(limit)
	if err != nil {
		return nil, err
	}
	values := []string{string(encoded)}
	if limit.DisableAt == 0 && len(limit.Rules) == 0 {
		values = append(values, "{}")
	}
	return values, nil
}

func channelDisableStateValues(state models.ChannelDisableState) ([]string, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	values := []string{string(encoded)}
	if state == (models.ChannelDisableState{}) {
		values = append(values, "{}")
	}
	return values, nil
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
	logDB, err := q.ctx.LogDB()
	if err != nil {
		return ChannelUsage{}, err
	}
	db := logDB.Model(&models.ChannelDailyBilling{}).
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
	err = db.Select("COALESCE(SUM(request_count),0) AS calls, " +
		"COALESCE(SUM(total_cost),0) AS billed_cost, " +
		"COALESCE(SUM(raw_cost),0) AS raw_cost").
		Scan(&row).Error
	return ChannelUsage{Calls: row.Calls, BilledCost: row.BilledCost, RawCost: row.RawCost}, WrapLogDatabaseError(err)
}
