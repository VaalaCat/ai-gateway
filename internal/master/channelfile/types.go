package channelfile

import (
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const (
	SchemaVersion = 1
	MaxFileBytes  = 16 << 20
	MaxChannels   = 1000
	MaxNameBytes  = 64
)

type Kind string

const (
	KindAdminChannels Kind = "admin_channels"
	KindBYOKChannels  Kind = "byok_channels"
)

type Envelope[T any] struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          Kind      `json:"kind"`
	ExportedAt    time.Time `json:"exported_at"`
	Channels      []T       `json:"channels"`
}

func NewEnvelope[T any](kind Kind, now time.Time, channels []T) Envelope[T] {
	if channels == nil {
		channels = []T{}
	}
	return Envelope[T]{
		SchemaVersion: SchemaVersion,
		Kind:          kind,
		ExportedAt:    now.UTC(),
		Channels:      channels,
	}
}

type AdminChannel struct {
	Name                string                   `json:"name"`
	Status              int                      `json:"status"`
	Type                int                      `json:"type"`
	Key                 string                   `json:"key"`
	BaseURL             string                   `json:"base_url"`
	Models              []string                 `json:"models"`
	ModelMapping        map[string]string        `json:"model_mapping"`
	Weight              uint                     `json:"weight"`
	Priority            int                      `json:"priority"`
	ProxyURL            string                   `json:"proxy_url"`
	HeaderOverride      string                   `json:"header_override"`
	SupportedAPITypes   string                   `json:"supported_api_types"`
	Endpoints           string                   `json:"endpoints"`
	PassthroughEnabled  bool                     `json:"passthrough_enabled"`
	UseLegacyAdaptor    bool                     `json:"use_legacy_adaptor"`
	Organization        string                   `json:"organization"`
	APIVersion          string                   `json:"api_version"`
	SystemPrompt        string                   `json:"system_prompt"`
	SystemPromptInInput bool                     `json:"system_prompt_in_input"`
	RoleMapping         string                   `json:"role_mapping"`
	ParamOverride       string                   `json:"param_override"`
	Setting             string                   `json:"setting"`
	Tag                 string                   `json:"tag"`
	Remark              string                   `json:"remark"`
	TestModel           string                   `json:"test_model"`
	AutoBan             int                      `json:"auto_ban"`
	StatusCodeMapping   string                   `json:"status_code_mapping"`
	OtherSettings       string                   `json:"other_settings"`
	DisableKeepalive    bool                     `json:"disable_keepalive"`
	Resilience          models.ChannelResilience `json:"resilience"`
	PriceRatio          float64                  `json:"price_ratio"`
	Free                bool                     `json:"free"`
	Limit               models.ChannelLimit      `json:"limit"`
	Affinity            models.ChannelAffinity   `json:"affinity"`
}

type BYOKChannel struct {
	Name                string                 `json:"name"`
	Status              int                    `json:"status"`
	Type                int                    `json:"type"`
	Key                 string                 `json:"key"`
	BaseURL             string                 `json:"base_url"`
	Models              []string               `json:"models"`
	ModelMapping        map[string]string      `json:"model_mapping"`
	Weight              uint                   `json:"weight"`
	Priority            int                    `json:"priority"`
	SupportedAPITypes   string                 `json:"supported_api_types"`
	Endpoints           string                 `json:"endpoints"`
	PassthroughEnabled  bool                   `json:"passthrough_enabled"`
	UseLegacyAdaptor    bool                   `json:"use_legacy_adaptor"`
	Organization        string                 `json:"organization"`
	APIVersion          string                 `json:"api_version"`
	SystemPrompt        string                 `json:"system_prompt"`
	SystemPromptInInput bool                   `json:"system_prompt_in_input"`
	RoleMapping         string                 `json:"role_mapping"`
	ParamOverride       string                 `json:"param_override"`
	Setting             string                 `json:"setting"`
	Tag                 string                 `json:"tag"`
	Remark              string                 `json:"remark"`
	TestModel           string                 `json:"test_model"`
	AutoBan             int                    `json:"auto_ban"`
	StatusCodeMapping   string                 `json:"status_code_mapping"`
	OtherSettings       string                 `json:"other_settings"`
	Affinity            models.ChannelAffinity `json:"affinity"`
}

type SelectionMode string

const (
	SelectionIDs    SelectionMode = "ids"
	SelectionFilter SelectionMode = "filter"
)

type Selection[F any] struct {
	Mode   SelectionMode `json:"mode" binding:"required"`
	IDs    []uint        `json:"ids,omitempty"`
	Filter F             `json:"filter,omitempty"`
}

type ExportRequest[F any] struct {
	Selection Selection[F] `json:"selection" binding:"required"`
}

type ItemIssue struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type PreviewItem struct {
	Index      int         `json:"index"`
	SourceName string      `json:"source_name"`
	FinalName  string      `json:"final_name"`
	Warnings   []ItemIssue `json:"warnings"`
	Error      *ItemIssue  `json:"error,omitempty"`
}

type Preview struct {
	Kind   Kind          `json:"kind"`
	Total  int           `json:"total"`
	Ready  int           `json:"ready"`
	Failed int           `json:"failed"`
	Items  []PreviewItem `json:"items"`
}

type ImportResultItem struct {
	ID         uint   `json:"id"`
	SourceName string `json:"source_name"`
	FinalName  string `json:"final_name"`
}

type ImportResult struct {
	Kind    Kind               `json:"kind"`
	Created int                `json:"created"`
	Items   []ImportResultItem `json:"items"`
}

type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func NewError(code string, err error) error {
	return &Error{Code: code, Err: err}
}
