package models

type UsageLogTrace struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	RequestID          string `gorm:"size:64;uniqueIndex" json:"request_id"`
	InboundPath        string `gorm:"size:256" json:"inbound_path"`
	OutboundPath       string `gorm:"size:256" json:"outbound_path"`
	InboundHeaders     string `gorm:"type:text" json:"inbound_headers"`
	OutboundHeaders    string `gorm:"type:text" json:"outbound_headers"`
	InboundBody        string `gorm:"type:text" json:"inbound_body"`
	OutboundBody       string `gorm:"type:text" json:"outbound_body"`
	ResponseHeaders    string `gorm:"type:text" json:"response_headers"`
	ResponseBody       string `gorm:"type:text" json:"response_body"`
	ClientResponseBody string `gorm:"type:text" json:"client_response_body"`
	UpstreamStatus     int    `json:"upstream_status"`
	ErrorStage         string `gorm:"size:32;index" json:"error_stage"`
	CreatedAt          int64  `gorm:"autoCreateTime;index" json:"created_at"`
}
