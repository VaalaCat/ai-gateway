package apiopenapi

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type documentJSON struct {
	OpenAPI           string                              `json:"openapi"`
	JSONSchemaDialect string                              `json:"jsonSchemaDialect"`
	Info              json.RawMessage                     `json:"info"`
	Tags              []json.RawMessage                   `json:"tags"`
	Servers           []json.RawMessage                   `json:"servers"`
	External          json.RawMessage                     `json:"externalDocs"`
	Security          []models.OpenAPISecurityRequirement `json:"security"`
	Components        json.RawMessage                     `json:"components"`
	Paths             map[string]json.RawMessage          `json:"paths"`
}

func decodeParsedDocument(raw []byte) (ParsedDocument, error) {
	var source documentJSON
	if err := json.Unmarshal(raw, &source); err != nil {
		return ParsedDocument{}, problemError("$", "invalid_document", "document could not be converted")
	}
	name, description, info, err := decodeInfo(source.Info)
	if err != nil {
		return ParsedDocument{}, err
	}
	document := models.OpenAPIServiceDocument{
		Version: source.OpenAPI, JSONSchemaDialect: source.JSONSchemaDialect, Info: info,
		Tags: decodeTags(source.Tags), Servers: decodeServers(source.Servers),
		ExternalDocs: decodeExternalDocs(source.External), Security: source.Security,
		Components: decodeComponents(source.Components), Extensions: extractExtensions(raw),
	}
	paths := make(map[string]models.OpenAPIPathItem, len(source.Paths))
	for path, item := range source.Paths {
		if !strings.HasPrefix(path, "/") {
			return ParsedDocument{}, problemError(appendJSONPath("$.paths", path), "invalid_path", "OpenAPI paths must start with /")
		}
		paths[path] = decodePathItem(item)
	}
	return ParsedDocument{Name: name, Description: description, Document: document, Paths: paths}, nil
}

func decodeInfo(raw json.RawMessage) (string, string, models.OpenAPIInfo, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", models.OpenAPIInfo{}, problemError("$.info", "invalid_document", "info is required")
	}
	var source struct {
		Title       string          `json:"title"`
		Summary     string          `json:"summary"`
		Description string          `json:"description"`
		Version     string          `json:"version"`
		Terms       string          `json:"termsOfService"`
		Contact     json.RawMessage `json:"contact"`
		License     json.RawMessage `json:"license"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return "", "", models.OpenAPIInfo{}, problemError("$.info", "invalid_document", "info must be an object")
	}
	info := models.OpenAPIInfo{
		Summary: source.Summary, Version: source.Version, TermsOfService: source.Terms,
		Contact: decodeContact(source.Contact), License: decodeLicense(source.License), Extensions: extractExtensions(raw),
	}
	return source.Title, source.Description, info, nil
}

func decodeContact(raw json.RawMessage) *models.OpenAPIContact {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value models.OpenAPIContact
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	return &value
}

func decodeLicense(raw json.RawMessage) *models.OpenAPILicense {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value models.OpenAPILicense
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	return &value
}

func decodeTags(rawTags []json.RawMessage) []models.OpenAPITag {
	tags := make([]models.OpenAPITag, 0, len(rawTags))
	for _, raw := range rawTags {
		var value models.OpenAPITag
		_ = json.Unmarshal(raw, &value)
		value.Extensions = extractExtensions(raw)
		value.ExternalDocs = decodeExternalDocs(objectField(raw, "externalDocs"))
		tags = append(tags, value)
	}
	return tags
}

func decodeExternalDocs(raw json.RawMessage) *models.OpenAPIExternalDocs {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value models.OpenAPIExternalDocs
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	return &value
}

func decodeServers(rawServers []json.RawMessage) []models.OpenAPIServer {
	servers := make([]models.OpenAPIServer, 0, len(rawServers))
	for _, raw := range rawServers {
		var value models.OpenAPIServer
		_ = json.Unmarshal(raw, &value)
		value.Extensions = extractExtensions(raw)
		variables := objectMap(objectField(raw, "variables"))
		for name, variableRaw := range variables {
			variable := value.Variables[name]
			variable.Extensions = extractExtensions(variableRaw)
			value.Variables[name] = variable
		}
		servers = append(servers, value)
	}
	return servers
}

func decodeComponents(raw json.RawMessage) models.OpenAPIComponents {
	if len(raw) == 0 || string(raw) == "null" {
		return models.OpenAPIComponents{}
	}
	var value models.OpenAPIComponents
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.Responses = decodeNamedResponses(objectMap(objectField(raw, "responses")))
	value.Parameters = decodeNamedParameters(objectMap(objectField(raw, "parameters")))
	value.RequestBodies = decodeNamedRequestBodies(objectMap(objectField(raw, "requestBodies")))
	value.Headers = decodeNamedHeaders(objectMap(objectField(raw, "headers")))
	value.PathItems = decodeNamedPathItems(objectMap(objectField(raw, "pathItems")))
	return value
}

func decodePathItem(raw json.RawMessage) models.OpenAPIPathItem {
	var value models.OpenAPIPathItem
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.Parameters = decodeParameters(arrayField(raw, "parameters"))
	value.Servers = decodeServers(arrayField(raw, "servers"))
	value.Operations = make(map[string]models.OpenAPIOperation)
	object := objectMap(raw)
	for method, operationRaw := range object {
		upper, lower, ok := normalizeOperationMethod(method)
		if ok && method == lower {
			value.Operations[upper] = decodeOperation(operationRaw)
		}
	}
	if len(value.Operations) == 0 {
		value.Operations = nil
	}
	return value
}

func decodeOperation(raw json.RawMessage) models.OpenAPIOperation {
	var value models.OpenAPIOperation
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.ExternalDocs = decodeExternalDocs(objectField(raw, "externalDocs"))
	value.Parameters = decodeParameters(arrayField(raw, "parameters"))
	value.RequestBody = decodeRequestBodyPointer(objectField(raw, "requestBody"))
	value.Responses = decodeNamedResponses(objectMap(objectField(raw, "responses")))
	value.Servers = decodeServers(arrayField(raw, "servers"))
	return value
}

func decodeParameters(rawParameters []json.RawMessage) []models.OpenAPIParameter {
	parameters := make([]models.OpenAPIParameter, 0, len(rawParameters))
	for _, raw := range rawParameters {
		parameters = append(parameters, decodeParameter(raw))
	}
	return parameters
}

func decodeParameter(raw json.RawMessage) models.OpenAPIParameter {
	var value models.OpenAPIParameter
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.Content = decodeMediaTypes(objectMap(objectField(raw, "content")))
	return value
}

func decodeRequestBodyPointer(raw json.RawMessage) *models.OpenAPIRequestBody {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	value := decodeRequestBody(raw)
	return &value
}

func decodeRequestBody(raw json.RawMessage) models.OpenAPIRequestBody {
	var value models.OpenAPIRequestBody
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.Content = decodeMediaTypes(objectMap(objectField(raw, "content")))
	return value
}

func decodeResponse(raw json.RawMessage) models.OpenAPIResponse {
	var value models.OpenAPIResponse
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.Headers = decodeNamedHeaders(objectMap(objectField(raw, "headers")))
	value.Content = decodeMediaTypes(objectMap(objectField(raw, "content")))
	return value
}

func decodeHeader(raw json.RawMessage) models.OpenAPIHeader {
	var value models.OpenAPIHeader
	_ = json.Unmarshal(raw, &value)
	value.Extensions = extractExtensions(raw)
	value.Content = decodeMediaTypes(objectMap(objectField(raw, "content")))
	return value
}

func decodeMediaTypes(source map[string]json.RawMessage) map[string]models.OpenAPIMediaType {
	if source == nil {
		return nil
	}
	values := make(map[string]models.OpenAPIMediaType, len(source))
	for name, raw := range source {
		var value models.OpenAPIMediaType
		_ = json.Unmarshal(raw, &value)
		value.Extensions = extractExtensions(raw)
		values[name] = value
	}
	return values
}

func decodeNamedResponses(source map[string]json.RawMessage) map[string]models.OpenAPIResponse {
	if source == nil {
		return nil
	}
	values := make(map[string]models.OpenAPIResponse, len(source))
	for name, raw := range source {
		values[name] = decodeResponse(raw)
	}
	return values
}

func decodeNamedParameters(source map[string]json.RawMessage) map[string]models.OpenAPIParameter {
	if source == nil {
		return nil
	}
	values := make(map[string]models.OpenAPIParameter, len(source))
	for name, raw := range source {
		values[name] = decodeParameter(raw)
	}
	return values
}

func decodeNamedRequestBodies(source map[string]json.RawMessage) map[string]models.OpenAPIRequestBody {
	if source == nil {
		return nil
	}
	values := make(map[string]models.OpenAPIRequestBody, len(source))
	for name, raw := range source {
		values[name] = decodeRequestBody(raw)
	}
	return values
}

func decodeNamedHeaders(source map[string]json.RawMessage) map[string]models.OpenAPIHeader {
	if source == nil {
		return nil
	}
	values := make(map[string]models.OpenAPIHeader, len(source))
	for name, raw := range source {
		values[name] = decodeHeader(raw)
	}
	return values
}

func decodeNamedPathItems(source map[string]json.RawMessage) map[string]models.OpenAPIPathItem {
	if source == nil {
		return nil
	}
	values := make(map[string]models.OpenAPIPathItem, len(source))
	for name, raw := range source {
		values[name] = decodePathItem(raw)
	}
	return values
}

func extractExtensions(raw json.RawMessage) map[string]json.RawMessage {
	object := objectMap(raw)
	var extensions map[string]json.RawMessage
	for key, value := range object {
		if strings.HasPrefix(strings.ToLower(key), "x-") {
			if extensions == nil {
				extensions = make(map[string]json.RawMessage)
			}
			extensions[key] = append(json.RawMessage(nil), value...)
		}
	}
	return extensions
}

func objectField(raw json.RawMessage, name string) json.RawMessage {
	return objectMap(raw)[name]
}

func arrayField(raw json.RawMessage, name string) []json.RawMessage {
	var values []json.RawMessage
	_ = json.Unmarshal(objectField(raw, name), &values)
	return values
}

func objectMap(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	return object
}
