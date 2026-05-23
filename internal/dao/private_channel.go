package dao

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
)

// PrivateChannelFilter is the optional filter passed to list operations.
//
// Search is name-only. Pre-Task-25 we additionally LIKE-matched against the
// JSON-encoded `models` text blob, but that conflated "find channel by name"
// with "find channel that exposes model X" (e.g. searching "gpt" hit every
// channel whose models array contained "gpt-4o"). Use ModelName for the
// model-membership filter — it matches the JSON token boundary (`"<name>"`)
// so substrings like "gpt-4" do NOT accidentally match "gpt-4-turbo".
type PrivateChannelFilter struct {
	Search    string
	ModelName string
	Type      *int
	Status    *int
	OwnerID   *uint // only meaningful for admin cross-owner views
}

// BaseURLUsagePreviewLimit caps how many owner/channel pairs CountByBaseURLPrefix
// returns alongside the total count. The full count is unbounded; the preview
// list is bounded so admin UI never paginates an arbitrarily long affected-channel
// roster (and so we don't ship megabytes of rows over the wire for a hot prefix).
const BaseURLUsagePreviewLimit = 50

// ChannelOwnerPair is a (owner_id, channel_name) preview row returned by
// CountByBaseURLPrefix. It is intentionally a thin projection — admin UI only
// needs enough context to identify which user/channel will break if a prefix
// is removed from the allowlist; it does not need full channel detail.
type ChannelOwnerPair struct {
	OwnerID     uint   `json:"owner_id"`
	ChannelName string `json:"channel_name" gorm:"column:channel_name"`
}

// AdminPrivateChannelQuery is the read-only DAO surface for PrivateChannel.
type AdminPrivateChannelQuery interface {
	GetByID(id uint) (*models.PrivateChannel, error)
	ListOwnedBy(ownerID uint, opts ListOptions, filter PrivateChannelFilter) ([]models.PrivateChannel, int64, error)
	ListVisibleTo(userID uint, userGroupIDs []uint) ([]models.PrivateChannel, error)
	CountByOwner(ownerID uint) (int64, error)
	ListAcrossOwners(opts ListOptions, filter PrivateChannelFilter) ([]models.PrivateChannel, int64, error)
	// CountByBaseURLPrefix returns the total number of private_channels whose
	// BaseURL begins with the given prefix, plus a bounded preview of
	// (owner_id, channel_name) pairs (capped at BaseURLUsagePreviewLimit).
	// Used by admin UI to warn before deleting a BaseURL allowlist entry: if any
	// existing channel still references the prefix string, removal will cause
	// silent validation failure for that owner the next time they edit the
	// channel. prefix is bound through a parameterized LIKE — safe from injection.
	CountByBaseURLPrefix(prefix string) (int64, []ChannelOwnerPair, error)
}

// AdminPrivateChannelShareQuery is the read-only DAO surface for PrivateChannelShare.
// v1 only exposes Query operations; Mutation API is intentionally not provided here.
type AdminPrivateChannelShareQuery interface {
	ListSharesForUser(userID uint, groupIDs []uint) ([]models.PrivateChannelShare, error)
	ListSharesByChannel(channelID uint) ([]models.PrivateChannelShare, error)
}

type adminPrivateChannelQuery struct{ ctx *baseContext }
type adminPrivateChannelShareQuery struct{ ctx *baseContext }

func (q *adminPrivateChannelQuery) GetByID(id uint) (*models.PrivateChannel, error) {
	var pc models.PrivateChannel
	err := q.ctx.GetDB().First(&pc, id).Error
	if err != nil {
		return nil, err
	}
	return &pc, nil
}

func (q *adminPrivateChannelQuery) CountByOwner(ownerID uint) (int64, error) {
	var n int64
	err := q.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("owner_id = ?", ownerID).Count(&n).Error
	return n, err
}

func (q *adminPrivateChannelQuery) ListOwnedBy(ownerID uint, opts ListOptions, filter PrivateChannelFilter) ([]models.PrivateChannel, int64, error) {
	db := q.ctx.GetDB().Model(&models.PrivateChannel{}).Where("owner_id = ?", ownerID)
	if filter.Search != "" {
		db = db.Where("name LIKE ?", "%"+filter.Search+"%")
	}
	if filter.ModelName != "" {
		db = db.Where(`models LIKE ? ESCAPE '\'`, modelsJSONLikePattern(filter.ModelName))
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
	var rows []models.PrivateChannel
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (q *adminPrivateChannelQuery) ListAcrossOwners(opts ListOptions, filter PrivateChannelFilter) ([]models.PrivateChannel, int64, error) {
	db := q.ctx.GetDB().Model(&models.PrivateChannel{})
	if filter.OwnerID != nil {
		db = db.Where("owner_id = ?", *filter.OwnerID)
	}
	if filter.Search != "" {
		db = db.Where("name LIKE ?", "%"+filter.Search+"%")
	}
	if filter.ModelName != "" {
		db = db.Where(`models LIKE ? ESCAPE '\'`, modelsJSONLikePattern(filter.ModelName))
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
	var rows []models.PrivateChannel
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

// modelsJSONLikePattern builds a LIKE pattern that matches a JSON token
// `"<modelName>"` inside the `models` column (datatypes.JSONSlice[string],
// serialized as a JSON array of quoted strings, e.g. `["gpt-4","claude"]`).
// The surrounding double-quotes anchor the match at JSON token boundaries
// so a query for "gpt-4" does NOT accidentally hit "gpt-4-turbo".
//
// Note this is still a non-indexed LIKE — acceptable because:
//   - per-user BYOK row counts are small (CountByOwner is the limiter), and
//   - the alternative (JSON_EACH) is dialect-specific and not portable across
//     the SQLite test backend and MySQL production backend.
//
// Escape % and _ in the user-supplied modelName so wildcard characters in the
// model name itself (rare but possible: e.g. "gpt_4") don't broaden the match.
func modelsJSONLikePattern(modelName string) string {
	return `%"` + likeEscape(modelName) + `"%`
}

// CountByBaseURLPrefix returns (total_count, preview_pairs, err).
// total_count is unbounded; preview_pairs is capped at BaseURLUsagePreviewLimit.
// SQL LIKE uses a `?` placeholder for the prefix value, so SQL injection via the
// prefix argument is not possible. We intentionally treat prefix as an opaque
// string prefix (not URL-parsed) — the admin UI only needs to know "which
// channels' raw stored base_url starts with this exact characters", because
// that's the form the admin Settings page also stored it as. URL-segment-aware
// matching belongs in the SSRF validator, not here.
func (q *adminPrivateChannelQuery) CountByBaseURLPrefix(prefix string) (int64, []ChannelOwnerPair, error) {
	// Escape LIKE wildcards (% and _) in the prefix so a user-typed underscore or
	// percent in the BaseURL doesn't widen the match. We also escape backslash
	// to keep cross-engine behaviour predictable.
	escaped := likeEscape(prefix)
	pattern := escaped + "%"

	var count int64
	if err := q.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("base_url LIKE ? ESCAPE '\\'", pattern).
		Count(&count).Error; err != nil {
		return 0, nil, err
	}

	var pairs []ChannelOwnerPair
	if count == 0 {
		return 0, pairs, nil
	}
	if err := q.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("base_url LIKE ? ESCAPE '\\'", pattern).
		Select("owner_id, name AS channel_name").
		Order("id ASC").
		Limit(BaseURLUsagePreviewLimit).
		Scan(&pairs).Error; err != nil {
		return count, nil, err
	}
	return count, pairs, nil
}

// likeEscape escapes LIKE wildcards in user-supplied prefix strings: %, _, and \.
// Paired with the `ESCAPE '\'` clause in the LIKE expression. Without this, an
// admin-entered prefix like "https://api_test.com" would also match
// "https://apiXtest.com" — surprising and (in theory) exploitable when a hostile
// admin co-tenant tries to bypass scoping. The escape is cheap; do it always.
func likeEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '%' || c == '_' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(out)
}

// ListVisibleTo returns enabled private channels visible to userID:
//
//	owner=u  ∪  share.target=(user,u)  ∪  share.target=(group, g∈u.groups)
//
// Single SQL with OR + IN subquery (no N+1).
// When share table is empty (v1 default), degenerates to WHERE owner_id=u AND status=1.
func (q *adminPrivateChannelQuery) ListVisibleTo(userID uint, userGroupIDs []uint) ([]models.PrivateChannel, error) {
	if userID == 0 {
		return nil, nil
	}
	db := q.ctx.GetDB().Model(&models.PrivateChannel{}).Where("status = 1")
	db = db.Where(`
		owner_id = ? OR id IN (SELECT channel_id FROM private_channel_shares
			WHERE (target_type = 'user' AND target_id = ?)
			   OR (target_type = 'group' AND target_id IN ?))`,
		userID, userID, normalizeGroupIDs(userGroupIDs))

	var rows []models.PrivateChannel
	err := db.Order("priority DESC, id DESC").Find(&rows).Error
	return rows, err
}

func (q *adminPrivateChannelShareQuery) ListSharesForUser(userID uint, groupIDs []uint) ([]models.PrivateChannelShare, error) {
	var rows []models.PrivateChannelShare
	err := q.ctx.GetDB().Where(
		"(target_type = 'user' AND target_id = ?) OR (target_type = 'group' AND target_id IN ?)",
		userID, normalizeGroupIDs(groupIDs),
	).Find(&rows).Error
	return rows, err
}

func (q *adminPrivateChannelShareQuery) ListSharesByChannel(channelID uint) ([]models.PrivateChannelShare, error) {
	var rows []models.PrivateChannelShare
	err := q.ctx.GetDB().Where("channel_id = ?", channelID).Find(&rows).Error
	return rows, err
}

// normalizeGroupIDs replaces empty slice with {0} so the SQL `IN ?` expression doesn't fail.
// No user can have group_id=0 (default group is id=1), so {0} is a safe "no match" sentinel.
func normalizeGroupIDs(g []uint) []uint {
	if len(g) == 0 {
		return []uint{0}
	}
	return g
}

// AdminPrivateChannelMutation is the write surface for PrivateChannel.
// Update/Delete use a double-factor (id, owner_id) WHERE clause to prevent
// cross-owner mutation via forged ids at the portal layer.
type AdminPrivateChannelMutation interface {
	Create(pc *models.PrivateChannel) error
	Update(id, ownerID uint, patch map[string]any) error
	UpdateKey(id, ownerID uint, newCipher []byte, newLast4 string) error
	Delete(id, ownerID uint) error
	// AdminDisable is the admin-only kill switch; does not check owner_id.
	AdminDisable(id uint) error
	// DeleteByOwner removes every private_channel row owned by ownerID.
	// Used by the user-delete cascade to purge BYOK ciphertext when a user
	// account is removed (GDPR/SOC2 right-to-erasure).
	// Returns nil when no rows match.
	DeleteByOwner(ownerID uint) error
}

// AdminPrivateChannelShareMutation is the write surface for PrivateChannelShare.
// Share rows are normally created/removed implicitly via channel ops; this
// mutation surface exists to support the user-delete cascade, which must
// strip every share whose target is the deleted user.
type AdminPrivateChannelShareMutation interface {
	// DeleteSharesByTarget removes every share row where (target_type, target_id)
	// matches. Use PrivateShareTargetUser when cascading a user delete.
	// Returns nil when no rows match.
	DeleteSharesByTarget(targetType string, targetID uint) error
}

type adminPrivateChannelMutation struct{ ctx *baseContext }
type adminPrivateChannelShareMutation struct{ ctx *baseContext }

// privateChannelReservedPatchKeys are fields a portal client can never set
// via Update (key changes go through UpdateKey; owner/id/timestamps are immutable).
var privateChannelReservedPatchKeys = []string{
	"id", "owner_id", "key_cipher", "key_last4", "created_at",
}

func (m *adminPrivateChannelMutation) Create(pc *models.PrivateChannel) error {
	if err := m.ctx.GetDB().Create(pc).Error; err != nil {
		return err
	}
	m.refreshOwnerGauge(pc.OwnerID)
	return nil
}

func (m *adminPrivateChannelMutation) Update(id, ownerID uint, patch map[string]any) error {
	for _, k := range privateChannelReservedPatchKeys {
		delete(patch, k)
	}
	return m.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("id = ? AND owner_id = ?", id, ownerID).
		Updates(patch).Error
}

func (m *adminPrivateChannelMutation) UpdateKey(id, ownerID uint, newCipher []byte, newLast4 string) error {
	return m.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("id = ? AND owner_id = ?", id, ownerID).
		Updates(map[string]any{
			"key_cipher": newCipher,
			"key_last4":  newLast4,
		}).Error
}

func (m *adminPrivateChannelMutation) Delete(id, ownerID uint) error {
	if err := m.ctx.GetDB().
		Where("id = ? AND owner_id = ?", id, ownerID).
		Delete(&models.PrivateChannel{}).Error; err != nil {
		return err
	}
	m.refreshOwnerGauge(ownerID)
	return nil
}

func (m *adminPrivateChannelMutation) AdminDisable(id uint) error {
	return m.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("id = ?", id).
		Update("status", 0).Error
}

func (m *adminPrivateChannelMutation) DeleteByOwner(ownerID uint) error {
	if err := m.ctx.GetDB().
		Where("owner_id = ?", ownerID).
		Delete(&models.PrivateChannel{}).Error; err != nil {
		return err
	}
	// 整 owner 全删后归零；用 Set(0) 而不是 Delete 标签，方便 alerting
	// 看到"曾经有数据"的 owner 回落到 0 而非彻底消失。
	metrics.BYOKPrivateChannelCount.WithLabelValues(strconv.FormatUint(uint64(ownerID), 10)).Set(0)
	return nil
}

// refreshOwnerGauge 在 Create/Delete 后重新 CountByOwner 并 Set GaugeVec。
// 走 SQL 一次是为了避免并发写下的 +1/-1 漂移（如多 master 并行 mutation）。
// 出错时不传递 — metric 失败不该污染业务路径，仅静默丢失一次更新（下次
// mutation 会再 Set 覆盖）。
func (m *adminPrivateChannelMutation) refreshOwnerGauge(ownerID uint) {
	if ownerID == 0 {
		return
	}
	var n int64
	if err := m.ctx.GetDB().Model(&models.PrivateChannel{}).
		Where("owner_id = ?", ownerID).Count(&n).Error; err != nil {
		return
	}
	metrics.BYOKPrivateChannelCount.WithLabelValues(strconv.FormatUint(uint64(ownerID), 10)).Set(float64(n))
}

func (m *adminPrivateChannelShareMutation) DeleteSharesByTarget(targetType string, targetID uint) error {
	return m.ctx.GetDB().
		Where("target_type = ? AND target_id = ?", targetType, targetID).
		Delete(&models.PrivateChannelShare{}).Error
}
