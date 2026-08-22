package models

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/net/http/httpguts"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type APIProtocol string

const (
	APIProtocolHTTP      APIProtocol = "http"
	APIProtocolWebSocket APIProtocol = "websocket"
)

var apiStandardMethods = map[string]struct{}{
	"DELETE": {}, "GET": {}, "HEAD": {}, "OPTIONS": {}, "PATCH": {}, "POST": {}, "PUT": {}, "TRACE": {},
}

// NormalizeStandardHTTPMethod returns the canonical uppercase form of a
// standard HTTP method supported by API routes. CONNECT is intentionally not
// supported because routes do not expose tunnel semantics.
func NormalizeStandardHTTPMethod(method string) (string, bool) {
	normalized := strings.ToUpper(method)
	_, ok := apiStandardMethods[normalized]
	if !ok {
		return "", false
	}
	return normalized, true
}

// APIRoute selects an APIService endpoint. An empty AllowedMethods set means
// every standard HTTP method except CONNECT is accepted.
type APIRoute struct {
	ID                    uint                                           `gorm:"primaryKey" json:"id"`
	APIServiceID          uint                                           `gorm:"not null;uniqueIndex:idx_api_route_service_slug" json:"api_service_id"`
	BackendID             uint                                           `gorm:"not null;index" json:"backend_id"`
	Slug                  string                                         `gorm:"size:64;not null;uniqueIndex:idx_api_route_service_slug" json:"slug"`
	Protocols             datatypes.JSONSlice[APIProtocol]               `gorm:"type:text;not null;default:'[\"http\"]'" json:"protocols"`
	AllowedMethods        datatypes.JSONSlice[string]                    `gorm:"type:text;not null;default:'[]'" json:"allowed_methods"`
	WebSocketSubprotocols datatypes.JSONSlice[string]                    `gorm:"type:text;not null;default:'[]'" json:"websocket_subprotocols"`
	UpstreamPath          string                                         `gorm:"type:text;not null;default:''" json:"upstream_path"`
	ForwardSubpath        bool                                           `gorm:"not null;default:false" json:"forward_subpath"`
	ExampleRequest        datatypes.JSONType[APIRequestExample]          `gorm:"type:text;not null;default:'{}'" json:"example_request"`
	Status                int                                            `gorm:"not null;default:1" json:"status"`
	OpenAPIPaths          datatypes.JSONType[map[string]OpenAPIPathItem] `gorm:"column:openapi_paths;type:text;not null;default:'{}'" json:"-"`
	CreatedAt             int64                                          `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt             int64                                          `gorm:"autoUpdateTime" json:"updated_at"`
}

func (r *APIRoute) NormalizeForWrite() error {
	openAPIPaths, err := normalizeOpenAPIPathItemsForWrite(r.OpenAPIPaths.Data())
	if err != nil {
		return err
	}
	r.OpenAPIPaths = datatypes.NewJSONType(openAPIPaths)

	protocols := r.Protocols
	if len(protocols) == 0 {
		protocols = datatypes.JSONSlice[APIProtocol]{APIProtocolHTTP}
	}
	seenProtocols := make(map[APIProtocol]struct{}, len(protocols))
	normalizedProtocols := make(datatypes.JSONSlice[APIProtocol], 0, len(protocols))
	for _, protocol := range protocols {
		if protocol != APIProtocolHTTP && protocol != APIProtocolWebSocket {
			return fmt.Errorf("api route protocol must be %q or %q, got %q", APIProtocolHTTP, APIProtocolWebSocket, protocol)
		}
		if _, exists := seenProtocols[protocol]; exists {
			continue
		}
		seenProtocols[protocol] = struct{}{}
		normalizedProtocols = append(normalizedProtocols, protocol)
	}
	sort.Slice(normalizedProtocols, func(i, j int) bool { return normalizedProtocols[i] < normalizedProtocols[j] })
	r.Protocols = normalizedProtocols

	seenMethods := make(map[string]struct{}, len(r.AllowedMethods))
	normalizedMethods := make(datatypes.JSONSlice[string], 0, len(r.AllowedMethods))
	for _, method := range r.AllowedMethods {
		normalizedMethod, valid := NormalizeStandardHTTPMethod(method)
		if !valid {
			return fmt.Errorf("api route method must be a standard HTTP method other than CONNECT, got %q", method)
		}
		if _, exists := seenMethods[normalizedMethod]; exists {
			continue
		}
		seenMethods[normalizedMethod] = struct{}{}
		normalizedMethods = append(normalizedMethods, normalizedMethod)
	}
	sort.Strings(normalizedMethods)
	r.AllowedMethods = normalizedMethods

	example, err := NormalizeAPIRequestExample(r.ExampleRequest.Data(), r.AllowedMethods)
	if err != nil {
		return err
	}
	r.ExampleRequest = datatypes.NewJSONType(example)

	seenSubprotocols := make(map[string]struct{}, len(r.WebSocketSubprotocols))
	normalizedSubprotocols := make(datatypes.JSONSlice[string], 0, len(r.WebSocketSubprotocols))
	for _, subprotocol := range r.WebSocketSubprotocols {
		subprotocol = strings.TrimSpace(subprotocol)
		if subprotocol == "" || !httpguts.ValidHeaderFieldName(subprotocol) {
			return fmt.Errorf("api route websocket subprotocol must be an HTTP token, got %q", subprotocol)
		}
		if _, exists := seenSubprotocols[subprotocol]; exists {
			continue
		}
		seenSubprotocols[subprotocol] = struct{}{}
		normalizedSubprotocols = append(normalizedSubprotocols, subprotocol)
	}
	sort.Strings(normalizedSubprotocols)
	r.WebSocketSubprotocols = normalizedSubprotocols
	return nil
}

func (r *APIRoute) Validate() error {
	if r.APIServiceID == 0 {
		return fmt.Errorf("api route api_service_id must not be zero")
	}
	if r.BackendID == 0 {
		return fmt.Errorf("api route backend_id must not be zero")
	}
	if err := validateAPIRouteSlug(r.Slug); err != nil {
		return err
	}
	if len(r.Protocols) == 0 {
		return fmt.Errorf("api route protocols must not be empty")
	}
	if err := validateAPIStatus("api route", int64(r.Status)); err != nil {
		return err
	}
	return nil
}

// BeforeCreate normalizes and validates the complete initial route. It must
// not run on partial GORM updates, whose receiver omits unrelated fields.
func (r *APIRoute) BeforeCreate(*gorm.DB) error {
	if err := r.NormalizeForWrite(); err != nil {
		return err
	}
	return r.Validate()
}

func (r *APIRoute) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		if err := r.NormalizeForWrite(); err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return err
		}
		return r.validatePersistedBackend(tx)
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "APIServiceID", validate: rejectAPIRouteServicePartialUpdate},
		apiPatchField{name: "Slug", validate: func(value any) error {
			slug, err := apiPatchString(value, "api route slug")
			if err != nil {
				return err
			}
			return validateAPIRouteSlug(slug)
		}},
		apiPatchField{name: "Status", validate: func(value any) error {
			status, err := apiPatchInt(value, "api route status")
			if err != nil {
				return err
			}
			return validateAPIStatus("api route", status)
		}},
		apiPatchField{name: "BackendID", validate: rejectAPIRouteBackendPartialUpdate},
		// JSON slice updates need NormalizeForWrite across the complete route so
		// their duplicate/sort/default contract cannot be applied safely here.
		// Load the row, patch it, then Save for a model-managed JSON update.
		apiPatchField{name: "Protocols", validate: rejectAPIRoutePartialJSONUpdate},
		apiPatchField{name: "AllowedMethods", validate: rejectAPIRoutePartialJSONUpdate},
		apiPatchField{name: "WebSocketSubprotocols", validate: rejectAPIRoutePartialJSONUpdate},
		apiPatchField{name: "ExampleRequest", validate: rejectAPIRoutePartialJSONUpdate},
	)
}

func (r *APIRoute) validatePersistedBackend(tx *gorm.DB) error {
	var persisted APIRoute
	if err := tx.Session(&gorm.Session{NewDB: true}).Select("api_service_id", "backend_id").First(&persisted, r.ID).Error; err != nil {
		return err
	}
	if persisted.APIServiceID != r.APIServiceID {
		return fmt.Errorf("api route api_service_id cannot be changed")
	}
	var backend APIBackend
	if err := tx.Session(&gorm.Session{NewDB: true}).Select("api_service_id").First(&backend, r.BackendID).Error; err != nil {
		return err
	}
	if backend.APIServiceID != r.APIServiceID {
		return fmt.Errorf("api route backend_id must belong to the same api service")
	}
	return nil
}

func rejectAPIRouteServicePartialUpdate(any) error {
	return fmt.Errorf("partial api route api_service_id updates are not supported")
}

func rejectAPIRouteBackendPartialUpdate(any) error {
	return fmt.Errorf("partial api route backend_id updates are not supported; load, validate ownership, and save the complete route")
}

func validateAPIRouteSlug(slug string) error {
	if slug == "" {
		return nil
	}
	if !apiSlugPattern.MatchString(slug) {
		return fmt.Errorf("api route slug must be lowercase URL-safe, got %q", slug)
	}
	return nil
}

func rejectAPIRoutePartialJSONUpdate(any) error {
	return fmt.Errorf("partial api route protocol/method updates are not supported; load, normalize, and save the complete route")
}
