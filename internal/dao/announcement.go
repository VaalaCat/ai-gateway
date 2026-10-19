package dao

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidAnnouncement = errors.New("invalid announcement")
var ErrAnnouncementNotVisible = errors.New("announcement is not visible")
var ErrAnnouncementRevisionConflict = errors.New("announcement revision has changed")

type AnnouncementQuery interface {
	List() ([]models.Announcement, error)
	ListPublic() ([]models.Announcement, error)
}

type AnnouncementMutation interface {
	Create(announcement *models.Announcement) error
	Update(announcement *models.Announcement, reannounce bool) error
	Delete(id uint) error
}

type UserAnnouncementQuery interface {
	List() ([]models.ReceivedAnnouncement, error)
}

type UserAnnouncementMutation interface {
	MarkSeen(id, revision uint) error
}

type announcementQuery struct{ ctx *baseContext }
type announcementMutation struct{ ctx *baseContext }
type userAnnouncementQuery struct{ ctx *userContextImpl }
type userAnnouncementMutation struct{ ctx *userContextImpl }

// Both listing and acknowledgement resolve users.group_id at query time, never
// from JWT claims or cached profile data. A missing user receives nothing.
func visibleAnnouncements(db *gorm.DB, userID uint) *gorm.DB {
	return db.Model(&models.Announcement{}).
		Joins("JOIN users AS recipient ON recipient.id = ?", userID).
		Where(`(NOT EXISTS (SELECT 1 FROM announcement_groups AS targets WHERE targets.announcement_id = announcements.id)
			OR EXISTS (SELECT 1 FROM announcement_groups AS targets WHERE targets.announcement_id = announcements.id AND targets.group_id = recipient.group_id))`)
}

func loadAnnouncementGroups(db *gorm.DB, rows []models.Announcement) error {
	if len(rows) == 0 {
		return nil
	}
	byID := make(map[uint]int, len(rows))
	ids := make([]uint, len(rows))
	for i := range rows {
		rows[i].GroupIDs = []uint{}
		ids[i] = rows[i].ID
		byID[rows[i].ID] = i
	}
	// Bound SQL parameters even for a large administrative history.
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		var targets []models.AnnouncementGroup
		if err := db.Where("announcement_id IN ?", ids[start:end]).Order("group_id ASC").Find(&targets).Error; err != nil {
			return err
		}
		for _, target := range targets {
			i := byID[target.AnnouncementID]
			rows[i].GroupIDs = append(rows[i].GroupIDs, target.GroupID)
		}
	}
	return nil
}

func (q *announcementQuery) List() ([]models.Announcement, error) {
	rows := []models.Announcement{}
	db := q.ctx.GetCoreDB()
	if err := db.Order("pinned DESC, created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, loadAnnouncementGroups(db, rows)
}

func (q *announcementQuery) ListPublic() ([]models.Announcement, error) {
	rows := []models.Announcement{}
	err := q.ctx.GetCoreDB().Where("show_for_guests = ?", true).Order("pinned DESC, created_at DESC, id DESC").Find(&rows).Error
	for i := range rows {
		rows[i].GroupIDs = []uint{}
	}
	return rows, err
}

func (q *userAnnouncementQuery) List() ([]models.ReceivedAnnouncement, error) {
	rows := []models.ReceivedAnnouncement{}
	db := q.ctx.GetCoreDB()
	err := visibleAnnouncements(db, q.ctx.UserInfo().UserID).
		Select("announcements.*, CASE WHEN receipt.user_id IS NULL THEN 0 ELSE 1 END AS seen").
		Joins("LEFT JOIN announcement_seens AS receipt ON receipt.announcement_id = announcements.id AND receipt.user_id = recipient.id AND receipt.revision = announcements.revision").
		Order("announcements.pinned DESC, announcements.created_at DESC, announcements.id DESC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	announcements := make([]models.Announcement, len(rows))
	for i := range rows {
		announcements[i] = rows[i].Announcement
	}
	if err := loadAnnouncementGroups(db, announcements); err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].GroupIDs = announcements[i].GroupIDs
	}
	return rows, nil
}

func validateAnnouncement(db *gorm.DB, announcement *models.Announcement) error {
	announcement.Title = strings.TrimSpace(announcement.Title)
	announcement.Content = strings.TrimSpace(announcement.Content)
	if n := utf8.RuneCountInString(announcement.Title); n == 0 || n > 200 {
		return fmt.Errorf("%w: title must contain 1 to 200 characters", ErrInvalidAnnouncement)
	}
	if n := utf8.RuneCountInString(announcement.Content); n == 0 || n > 20000 {
		return fmt.Errorf("%w: content must contain 1 to 20000 characters", ErrInvalidAnnouncement)
	}
	unique := make(map[uint]struct{}, len(announcement.GroupIDs))
	for _, id := range announcement.GroupIDs {
		if _, exists := unique[id]; exists || id == 0 {
			return fmt.Errorf("%w: group_ids must contain unique positive IDs", ErrInvalidAnnouncement)
		}
		unique[id] = struct{}{}
	}
	for start := 0; start < len(announcement.GroupIDs); start += 500 {
		end := min(start+500, len(announcement.GroupIDs))
		var count int64
		if err := db.Model(&models.UserGroup{}).Where("id IN ?", announcement.GroupIDs[start:end]).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(end-start) {
			return fmt.Errorf("%w: one or more user groups do not exist", ErrInvalidAnnouncement)
		}
	}
	if announcement.GroupIDs == nil {
		announcement.GroupIDs = []uint{}
	}
	return nil
}

func createAnnouncementTargets(db *gorm.DB, announcement *models.Announcement) error {
	if len(announcement.GroupIDs) == 0 {
		return nil
	}
	targets := make([]models.AnnouncementGroup, len(announcement.GroupIDs))
	for i, id := range announcement.GroupIDs {
		targets[i] = models.AnnouncementGroup{AnnouncementID: announcement.ID, GroupID: id}
	}
	return db.CreateInBatches(&targets, 250).Error
}

func (m *announcementMutation) Create(announcement *models.Announcement) error {
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
		if err := validateAnnouncement(tx, announcement); err != nil {
			return err
		}
		announcement.Revision = 1
		if err := tx.Create(announcement).Error; err != nil {
			return err
		}
		return createAnnouncementTargets(tx, announcement)
	})
}

func (m *announcementMutation) Update(announcement *models.Announcement, reannounce bool) error {
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
		if err := validateAnnouncement(tx, announcement); err != nil {
			return err
		}
		var existing models.Announcement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, announcement.ID).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"title": announcement.Title, "content": announcement.Content,
			"show_for_guests": announcement.ShowForGuests, "pinned": announcement.Pinned,
		}
		if reannounce {
			updates["revision"] = gorm.Expr("revision + 1")
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("announcement_id = ?", announcement.ID).Delete(&models.AnnouncementGroup{}).Error; err != nil {
			return err
		}
		if err := createAnnouncementTargets(tx, announcement); err != nil {
			return err
		}
		if err := tx.First(&existing, announcement.ID).Error; err != nil {
			return err
		}
		announcement.Revision = existing.Revision
		announcement.CreatedAt = existing.CreatedAt
		announcement.UpdatedAt = existing.UpdatedAt
		return nil
	})
}

func (m *announcementMutation) Delete(id uint) error {
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&models.Announcement{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("announcement_id = ?", id).Delete(&models.AnnouncementGroup{}).Error; err != nil {
			return err
		}
		return tx.Where("announcement_id = ?", id).Delete(&models.AnnouncementSeen{}).Error
	})
}

func (m *userAnnouncementMutation) MarkSeen(id, revision uint) error {
	if revision == 0 {
		return fmt.Errorf("%w: revision must be positive", ErrInvalidAnnouncement)
	}
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
		var announcement models.Announcement
		err := visibleAnnouncements(tx, m.ctx.UserInfo().UserID).
			Select("announcements.*").
			Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "announcements"}}).
			Where("announcements.id = ?", id).Take(&announcement).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAnnouncementNotVisible
		}
		if err != nil {
			return err
		}
		if announcement.Revision != revision {
			return ErrAnnouncementRevisionConflict
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "announcement_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"revision", "created_at"}),
			Where: clause.Where{Exprs: []clause.Expression{
				clause.Lt{Column: clause.Column{Table: "announcement_seens", Name: "revision"}, Value: revision},
			}},
		}).Create(&models.AnnouncementSeen{
			UserID: m.ctx.UserInfo().UserID, AnnouncementID: id, Revision: revision,
		}).Error
	})
}
