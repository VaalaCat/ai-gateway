package apiopenapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
)

const (
	MaxDocumentBytes  = 10 << 20
	maxReferenceDepth = 256
	maxReferenceNodes = 100_000
)

var openAPIVersionPattern = regexp.MustCompile(`^3\.(0|1)\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)

var pathItemFields = map[string]struct{}{
	"$ref": {}, "description": {}, "parameters": {}, "servers": {}, "summary": {},
}

// Problem is one safe, user-facing document problem located by JSON path.
type Problem struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ParseError reports all problems found while accepting an OpenAPI document.
type ParseError struct {
	Problems []Problem `json:"problems"`
}

func (e *ParseError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "OpenAPI document is invalid"
	}
	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, fmt.Sprintf("%s at %s: %s", problem.Code, problem.Path, problem.Message))
	}
	return strings.Join(parts, "; ")
}

// ParsedDocument contains only platform-owned OpenAPI facts. The title and
// description remain separate because APIService owns them after import.
type ParsedDocument struct {
	Name        string
	Description string
	Document    models.OpenAPIServiceDocument
	Paths       map[string]models.OpenAPIPathItem
}

// ParseJSON validates an OpenAPI 3.0/3.1 JSON document and converts it at the
// parser boundary into platform model types.
func ParseJSON(raw []byte) (ParsedDocument, error) {
	if len(raw) > MaxDocumentBytes {
		return ParsedDocument{}, problemError("$", "document_too_large", "document exceeds the maximum supported size")
	}
	if !json.Valid(raw) {
		return ParsedDocument{}, problemError("$", "invalid_json", "document must be valid JSON")
	}

	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ParsedDocument{}, problemError("$", "invalid_json", "document must be valid JSON")
	}
	rootObject, ok := root.(map[string]any)
	if !ok {
		return ParsedDocument{}, problemError("$", "invalid_document", "document root must be an object")
	}
	version, _ := rootObject["openapi"].(string)
	if !openAPIVersionPattern.MatchString(version) {
		return ParsedDocument{}, problemError("$.openapi", "unsupported_version", "only OpenAPI 3.0 and 3.1 are supported")
	}
	if _, exists := rootObject["webhooks"]; exists {
		return ParsedDocument{}, problemError("$.webhooks", "unsupported_feature", "webhooks are not supported")
	}
	if !hasRequiredInfo(rootObject) {
		return ParsedDocument{}, problemError("$", "invalid_document", "document failed OpenAPI validation")
	}
	if problem := validatePathObjects(rootObject); problem != nil {
		return ParsedDocument{}, &ParseError{Problems: []Problem{*problem}}
	}
	if problem := validateReferences(root); problem != nil {
		return ParsedDocument{}, &ParseError{Problems: []Problem{*problem}}
	}
	validationRaw, err := buildLibOpenAPIValidationJSON(root)
	if err != nil {
		return ParsedDocument{}, problemError("$", "invalid_document", "document failed OpenAPI validation")
	}
	if err := validateWithLibOpenAPI(validationRaw); err != nil {
		return ParsedDocument{}, problemError("$", "invalid_document", "document failed OpenAPI validation")
	}

	return decodeParsedDocument(raw)
}

func hasRequiredInfo(root map[string]any) bool {
	info, ok := root["info"].(map[string]any)
	if !ok {
		return false
	}
	title, titleOK := info["title"].(string)
	version, versionOK := info["version"].(string)
	return titleOK && strings.TrimSpace(title) != "" && versionOK && strings.TrimSpace(version) != ""
}

func validatePathObjects(root map[string]any) *Problem {
	if problem := validatePathItemMap(root["paths"], "$.paths", true); problem != nil {
		return problem
	}
	components, _ := root["components"].(map[string]any)
	if components == nil {
		return nil
	}
	return validatePathItemMap(components["pathItems"], "$.components.pathItems", false)
}

func validatePathItemMap(value any, location string, requireLeadingSlash bool) *Problem {
	if value == nil {
		return nil
	}
	paths, ok := value.(map[string]any)
	if !ok {
		return &Problem{Path: location, Code: "invalid_path", Message: "path items must be an object"}
	}
	pathNames := make([]string, 0, len(paths))
	for path := range paths {
		pathNames = append(pathNames, path)
	}
	sort.Strings(pathNames)
	for _, path := range pathNames {
		pathLocation := appendJSONPath(location, path)
		if requireLeadingSlash && !strings.HasPrefix(path, "/") {
			return &Problem{Path: pathLocation, Code: "invalid_path", Message: "OpenAPI paths must start with /"}
		}
		item, ok := paths[path].(map[string]any)
		if !ok {
			return &Problem{Path: pathLocation, Code: "invalid_path", Message: "path item must be an object"}
		}
		fields := make([]string, 0, len(item))
		for field := range item {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			if strings.HasPrefix(strings.ToLower(field), "x-") {
				continue
			}
			if _, known := pathItemFields[field]; known {
				continue
			}
			if _, lower, method := normalizeOperationMethod(field); !method || field != lower {
				return &Problem{Path: appendJSONPath(pathLocation, field), Code: "invalid_method", Message: "path item contains an unsupported operation method"}
			}
		}
	}
	return nil
}

func normalizeOperationMethod(method string) (upper, lower string, ok bool) {
	lower = strings.ToLower(method)
	upper, ok = models.NormalizeStandardHTTPMethod(method)
	return upper, lower, ok
}

func validateWithLibOpenAPI(raw []byte) error {
	configuration := datamodel.NewDocumentConfiguration()
	configuration.AllowFileReferences = false
	configuration.AllowRemoteReferences = false
	document, err := libopenapi.NewDocumentWithConfiguration(raw, configuration)
	if err != nil {
		return fmt.Errorf("OpenAPI structure is invalid: %w", err)
	}
	defer document.Release()
	_, err = document.BuildV3Model()
	if err != nil {
		return fmt.Errorf("OpenAPI model is invalid: %w", err)
	}
	return nil
}

func problemError(path, code, message string) error {
	return &ParseError{Problems: []Problem{{Path: path, Code: code, Message: message}}}
}

func appendJSONPath(path, key string) string {
	if isSimpleJSONPathKey(key) || strings.HasPrefix(key, "$") && isSimpleJSONPathKey(strings.TrimPrefix(key, "$")) {
		return path + "." + key
	}
	encoded, _ := json.Marshal(key)
	return path + "[" + string(encoded) + "]"
}

func isSimpleJSONPathKey(key string) bool {
	if key == "" || !((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= 'a' && key[0] <= 'z') || key[0] == '_') {
		return false
	}
	for index := 1; index < len(key); index++ {
		char := key[index]
		if !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}
