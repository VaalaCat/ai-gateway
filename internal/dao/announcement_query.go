package dao

import (
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AnnouncementListOptions struct {
	Page     int
	PageSize int
}

const announcementDefaultPageLimit = 10

func parseAnnouncementPage(options AnnouncementListOptions) (AnnouncementListOptions, int, error) {
	if options.Page == 0 {
		options.Page = 1
	}
	if options.PageSize == 0 {
		options.PageSize = announcementDefaultPageLimit
	}
	if options.Page < 1 || options.PageSize < 1 || options.PageSize > 100 || options.Page-1 > int(^uint(0)>>1)/options.PageSize {
		return options, 0, fmt.Errorf("%w: invalid page or page_size", ErrInvalidAnnouncement)
	}
	return options, (options.Page - 1) * options.PageSize, nil
}

func announcementSummary(row models.Announcement) models.AnnouncementSummary {
	return models.AnnouncementSummary{
		ID: row.ID, Title: row.Title, GroupIDs: append([]uint{}, row.GroupIDs...),
		ShowForGuests: row.ShowForGuests, Pinned: row.Pinned, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func announcementDetail(summary models.AnnouncementSummary, content string) models.Announcement {
	return models.Announcement{
		ID: summary.ID, Title: summary.Title, Content: content,
		GroupIDs:      append([]uint{}, summary.GroupIDs...),
		ShowForGuests: summary.ShowForGuests, Pinned: summary.Pinned, Revision: summary.Revision,
		CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt,
	}
}

func (q *announcementQuery) List(options AnnouncementListOptions) (models.AnnouncementPage[models.AnnouncementSummary], error) {
	options, offset, err := parseAnnouncementPage(options)
	page := models.AnnouncementPage[models.AnnouncementSummary]{Items: []models.AnnouncementSummary{}, Page: options.Page, PageSize: options.PageSize}
	if err != nil {
		return page, err
	}
	cache := cacheForAnnouncements(q.ctx.GetCoreDB())
	cache.transactions.RLock()
	defer cache.transactions.RUnlock()
	page.Total = int64(len(cache.index.all))
	page.Items = cache.index.page(cache.index.all, nil, offset, options.PageSize)
	return page, nil
}

func (q *userAnnouncementQuery) List(options AnnouncementListOptions) (models.AnnouncementPage[models.ReceivedAnnouncementSummary], error) {
	options, offset, err := parseAnnouncementPage(options)
	page := models.AnnouncementPage[models.ReceivedAnnouncementSummary]{Items: []models.ReceivedAnnouncementSummary{}, Page: options.Page, PageSize: options.PageSize}
	if err != nil {
		return page, err
	}
	db := q.ctx.GetCoreDB()
	cache := cacheForAnnouncements(db)
	cache.transactions.RLock()
	defer cache.transactions.RUnlock()
	if info := q.ctx.UserInfo(); info == nil || info.UserID == 0 {
		page.Total = int64(len(cache.index.guests))
		for _, row := range cache.index.page(cache.index.guests, nil, offset, options.PageSize) {
			row.GroupIDs = []uint{}
			page.Items = append(page.Items, models.ReceivedAnnouncementSummary{AnnouncementSummary: row})
		}
		return page, nil
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Select("id, group_id").First(&user, q.ctx.UserInfo().UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		group := cache.index.byGroup[user.GroupID]
		page.Total = int64(len(cache.index.global) + len(group))
		rows := cache.index.page(cache.index.global, group, offset, options.PageSize)
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uint, len(rows))
		for i := range rows {
			ids[i] = rows[i].ID
		}
		var receipts []models.AnnouncementSeen
		if err := tx.Where("user_id = ? AND announcement_id IN ?", user.ID, ids).Find(&receipts).Error; err != nil {
			return err
		}
		seen := make(map[uint]uint, len(receipts))
		for _, receipt := range receipts {
			seen[receipt.AnnouncementID] = receipt.Revision
		}
		for _, row := range rows {
			page.Items = append(page.Items, models.ReceivedAnnouncementSummary{AnnouncementSummary: row, Seen: seen[row.ID] == row.Revision})
		}
		return nil
	})
	return page, err
}

func (q *announcementQuery) Get(id uint) (models.Announcement, error) {
	db := q.ctx.GetCoreDB()
	cache := cacheForAnnouncements(db)
	cache.transactions.RLock()
	defer cache.transactions.RUnlock()
	summary, exists := cache.index.byID[id]
	if !exists {
		return models.Announcement{}, gorm.ErrRecordNotFound
	}
	body, err := cache.body(db, id)
	if err != nil {
		return models.Announcement{}, err
	}
	return announcementDetail(summary, body), nil
}

func (q *userAnnouncementQuery) Get(id uint) (models.ReceivedAnnouncement, error) {
	db := q.ctx.GetCoreDB()
	cache := cacheForAnnouncements(db)
	cache.transactions.RLock()
	defer cache.transactions.RUnlock()
	var row models.ReceivedAnnouncement
	summary, exists := cache.index.byID[id]
	if info := q.ctx.UserInfo(); info == nil || info.UserID == 0 {
		if !exists || !summary.ShowForGuests {
			return row, gorm.ErrRecordNotFound
		}
		body, err := cache.body(db, id)
		if err != nil {
			return row, err
		}
		row.Announcement = announcementDetail(summary, body)
		row.GroupIDs = []uint{}
		return row, nil
	}
	if !exists {
		return row, ErrAnnouncementNotVisible
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Select("id, group_id").First(&user, q.ctx.UserInfo().UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAnnouncementNotVisible
			}
			return err
		}
		// Visibility comes from the current user group and synchronized summary,
		// never from a cached body or a previous authorization result.
		if !announcementVisible(summary, user.GroupID) {
			return ErrAnnouncementNotVisible
		}
		body, err := cache.body(tx, id)
		if err != nil {
			return err
		}
		row.Announcement = announcementDetail(summary, body)
		var receipt models.AnnouncementSeen
		err = tx.Where("user_id = ? AND announcement_id = ?", user.ID, id).Take(&receipt).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row.Seen = err == nil && receipt.Revision == row.Revision
		return nil
	})
	return row, err
}
