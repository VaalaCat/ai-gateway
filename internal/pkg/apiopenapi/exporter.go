package apiopenapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const (
	defaultOpenAPIVersion = "3.1.0"
	defaultInfoVersion    = "1.0.0"
)

// BuildDocument rebuilds a standard OpenAPI document from current platform
// facts. Runtime upstreams and credentials are deliberately outside its input.
func BuildDocument(service models.APIService, routes []models.APIRoute, gatewayServer string) ([]byte, error) {
	if strings.TrimSpace(service.Name) == "" {
		return nil, fmt.Errorf("OpenAPI service name must not be empty")
	}
	if err := validateGatewayServer(gatewayServer); err != nil {
		return nil, err
	}

	platform := service.OpenAPIDocument.Data()
	version := platform.Version
	if version == "" {
		version = defaultOpenAPIVersion
	}
	if !openAPIVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("stored OpenAPI version %q is not supported", version)
	}
	info, err := encodeInfo(platform.Info, service.Name, service.Description)
	if err != nil {
		return nil, err
	}
	paths, publicPaths, err := encodeRoutePaths(routes)
	if err != nil {
		return nil, err
	}
	document, err := encodeServiceDocument(platform)
	if err != nil {
		return nil, err
	}
	if err := rewriteComponentPathReferences(document, publicPaths); err != nil {
		return nil, err
	}
	document["openapi"] = version
	document["info"] = info
	document["servers"] = []map[string]any{{"url": gatewayServer}}
	document["paths"] = paths

	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal exported OpenAPI document: %w", err)
	}
	if err := validateGeneratedDocument(raw); err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func validateGatewayServer(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, "{}\r\n") {
		return fmt.Errorf("gateway server must be an absolute http/https URL without credentials, query, fragment, or templates")
	}
	return nil
}

func validateGeneratedDocument(raw []byte) error {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("generated OpenAPI JSON is invalid: %w", err)
	}
	if problem := validateReferences(root); problem != nil {
		return fmt.Errorf("generated OpenAPI document is invalid: %s at %s", problem.Code, problem.Path)
	}
	validationRaw, err := buildLibOpenAPIValidationJSON(root)
	if err != nil {
		return fmt.Errorf("generated OpenAPI validation document is invalid: %w", err)
	}
	if err := validateWithLibOpenAPI(validationRaw); err != nil {
		return fmt.Errorf("generated OpenAPI document failed validation: %w", err)
	}
	return nil
}

func encodeServiceDocument(document models.OpenAPIServiceDocument) (map[string]any, error) {
	encoded, err := marshalObject(document)
	if err != nil {
		return nil, fmt.Errorf("encode service OpenAPI document: %w", err)
	}
	delete(encoded, "openapi")
	delete(encoded, "info")
	delete(encoded, "servers")
	delete(encoded, "paths")
	flattenExtensions(encoded, document.Extensions)

	if len(document.Tags) > 0 {
		tags := make([]map[string]any, 0, len(document.Tags))
		for _, tag := range document.Tags {
			value, encodeErr := encodeTag(tag)
			if encodeErr != nil {
				return nil, encodeErr
			}
			tags = append(tags, value)
		}
		encoded["tags"] = tags
	}
	if document.ExternalDocs != nil {
		value, encodeErr := encodeExternalDocs(*document.ExternalDocs)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["externalDocs"] = value
	}
	components, err := encodeComponents(document.Components)
	if err != nil {
		return nil, err
	}
	if len(components) > 0 {
		encoded["components"] = components
	} else {
		delete(encoded, "components")
	}
	return encoded, nil
}

func encodeInfo(info models.OpenAPIInfo, title, description string) (map[string]any, error) {
	encoded, err := marshalObject(info)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI info: %w", err)
	}
	flattenExtensions(encoded, info.Extensions)
	encoded["title"] = title
	if description != "" {
		encoded["description"] = description
	} else {
		delete(encoded, "description")
	}
	if info.Version == "" {
		encoded["version"] = defaultInfoVersion
	}
	if info.Contact != nil {
		value, encodeErr := encodeContact(*info.Contact)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["contact"] = value
	}
	if info.License != nil {
		value, encodeErr := encodeLicense(*info.License)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["license"] = value
	}
	return encoded, nil
}

func encodeContact(contact models.OpenAPIContact) (map[string]any, error) {
	return marshalAndFlattenExtensions(contact, contact.Extensions)
}

func encodeLicense(license models.OpenAPILicense) (map[string]any, error) {
	return marshalAndFlattenExtensions(license, license.Extensions)
}

func encodeTag(tag models.OpenAPITag) (map[string]any, error) {
	encoded, err := marshalObject(tag)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI tag: %w", err)
	}
	flattenExtensions(encoded, tag.Extensions)
	if tag.ExternalDocs != nil {
		value, encodeErr := encodeExternalDocs(*tag.ExternalDocs)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["externalDocs"] = value
	}
	return encoded, nil
}

func encodeExternalDocs(external models.OpenAPIExternalDocs) (map[string]any, error) {
	return marshalAndFlattenExtensions(external, external.Extensions)
}

func encodeComponents(components models.OpenAPIComponents) (map[string]any, error) {
	encoded, err := marshalObject(components)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI components: %w", err)
	}
	flattenExtensions(encoded, components.Extensions)
	if err := replaceNamed(encoded, "responses", components.Responses, encodeResponse); err != nil {
		return nil, err
	}
	if err := replaceNamed(encoded, "parameters", components.Parameters, encodeParameter); err != nil {
		return nil, err
	}
	if err := replaceNamed(encoded, "requestBodies", components.RequestBodies, encodeRequestBody); err != nil {
		return nil, err
	}
	if err := replaceNamed(encoded, "headers", components.Headers, encodeHeader); err != nil {
		return nil, err
	}
	if err := replaceNamed(encoded, "pathItems", components.PathItems, encodePathItem); err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeRoutePaths(routes []models.APIRoute) (map[string]any, map[string]string, error) {
	type routePath struct {
		route      models.APIRoute
		storedPath string
		publicPath string
	}
	var routePaths []routePath
	publicPaths := make(map[string]string)
	seenPublicPaths := make(map[string]struct{})
	for _, route := range routes {
		for _, storedPath := range sortedPathNames(route.OpenAPIPaths.Data()) {
			publicPath, err := exportedPath(route, storedPath)
			if err != nil {
				return nil, nil, err
			}
			if _, duplicate := seenPublicPaths[publicPath]; duplicate {
				return nil, nil, fmt.Errorf("duplicate exported OpenAPI path %q", publicPath)
			}
			if previous, duplicate := publicPaths[storedPath]; duplicate && previous != publicPath {
				return nil, nil, fmt.Errorf("stored OpenAPI path %q maps to multiple public paths", storedPath)
			}
			seenPublicPaths[publicPath] = struct{}{}
			publicPaths[storedPath] = publicPath
			routePaths = append(routePaths, routePath{route: route, storedPath: storedPath, publicPath: publicPath})
		}
	}

	paths := make(map[string]any, len(routePaths))
	for _, routePath := range routePaths {
		item, err := encodePathItem(routePath.route.OpenAPIPaths.Data()[routePath.storedPath])
		if err != nil {
			return nil, nil, fmt.Errorf("encode OpenAPI path %q: %w", routePath.storedPath, err)
		}
		if err := rewritePathReferences(item, semanticPathItem, publicPaths); err != nil {
			return nil, nil, fmt.Errorf("rewrite OpenAPI path %q references: %w", routePath.storedPath, err)
		}
		item[routeSlugExtension] = routePath.route.Slug
		paths[routePath.publicPath] = item
	}
	return paths, publicPaths, nil
}

func rewriteComponentPathReferences(document map[string]any, publicPaths map[string]string) error {
	components, _ := document["components"].(map[string]any)
	componentKinds := map[string]semanticKind{
		"schemas": semanticSchema, "responses": semanticResponse, "parameters": semanticParameter,
		"examples": semanticExample, "requestBodies": semanticRequestBody, "headers": semanticHeader,
		"securitySchemes": semanticSecurityScheme, "links": semanticLink, "callbacks": semanticCallback,
		"pathItems": semanticPathItem,
	}
	for _, collection := range sortedAnyKeys(components) {
		kind, supported := componentKinds[collection]
		if !supported {
			continue
		}
		if err := rewriteNamedReferences(components[collection], kind, publicPaths); err != nil {
			return fmt.Errorf("rewrite OpenAPI component %s references: %w", collection, err)
		}
	}
	return nil
}

func rewritePathReferences(value any, kind semanticKind, publicPaths map[string]string) error {
	return rewritePathReferencesWithSchemaResource(value, kind, publicPaths, true)
}

func rewritePathReferencesWithSchemaResource(value any, kind semanticKind, publicPaths map[string]string, documentResource bool) error {
	object, _ := value.(map[string]any)
	if object == nil {
		return nil
	}
	if kind == semanticSchema {
		if id, ok := object["$id"].(string); ok && id != "" {
			documentResource = false
		}
	}
	keywords := []string{"$ref"}
	if kind == semanticSchema {
		keywords = append(keywords, "$dynamicRef")
	}
	for _, keyword := range keywords {
		raw, exists := object[keyword]
		if !exists || (keyword == "$ref" && !kindSupportsReference(kind)) || (kind == semanticSchema && !documentResource) {
			continue
		}
		ref, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", keyword)
		}
		rewritten, err := rewriteLocalPathReference(ref, publicPaths)
		if err != nil {
			return err
		}
		object[keyword] = rewritten
	}
	if kind == semanticLink {
		if raw, exists := object["operationRef"]; exists {
			operationRef, ok := raw.(string)
			if !ok {
				return fmt.Errorf("operationRef must be a string")
			}
			if !strings.HasPrefix(operationRef, "#") {
				return fmt.Errorf("external operationRef is not supported")
			}
			rewritten, err := rewriteLocalPathReference(operationRef, publicPaths)
			if err != nil {
				return err
			}
			object["operationRef"] = rewritten
		}
	}

	switch kind {
	case semanticSchema:
		for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
			if err := rewriteSchemaNamedReferences(object[keyword], publicPaths, documentResource); err != nil {
				return err
			}
		}
		for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
			if err := rewriteSchemaSliceReferences(object[keyword], publicPaths, documentResource); err != nil {
				return err
			}
		}
		for _, keyword := range []string{"items", "contains", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "propertyNames", "if", "then", "else", "not", "contentSchema"} {
			if err := rewritePathReferencesWithSchemaResource(object[keyword], semanticSchema, publicPaths, documentResource); err != nil {
				return err
			}
		}
	case semanticPathItem:
		if err := rewriteSliceReferences(object["parameters"], semanticParameter, publicPaths); err != nil {
			return err
		}
		for _, field := range sortedAnyKeys(object) {
			if _, _, ok := normalizeOperationMethod(field); ok {
				if err := rewritePathReferences(object[field], semanticOperation, publicPaths); err != nil {
					return err
				}
			}
		}
	case semanticOperation:
		if err := rewriteSliceReferences(object["parameters"], semanticParameter, publicPaths); err != nil {
			return err
		}
		if err := rewritePathReferences(object["requestBody"], semanticRequestBody, publicPaths); err != nil {
			return err
		}
		if err := rewriteNamedReferences(object["responses"], semanticResponse, publicPaths); err != nil {
			return err
		}
		if err := rewriteNamedReferences(object["callbacks"], semanticCallback, publicPaths); err != nil {
			return err
		}
	case semanticParameter, semanticHeader:
		if err := rewritePathReferences(object["schema"], semanticSchema, publicPaths); err != nil {
			return err
		}
		if err := rewriteNamedReferences(object["examples"], semanticExample, publicPaths); err != nil {
			return err
		}
		if err := rewriteNamedReferences(object["content"], semanticMediaType, publicPaths); err != nil {
			return err
		}
	case semanticRequestBody:
		return rewriteNamedReferences(object["content"], semanticMediaType, publicPaths)
	case semanticResponse:
		if err := rewriteNamedReferences(object["headers"], semanticHeader, publicPaths); err != nil {
			return err
		}
		if err := rewriteNamedReferences(object["content"], semanticMediaType, publicPaths); err != nil {
			return err
		}
		return rewriteNamedReferences(object["links"], semanticLink, publicPaths)
	case semanticMediaType:
		if err := rewritePathReferences(object["schema"], semanticSchema, publicPaths); err != nil {
			return err
		}
		if err := rewriteNamedReferences(object["examples"], semanticExample, publicPaths); err != nil {
			return err
		}
		return rewriteEncodingHeaderReferences(object["encoding"], publicPaths)
	case semanticCallback:
		for _, expression := range sortedAnyKeys(object) {
			if expression == "$ref" || strings.HasPrefix(strings.ToLower(expression), "x-") {
				continue
			}
			if err := rewritePathReferences(object[expression], semanticPathItem, publicPaths); err != nil {
				return err
			}
		}
	}
	return nil
}

func rewriteSchemaNamedReferences(value any, publicPaths map[string]string, documentResource bool) error {
	object, _ := value.(map[string]any)
	for _, name := range sortedAnyKeys(object) {
		if err := rewritePathReferencesWithSchemaResource(object[name], semanticSchema, publicPaths, documentResource); err != nil {
			return err
		}
	}
	return nil
}

func rewriteSchemaSliceReferences(value any, publicPaths map[string]string, documentResource bool) error {
	values, _ := value.([]any)
	for _, item := range values {
		if err := rewritePathReferencesWithSchemaResource(item, semanticSchema, publicPaths, documentResource); err != nil {
			return err
		}
	}
	return nil
}

func rewriteEncodingHeaderReferences(value any, publicPaths map[string]string) error {
	encodings, _ := value.(map[string]any)
	for _, name := range sortedAnyKeys(encodings) {
		encoding, _ := encodings[name].(map[string]any)
		if err := rewriteNamedReferences(encoding["headers"], semanticHeader, publicPaths); err != nil {
			return err
		}
	}
	return nil
}

func rewriteNamedReferences(value any, kind semanticKind, publicPaths map[string]string) error {
	object, _ := value.(map[string]any)
	for _, name := range sortedAnyKeys(object) {
		if err := rewritePathReferences(object[name], kind, publicPaths); err != nil {
			return err
		}
	}
	return nil
}

func rewriteSliceReferences(value any, kind semanticKind, publicPaths map[string]string) error {
	values, _ := value.([]any)
	for _, item := range values {
		if err := rewritePathReferences(item, kind, publicPaths); err != nil {
			return err
		}
	}
	return nil
}

func rewriteLocalPathReference(reference string, publicPaths map[string]string) (string, error) {
	if !strings.HasPrefix(reference, "#") {
		return reference, nil
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(reference, "#"))
	if err != nil || !strings.HasPrefix(fragment, "/") {
		return reference, nil
	}
	encoded := strings.Split(strings.TrimPrefix(fragment, "/"), "/")
	segments := make([]string, 0, len(encoded))
	for _, value := range encoded {
		segment, ok := decodeJSONPointerSegment(value)
		if !ok {
			return "", fmt.Errorf("reference %q contains an invalid JSON pointer", reference)
		}
		segments = append(segments, segment)
	}
	if len(segments) < 2 || segments[0] != "paths" {
		return reference, nil
	}
	publicPath, exists := publicPaths[segments[1]]
	if !exists {
		return reference, nil
	}
	segments[1] = publicPath
	for index := range segments {
		segments[index] = encodeJSONPointerSegment(segments[index])
	}
	return "#/" + strings.Join(segments, "/"), nil
}

func exportedPath(route models.APIRoute, storedPath string) (string, error) {
	if !strings.HasPrefix(storedPath, "/") {
		return "", fmt.Errorf("stored OpenAPI path %q must start with /", storedPath)
	}
	if route.Slug == "" {
		return storedPath, nil
	}
	publicPrefix := "/" + route.Slug
	upstreamPrefix := strings.TrimSuffix(route.UpstreamPath, "/")
	if upstreamPrefix == "" {
		if storedPath == publicPrefix || strings.HasPrefix(storedPath, publicPrefix+"/") {
			return storedPath, nil
		}
		return publicPrefix + storedPath, nil
	}
	if storedPath == upstreamPrefix {
		return publicPrefix, nil
	}
	if !strings.HasPrefix(storedPath, upstreamPrefix+"/") {
		return "", fmt.Errorf("OpenAPI path %q is outside route upstream prefix %q", storedPath, route.UpstreamPath)
	}
	return publicPrefix + strings.TrimPrefix(storedPath, upstreamPrefix), nil
}

func encodePathItem(item models.OpenAPIPathItem) (map[string]any, error) {
	encoded, err := marshalObject(item)
	if err != nil {
		return nil, err
	}
	delete(encoded, "servers")
	delete(encoded, "operations")
	flattenExtensions(encoded, item.Extensions)
	if len(item.Parameters) > 0 {
		parameters, encodeErr := encodeSlice(item.Parameters, encodeParameter)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["parameters"] = parameters
	}
	methods := make([]string, 0, len(item.Operations))
	for method := range item.Operations {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		_, lower, supported := normalizeOperationMethod(method)
		if !supported {
			return nil, fmt.Errorf("unsupported OpenAPI operation method %q", method)
		}
		if _, duplicate := seen[lower]; duplicate {
			return nil, fmt.Errorf("duplicate OpenAPI operation method %q", method)
		}
		seen[lower] = struct{}{}
		operation, encodeErr := encodeOperation(item.Operations[method])
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded[lower] = operation
	}
	return encoded, nil
}

func encodeOperation(operation models.OpenAPIOperation) (map[string]any, error) {
	encoded, err := marshalObject(operation)
	if err != nil {
		return nil, err
	}
	delete(encoded, "servers")
	flattenExtensions(encoded, operation.Extensions)
	if operation.ExternalDocs != nil {
		value, encodeErr := encodeExternalDocs(*operation.ExternalDocs)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["externalDocs"] = value
	}
	if len(operation.Parameters) > 0 {
		parameters, encodeErr := encodeSlice(operation.Parameters, encodeParameter)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["parameters"] = parameters
	}
	if operation.RequestBody != nil {
		requestBody, encodeErr := encodeRequestBody(*operation.RequestBody)
		if encodeErr != nil {
			return nil, encodeErr
		}
		encoded["requestBody"] = requestBody
	}
	if err := replaceNamed(encoded, "responses", operation.Responses, encodeResponse); err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeParameter(parameter models.OpenAPIParameter) (map[string]any, error) {
	encoded, err := marshalObject(parameter)
	if err != nil {
		return nil, err
	}
	flattenExtensions(encoded, parameter.Extensions)
	if err := replaceNamed(encoded, "content", parameter.Content, encodeMediaType); err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeRequestBody(body models.OpenAPIRequestBody) (map[string]any, error) {
	encoded, err := marshalObject(body)
	if err != nil {
		return nil, err
	}
	flattenExtensions(encoded, body.Extensions)
	if err := replaceNamed(encoded, "content", body.Content, encodeMediaType); err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeResponse(response models.OpenAPIResponse) (map[string]any, error) {
	encoded, err := marshalObject(response)
	if err != nil {
		return nil, err
	}
	flattenExtensions(encoded, response.Extensions)
	if err := replaceNamed(encoded, "headers", response.Headers, encodeHeader); err != nil {
		return nil, err
	}
	if err := replaceNamed(encoded, "content", response.Content, encodeMediaType); err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeHeader(header models.OpenAPIHeader) (map[string]any, error) {
	encoded, err := marshalObject(header)
	if err != nil {
		return nil, err
	}
	flattenExtensions(encoded, header.Extensions)
	if err := replaceNamed(encoded, "content", header.Content, encodeMediaType); err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeMediaType(media models.OpenAPIMediaType) (map[string]any, error) {
	return marshalAndFlattenExtensions(media, media.Extensions)
}

func marshalAndFlattenExtensions(value any, extensions map[string]json.RawMessage) (map[string]any, error) {
	encoded, err := marshalObject(value)
	if err != nil {
		return nil, err
	}
	flattenExtensions(encoded, extensions)
	return encoded, nil
}

func replaceNamed[T any](object map[string]any, key string, values map[string]T, encode func(T) (map[string]any, error)) error {
	if len(values) == 0 {
		delete(object, key)
		return nil
	}
	encoded := make(map[string]any, len(values))
	for name, value := range values {
		item, err := encode(value)
		if err != nil {
			return fmt.Errorf("encode OpenAPI %s %q: %w", key, name, err)
		}
		encoded[name] = item
	}
	object[key] = encoded
	return nil
}

func encodeSlice[T any](values []T, encode func(T) (map[string]any, error)) ([]map[string]any, error) {
	encoded := make([]map[string]any, 0, len(values))
	for _, value := range values {
		item, err := encode(value)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, item)
	}
	return encoded, nil
}

func marshalObject(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	return object, nil
}

func flattenExtensions(object map[string]any, extensions map[string]json.RawMessage) {
	delete(object, "extensions")
	for key, raw := range extensions {
		if strings.HasPrefix(strings.ToLower(key), "x-") {
			object[key] = raw
		}
	}
}
