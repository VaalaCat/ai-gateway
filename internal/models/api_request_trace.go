package models

import "gorm.io/datatypes"

type APIBodyCapture struct {
	Captured      bool   `json:"captured"`
	Status        string `json:"status,omitempty"`
	SkipReason    string `json:"skip_reason,omitempty"`
	Data          string `json:"data,omitempty"`
	CapturedBytes int64  `json:"captured_bytes,omitempty"`
	TotalBytes    int64  `json:"total_bytes,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
}

// APIRequestTrace is intentionally independent from the LLM RequestTrace.
// Task 12 adds the generic API capture payload; this entity establishes the
// separate log-database persistence boundary now without reusing LLM fields.
type APIRequestTrace struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	RequestID string `gorm:"size:64;uniqueIndex" json:"request_id"`

	SourceRequestHeaders           datatypes.JSONType[map[string][]string] `gorm:"type:text" json:"source_request_headers"`
	SourceRequestTrailers          datatypes.JSONType[map[string][]string] `gorm:"type:text" json:"source_request_trailers"`
	SourceRequestHeadersTruncated  bool                                    `json:"source_request_headers_truncated"`
	SourceRequestTrailersTruncated bool                                    `json:"source_request_trailers_truncated"`
	SourceRequestBody              datatypes.JSONType[APIBodyCapture]      `gorm:"type:text" json:"source_request_body"`

	RequestHeaders           datatypes.JSONType[map[string][]string] `gorm:"type:text" json:"request_headers"`
	RequestTrailers          datatypes.JSONType[map[string][]string] `gorm:"type:text" json:"request_trailers"`
	RequestHeadersTruncated  bool                                    `json:"request_headers_truncated"`
	RequestTrailersTruncated bool                                    `json:"request_trailers_truncated"`
	RequestBody              datatypes.JSONType[APIBodyCapture]      `gorm:"type:text" json:"request_body"`

	ResponseHeaders           datatypes.JSONType[map[string][]string] `gorm:"type:text" json:"response_headers"`
	ResponseTrailers          datatypes.JSONType[map[string][]string] `gorm:"type:text" json:"response_trailers"`
	ResponseHeadersTruncated  bool                                    `json:"response_headers_truncated"`
	ResponseTrailersTruncated bool                                    `json:"response_trailers_truncated"`
	ResponseBody              datatypes.JSONType[APIBodyCapture]      `gorm:"type:text" json:"response_body"`
	CreatedAt                 int64                                   `gorm:"autoCreateTime;index" json:"created_at"`
}
