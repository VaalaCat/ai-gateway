package models

import (
	"encoding/json"
	"fmt"
)

// OpenAPIServiceDocument contains service-level OpenAPI facts. APIService owns
// the exported info title and description, so they are deliberately absent
// from OpenAPIInfo.
type OpenAPIServiceDocument struct {
	Version           string                       `json:"openapi,omitempty"`
	JSONSchemaDialect string                       `json:"jsonSchemaDialect,omitempty"`
	Info              OpenAPIInfo                  `json:"info,omitempty"`
	Tags              []OpenAPITag                 `json:"tags,omitempty"`
	Servers           []OpenAPIServer              `json:"servers,omitempty"`
	ExternalDocs      *OpenAPIExternalDocs         `json:"externalDocs,omitempty"`
	Security          []OpenAPISecurityRequirement `json:"security,omitempty"`
	Components        OpenAPIComponents            `json:"components,omitempty"`
	Extensions        map[string]json.RawMessage   `json:"extensions,omitempty"`
}

type OpenAPIInfo struct {
	Summary        string                     `json:"summary,omitempty"`
	Version        string                     `json:"version,omitempty"`
	TermsOfService string                     `json:"termsOfService,omitempty"`
	Contact        *OpenAPIContact            `json:"contact,omitempty"`
	License        *OpenAPILicense            `json:"license,omitempty"`
	Extensions     map[string]json.RawMessage `json:"extensions,omitempty"`
}

type OpenAPIContact struct {
	Name       string                     `json:"name,omitempty"`
	URL        string                     `json:"url,omitempty"`
	Email      string                     `json:"email,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type OpenAPILicense struct {
	Name       string                     `json:"name,omitempty"`
	Identifier string                     `json:"identifier,omitempty"`
	URL        string                     `json:"url,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type OpenAPITag struct {
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	ExternalDocs *OpenAPIExternalDocs       `json:"externalDocs,omitempty"`
	Extensions   map[string]json.RawMessage `json:"extensions,omitempty"`
}

type OpenAPIExternalDocs struct {
	Description string                     `json:"description,omitempty"`
	URL         string                     `json:"url"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

type OpenAPIServer struct {
	URL         string                           `json:"url"`
	Description string                           `json:"description,omitempty"`
	Variables   map[string]OpenAPIServerVariable `json:"variables,omitempty"`
	Extensions  map[string]json.RawMessage       `json:"extensions,omitempty"`
}

type OpenAPIServerVariable struct {
	Default     string                     `json:"default"`
	Enum        []string                   `json:"enum,omitempty"`
	Description string                     `json:"description,omitempty"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

type OpenAPISecurityRequirement map[string][]string

// OpenAPIComponents keeps reusable objects in named fields. JSON Schema and
// component kinds whose complete semantics belong to OpenAPI are preserved as
// raw JSON values rather than flattened into string-encoded JSON.
type OpenAPIComponents struct {
	Schemas         map[string]json.RawMessage    `json:"schemas,omitempty"`
	Responses       map[string]OpenAPIResponse    `json:"responses,omitempty"`
	Parameters      map[string]OpenAPIParameter   `json:"parameters,omitempty"`
	Examples        map[string]json.RawMessage    `json:"examples,omitempty"`
	RequestBodies   map[string]OpenAPIRequestBody `json:"requestBodies,omitempty"`
	Headers         map[string]OpenAPIHeader      `json:"headers,omitempty"`
	SecuritySchemes map[string]json.RawMessage    `json:"securitySchemes,omitempty"`
	Links           map[string]json.RawMessage    `json:"links,omitempty"`
	Callbacks       map[string]json.RawMessage    `json:"callbacks,omitempty"`
	PathItems       map[string]OpenAPIPathItem    `json:"pathItems,omitempty"`
	Extensions      map[string]json.RawMessage    `json:"extensions,omitempty"`
}

type OpenAPIPathItem struct {
	Ref         string                      `json:"$ref,omitempty"`
	Summary     string                      `json:"summary,omitempty"`
	Description string                      `json:"description,omitempty"`
	Parameters  []OpenAPIParameter          `json:"parameters,omitempty"`
	Servers     []OpenAPIServer             `json:"servers,omitempty"`
	Operations  map[string]OpenAPIOperation `json:"operations,omitempty"`
	Extensions  map[string]json.RawMessage  `json:"extensions,omitempty"`
}

type OpenAPIOperation struct {
	Tags         []string                      `json:"tags,omitempty"`
	Summary      string                        `json:"summary,omitempty"`
	Description  string                        `json:"description,omitempty"`
	ExternalDocs *OpenAPIExternalDocs          `json:"externalDocs,omitempty"`
	OperationID  string                        `json:"operationId,omitempty"`
	Parameters   []OpenAPIParameter            `json:"parameters,omitempty"`
	RequestBody  *OpenAPIRequestBody           `json:"requestBody,omitempty"`
	Responses    map[string]OpenAPIResponse    `json:"responses,omitempty"`
	Callbacks    map[string]json.RawMessage    `json:"callbacks,omitempty"`
	Deprecated   bool                          `json:"deprecated,omitempty"`
	Security     *[]OpenAPISecurityRequirement `json:"security,omitempty"`
	Servers      []OpenAPIServer               `json:"servers,omitempty"`
	Extensions   map[string]json.RawMessage    `json:"extensions,omitempty"`
}

type OpenAPIParameter struct {
	Ref             string                      `json:"$ref,omitempty"`
	Name            string                      `json:"name,omitempty"`
	In              string                      `json:"in,omitempty"`
	Description     string                      `json:"description,omitempty"`
	Required        bool                        `json:"required,omitempty"`
	Deprecated      bool                        `json:"deprecated,omitempty"`
	AllowEmptyValue bool                        `json:"allowEmptyValue,omitempty"`
	Style           string                      `json:"style,omitempty"`
	Explode         *bool                       `json:"explode,omitempty"`
	AllowReserved   bool                        `json:"allowReserved,omitempty"`
	Schema          json.RawMessage             `json:"schema,omitempty"`
	Example         json.RawMessage             `json:"example,omitempty"`
	Examples        map[string]json.RawMessage  `json:"examples,omitempty"`
	Content         map[string]OpenAPIMediaType `json:"content,omitempty"`
	Extensions      map[string]json.RawMessage  `json:"extensions,omitempty"`
}

type OpenAPIRequestBody struct {
	Ref         string                      `json:"$ref,omitempty"`
	Description string                      `json:"description,omitempty"`
	Required    bool                        `json:"required,omitempty"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
	Extensions  map[string]json.RawMessage  `json:"extensions,omitempty"`
}

type OpenAPIResponse struct {
	Ref         string                      `json:"$ref,omitempty"`
	Description string                      `json:"description,omitempty"`
	Headers     map[string]OpenAPIHeader    `json:"headers,omitempty"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
	Links       map[string]json.RawMessage  `json:"links,omitempty"`
	Extensions  map[string]json.RawMessage  `json:"extensions,omitempty"`
}

type OpenAPIHeader struct {
	Ref             string                      `json:"$ref,omitempty"`
	Description     string                      `json:"description,omitempty"`
	Required        bool                        `json:"required,omitempty"`
	Deprecated      bool                        `json:"deprecated,omitempty"`
	AllowEmptyValue bool                        `json:"allowEmptyValue,omitempty"`
	Style           string                      `json:"style,omitempty"`
	Explode         *bool                       `json:"explode,omitempty"`
	AllowReserved   bool                        `json:"allowReserved,omitempty"`
	Schema          json.RawMessage             `json:"schema,omitempty"`
	Example         json.RawMessage             `json:"example,omitempty"`
	Examples        map[string]json.RawMessage  `json:"examples,omitempty"`
	Content         map[string]OpenAPIMediaType `json:"content,omitempty"`
	Extensions      map[string]json.RawMessage  `json:"extensions,omitempty"`
}

type OpenAPIMediaType struct {
	Schema     json.RawMessage            `json:"schema,omitempty"`
	Example    json.RawMessage            `json:"example,omitempty"`
	Examples   map[string]json.RawMessage `json:"examples,omitempty"`
	Encoding   map[string]json.RawMessage `json:"encoding,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

func normalizeOpenAPIPathItemsForWrite(pathItems map[string]OpenAPIPathItem) (map[string]OpenAPIPathItem, error) {
	if pathItems == nil {
		return nil, nil
	}
	normalizedPathItems := make(map[string]OpenAPIPathItem, len(pathItems))
	for path, item := range pathItems {
		if item.Operations != nil {
			normalizedOperations := make(map[string]OpenAPIOperation, len(item.Operations))
			for method, operation := range item.Operations {
				normalizedMethod, valid := NormalizeStandardHTTPMethod(method)
				if !valid {
					return nil, fmt.Errorf("OpenAPI operation method must be a standard HTTP method other than CONNECT, got %q", method)
				}
				if _, duplicate := normalizedOperations[normalizedMethod]; duplicate {
					return nil, fmt.Errorf("duplicate OpenAPI operation method %q for path %q", normalizedMethod, path)
				}
				normalizedOperations[normalizedMethod] = operation
			}
			item.Operations = normalizedOperations
		}
		normalizedPathItems[path] = item
	}
	return normalizedPathItems, nil
}
