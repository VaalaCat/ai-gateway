package models

type UsageUserTTFTHistogram struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Date      string `gorm:"size:10;uniqueIndex:idx_uutth_bucket,priority:1" json:"date"`
	Hour      int    `gorm:"uniqueIndex:idx_uutth_bucket,priority:2" json:"hour"`
	UserID    uint   `gorm:"uniqueIndex:idx_uutth_bucket,priority:3;index" json:"user_id"`
	ModelName string `gorm:"size:128;uniqueIndex:idx_uutth_bucket,priority:4" json:"model_name"`

	MaxFirstResponseMs int64 `json:"max_first_response_ms"`
	// Fixed columns allow aggregation with direct SQL SUMs; JSON cannot serve that hot path.
	H0        int64 `json:"h0"`
	H1        int64 `json:"h1"`
	H2        int64 `json:"h2"`
	H3        int64 `json:"h3"`
	H4        int64 `json:"h4"`
	H5        int64 `json:"h5"`
	H6        int64 `json:"h6"`
	H7        int64 `json:"h7"`
	H8        int64 `json:"h8"`
	H9        int64 `json:"h9"`
	H10       int64 `json:"h10"`
	H11       int64 `json:"h11"`
	H12       int64 `json:"h12"`
	H13       int64 `json:"h13"`
	H14       int64 `json:"h14"`
	H15       int64 `json:"h15"`
	H16       int64 `json:"h16"`
	CreatedAt int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64 `gorm:"autoUpdateTime" json:"updated_at"`
}

func (UsageUserTTFTHistogram) TableName() string { return "usage_user_ttft_histograms" }

type UsageUserTPSHistogram struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Date      string `gorm:"size:10;uniqueIndex:idx_uutps_bucket,priority:1" json:"date"`
	Hour      int    `gorm:"uniqueIndex:idx_uutps_bucket,priority:2" json:"hour"`
	UserID    uint   `gorm:"uniqueIndex:idx_uutps_bucket,priority:3;index" json:"user_id"`
	ModelName string `gorm:"size:128;uniqueIndex:idx_uutps_bucket,priority:4" json:"model_name"`

	MaxTps int64 `json:"max_tps"`
	// Fixed columns allow aggregation with direct SQL SUMs; JSON cannot serve that hot path.
	H0        int64 `json:"h0"`
	H1        int64 `json:"h1"`
	H2        int64 `json:"h2"`
	H3        int64 `json:"h3"`
	H4        int64 `json:"h4"`
	H5        int64 `json:"h5"`
	H6        int64 `json:"h6"`
	H7        int64 `json:"h7"`
	H8        int64 `json:"h8"`
	H9        int64 `json:"h9"`
	H10       int64 `json:"h10"`
	H11       int64 `json:"h11"`
	H12       int64 `json:"h12"`
	H13       int64 `json:"h13"`
	H14       int64 `json:"h14"`
	H15       int64 `json:"h15"`
	H16       int64 `json:"h16"`
	CreatedAt int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64 `gorm:"autoUpdateTime" json:"updated_at"`
}

func (UsageUserTPSHistogram) TableName() string { return "usage_user_tps_histograms" }
