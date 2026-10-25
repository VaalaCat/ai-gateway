package models

// Announcement targets all authenticated users when it has no group rows.
// Guest visibility is independent of authenticated recipient groups.
type Announcement struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	Title         string `gorm:"size:200;not null" json:"title"`
	Content       string `gorm:"type:text;not null" json:"content"`
	ShowForGuests bool   `gorm:"not null" json:"show_for_guests"`
	Pinned        bool   `gorm:"not null;default:false" json:"pinned"`
	Revision      uint   `gorm:"not null;default:1" json:"revision"`
	GroupIDs      []uint `gorm:"-" json:"group_ids"`
	CreatedAt     int64  `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt     int64  `gorm:"autoUpdateTime" json:"updated_at"`
}

// Keep target rows when a group is deleted: dropping the final target would
// accidentally turn a private announcement into a global one.
type AnnouncementGroup struct {
	AnnouncementID uint `gorm:"primaryKey;autoIncrement:false"`
	GroupID        uint `gorm:"primaryKey;autoIncrement:false;index"`
}

type AnnouncementSeen struct {
	UserID         uint  `gorm:"primaryKey;autoIncrement:false"`
	AnnouncementID uint  `gorm:"primaryKey;autoIncrement:false;index"`
	Revision       uint  `gorm:"not null;default:1"`
	CreatedAt      int64 `gorm:"autoCreateTime"`
}

type ReceivedAnnouncement struct {
	Announcement
	Seen bool `json:"seen"`
}

// AnnouncementSummary deliberately excludes the body from history pages.
type AnnouncementSummary struct {
	ID            uint   `json:"id"`
	Title         string `json:"title"`
	ShowForGuests bool   `json:"show_for_guests"`
	Pinned        bool   `json:"pinned"`
	Revision      uint   `json:"revision"`
	GroupIDs      []uint `gorm:"-" json:"group_ids"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

type ReceivedAnnouncementSummary struct {
	AnnouncementSummary
	Seen bool `json:"seen"`
}

type AnnouncementPage[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}
