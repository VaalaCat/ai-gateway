package apiopenapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const schemaDocumentResource = "$document"

type referenceRecord struct {
	path     string
	ref      string
	keyword  string
	kind     semanticKind
	resource string
}

type componentReferenceRecord struct {
	path           string
	collection     string
	name           string
	localReference *referenceRecord
}

func validateReferences(root any) *Problem {
	validator := newReferenceValidator(root)
	validator.collectDocument(root)
	if validator.problem != nil {
		return validator.problem
	}
	for _, reference := range validator.references {
		if _, problem := validator.resolve(reference); problem != nil {
			return problem
		}
	}
	for _, reference := range validator.references {
		if problem := validator.visit(reference, 0); problem != nil {
			return problem
		}
	}
	return nil
}

func buildLibOpenAPIValidationJSON(root any) ([]byte, error) {
	validator := newReferenceValidator(root)
	validator.collectDocument(root)
	if validator.problem != nil {
		return nil, &ParseError{Problems: []Problem{*validator.problem}}
	}
	semanticReferences := make(map[string]referenceRecord, len(validator.references))
	for _, reference := range validator.references {
		semanticReferences[reference.path] = reference
	}
	pointers := make(map[string]string)
	collectJSONPointers(root, "$", "#", pointers)
	aliases := make(map[string]string)
	rootObject, _ := root.(map[string]any)
	if err := normalizeReferencesForLib(root, "$", validator, semanticReferences, pointers, rootObject, aliases); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

func collectJSONPointers(value any, path, pointer string, destination map[string]string) {
	destination[path] = pointer
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedAnyKeys(typed) {
			collectJSONPointers(typed[key], appendJSONPath(path, key), pointer+"/"+encodeJSONPointerSegment(key), destination)
		}
	case []any:
		for index, child := range typed {
			collectJSONPointers(child, fmt.Sprintf("%s[%d]", path, index), fmt.Sprintf("%s/%d", pointer, index), destination)
		}
	}
}

func normalizeReferencesForLib(value any, path string, validator *referenceValidator, semantic map[string]referenceRecord, pointers map[string]string, root map[string]any, aliases map[string]string) error {
	switch typed := value.(type) {
	case map[string]any:
		for _, keyword := range []string{"$ref", "$dynamicRef"} {
			if _, exists := typed[keyword]; !exists {
				continue
			}
			referencePath := appendJSONPath(path, keyword)
			reference, meaningful := semantic[referencePath]
			if !meaningful {
				delete(typed, keyword)
			} else {
				target, problem := validator.resolve(reference)
				if problem != nil {
					return &ParseError{Problems: []Problem{*problem}}
				}
				pointer, exists := pointers[target.path]
				if !exists {
					return fmt.Errorf("resolved reference target has no JSON pointer")
				}
				pointer = validationReferencePointer(root, reference.kind, target, pointer, aliases)
				typed[keyword] = pointer
			}
		}
		for _, key := range sortedAnyKeys(typed) {
			if err := normalizeReferencesForLib(typed[key], appendJSONPath(path, key), validator, semantic, pointers, root, aliases); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := normalizeReferencesForLib(child, fmt.Sprintf("%s[%d]", path, index), validator, semantic, pointers, root, aliases); err != nil {
				return err
			}
		}
	}
	return nil
}

func validationReferencePointer(root map[string]any, kind semanticKind, target referenceTarget, pointer string, aliases map[string]string) string {
	if libSupportsReferencePointer(kind, pointer) {
		return pointer
	}
	identity := fmt.Sprintf("%d:%s", kind, target.path)
	if alias, exists := aliases[identity]; exists {
		return alias
	}
	componentName := fmt.Sprintf("x-ai-gateway-validation-%d", len(aliases))
	collection := referenceComponentCollection(kind)
	if collection == "" {
		return pointer
	}
	components, _ := root["components"].(map[string]any)
	if components == nil {
		components = make(map[string]any)
		root["components"] = components
	}
	items, _ := components[collection].(map[string]any)
	if items == nil {
		items = make(map[string]any)
		components[collection] = items
	}
	items[componentName] = target.value
	alias := "#/components/" + collection + "/" + componentName
	aliases[identity] = alias
	return alias
}

func libSupportsReferencePointer(kind semanticKind, pointer string) bool {
	segments := strings.Split(strings.TrimPrefix(pointer, "#/"), "/")
	if kind == semanticPathItem && len(segments) == 2 && segments[0] == "paths" {
		return true
	}
	return len(segments) == 3 && segments[0] == "components" && segments[1] == referenceComponentCollection(kind)
}

func referenceComponentCollection(kind semanticKind) string {
	return map[semanticKind]string{
		semanticSchema: "schemas", semanticPathItem: "pathItems", semanticParameter: "parameters",
		semanticRequestBody: "requestBodies", semanticResponse: "responses", semanticHeader: "headers",
		semanticExample: "examples", semanticLink: "links", semanticCallback: "callbacks",
		semanticSecurityScheme: "securitySchemes",
	}[kind]
}

func encodeJSONPointerSegment(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

type semanticKind uint8

const (
	semanticSchema semanticKind = iota
	semanticPathItem
	semanticOperation
	semanticParameter
	semanticRequestBody
	semanticResponse
	semanticHeader
	semanticMediaType
	semanticExample
	semanticLink
	semanticCallback
	semanticSecurityScheme
)

type referenceTarget struct {
	value    any
	path     string
	resource string
}

type referenceValidator struct {
	root           any
	anchors        map[string]map[string]referenceTarget
	dynamicAnchors map[string]map[string]referenceTarget
	resources      map[string]referenceTarget
	resourceByPath map[string]string
	references     []referenceRecord
	componentRefs  []componentReferenceRecord
	visiting       map[string]struct{}
	heights        map[string]int
	nodes          int
	collectAnchors bool
	problem        *Problem
}

func newReferenceValidator(root any) *referenceValidator {
	return &referenceValidator{
		root:    root,
		anchors: make(map[string]map[string]referenceTarget), dynamicAnchors: make(map[string]map[string]referenceTarget),
		resources:      map[string]referenceTarget{schemaDocumentResource: {value: root, path: "$", resource: schemaDocumentResource}},
		resourceByPath: make(map[string]string), visiting: make(map[string]struct{}), heights: make(map[string]int),
		collectAnchors: true,
	}
}

func (validator *referenceValidator) collectDocument(root any) {
	object, _ := root.(map[string]any)
	validator.addSecurityRequirements(object["security"], "$.security")
	validator.walkNamed(object["paths"], "$.paths", semanticPathItem)
	components, _ := object["components"].(map[string]any)
	if components == nil {
		return
	}
	componentKinds := map[string]semanticKind{
		"schemas": semanticSchema, "responses": semanticResponse, "parameters": semanticParameter,
		"examples": semanticExample, "requestBodies": semanticRequestBody, "headers": semanticHeader,
		"securitySchemes": semanticSecurityScheme, "links": semanticLink, "callbacks": semanticCallback,
		"pathItems": semanticPathItem,
	}
	names := make([]string, 0, len(componentKinds))
	for name := range componentKinds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		validator.walkNamed(components[name], appendJSONPath("$.components", name), componentKinds[name])
	}
}

func (validator *referenceValidator) walkNamed(value any, path string, kind semanticKind) {
	object, _ := value.(map[string]any)
	for _, name := range sortedAnyKeys(object) {
		validator.walkWithResource(object[name], appendJSONPath(path, name), kind, schemaDocumentResource)
	}
}

func (validator *referenceValidator) walkSlice(value any, path string, kind semanticKind) {
	values, _ := value.([]any)
	for index, item := range values {
		validator.walkWithResource(item, fmt.Sprintf("%s[%d]", path, index), kind, schemaDocumentResource)
	}
}

func (validator *referenceValidator) walk(value any, path string, kind semanticKind) {
	validator.walkWithResource(value, path, kind, schemaDocumentResource)
}

func (validator *referenceValidator) walkWithResource(value any, path string, kind semanticKind, resource string) {
	if validator.problem != nil {
		return
	}
	object, _ := value.(map[string]any)
	if object == nil {
		return
	}
	if kind == semanticSchema {
		validator.walkSchema(object, path, resource)
		return
	}
	if raw, exists := object["$ref"]; exists && kindSupportsReference(kind) {
		validator.addReference(raw, appendJSONPath(path, "$ref"), "$ref", kind, schemaDocumentResource)
	}

	switch kind {
	case semanticPathItem:
		validator.walkSlice(object["parameters"], appendJSONPath(path, "parameters"), semanticParameter)
		for _, field := range sortedAnyKeys(object) {
			if _, _, ok := normalizeOperationMethod(field); ok {
				validator.walk(object[field], appendJSONPath(path, field), semanticOperation)
			}
		}
	case semanticOperation:
		validator.addSecurityRequirements(object["security"], appendJSONPath(path, "security"))
		validator.walkSlice(object["parameters"], appendJSONPath(path, "parameters"), semanticParameter)
		validator.walk(object["requestBody"], appendJSONPath(path, "requestBody"), semanticRequestBody)
		validator.walkNamed(object["responses"], appendJSONPath(path, "responses"), semanticResponse)
		validator.walkNamed(object["callbacks"], appendJSONPath(path, "callbacks"), semanticCallback)
	case semanticParameter, semanticHeader:
		validator.walk(object["schema"], appendJSONPath(path, "schema"), semanticSchema)
		validator.walkNamed(object["examples"], appendJSONPath(path, "examples"), semanticExample)
		validator.walkNamed(object["content"], appendJSONPath(path, "content"), semanticMediaType)
	case semanticRequestBody:
		validator.walkNamed(object["content"], appendJSONPath(path, "content"), semanticMediaType)
	case semanticResponse:
		validator.walkNamed(object["headers"], appendJSONPath(path, "headers"), semanticHeader)
		validator.walkNamed(object["content"], appendJSONPath(path, "content"), semanticMediaType)
		validator.walkNamed(object["links"], appendJSONPath(path, "links"), semanticLink)
	case semanticMediaType:
		validator.walk(object["schema"], appendJSONPath(path, "schema"), semanticSchema)
		validator.walkNamed(object["examples"], appendJSONPath(path, "examples"), semanticExample)
		validator.walkEncodings(object["encoding"], appendJSONPath(path, "encoding"))
	case semanticCallback:
		for _, expression := range sortedAnyKeys(object) {
			if expression == "$ref" || strings.HasPrefix(strings.ToLower(expression), "x-") {
				continue
			}
			validator.walk(object[expression], appendJSONPath(path, expression), semanticPathItem)
		}
	}
}

func (validator *referenceValidator) walkEncodings(value any, path string) {
	encodings, _ := value.(map[string]any)
	for _, name := range sortedAnyKeys(encodings) {
		encoding, _ := encodings[name].(map[string]any)
		validator.walkNamed(encoding["headers"], appendJSONPath(appendJSONPath(path, name), "headers"), semanticHeader)
	}
}

func (validator *referenceValidator) walkSchema(schema map[string]any, path, parentResource string) {
	resource := validator.schemaResource(schema, path, parentResource)
	if raw, exists := schema["$ref"]; exists {
		validator.addReference(raw, appendJSONPath(path, "$ref"), "$ref", semanticSchema, resource)
	}
	if raw, exists := schema["$dynamicRef"]; exists {
		validator.addReference(raw, appendJSONPath(path, "$dynamicRef"), "$dynamicRef", semanticSchema, resource)
	}
	if validator.collectAnchors {
		validator.addAnchor("$anchor", schema["$anchor"], path, schema, resource, false)
		validator.addAnchor("$dynamicAnchor", schema["$dynamicAnchor"], path, schema, resource, true)
	}
	validator.addDiscriminatorMappings(schema["discriminator"], appendJSONPath(path, "discriminator"), resource)
	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
		validator.walkSchemaNamed(schema[keyword], appendJSONPath(path, keyword), resource)
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		validator.walkSchemaSlice(schema[keyword], appendJSONPath(path, keyword), resource)
	}
	for _, keyword := range []string{
		"items", "contains", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "propertyNames",
		"if", "then", "else", "not", "contentSchema",
	} {
		validator.walkWithResource(schema[keyword], appendJSONPath(path, keyword), semanticSchema, resource)
	}
}

func (validator *referenceValidator) addSecurityRequirements(value any, path string) {
	requirements, _ := value.([]any)
	for index, requirement := range requirements {
		object, _ := requirement.(map[string]any)
		for _, name := range sortedAnyKeys(object) {
			validator.componentRefs = append(validator.componentRefs, componentReferenceRecord{
				path: fmt.Sprintf("%s[%d]", path, index), collection: "securitySchemes", name: name,
			})
		}
	}
}

func (validator *referenceValidator) addDiscriminatorMappings(value any, path, resource string) {
	discriminator, _ := value.(map[string]any)
	mapping, _ := discriminator["mapping"].(map[string]any)
	for _, key := range sortedAnyKeys(mapping) {
		target, _ := mapping[key].(string)
		mappingPath := appendJSONPath(appendJSONPath(path, "mapping"), key)
		if name, ok := discriminatorSchemaName(target); ok {
			validator.componentRefs = append(validator.componentRefs, componentReferenceRecord{
				path: mappingPath, collection: "schemas", name: name,
			})
			continue
		}
		if strings.HasPrefix(target, "#") {
			reference := referenceRecord{
				path: mappingPath, ref: target, keyword: "discriminator mapping", kind: semanticSchema, resource: resource,
			}
			validator.componentRefs = append(validator.componentRefs, componentReferenceRecord{
				path: mappingPath, localReference: &reference,
			})
		}
	}
}

func discriminatorSchemaName(target string) (string, bool) {
	return target, target != "" && !strings.ContainsAny(target, "#/:?")
}

func (validator *referenceValidator) schemaResource(schema map[string]any, path, parentResource string) string {
	if resource, exists := validator.resourceByPath[path]; exists {
		return resource
	}
	resource := parentResource
	if id, ok := schema["$id"].(string); ok && id != "" {
		resource = path
		validator.resources[resource] = referenceTarget{value: schema, path: path, resource: resource}
	}
	if validator.collectAnchors {
		validator.resourceByPath[path] = resource
	}
	return resource
}

func (validator *referenceValidator) walkSchemaNamed(value any, path, resource string) {
	object, _ := value.(map[string]any)
	for _, name := range sortedAnyKeys(object) {
		validator.walkWithResource(object[name], appendJSONPath(path, name), semanticSchema, resource)
	}
}

func (validator *referenceValidator) walkSchemaSlice(value any, path, resource string) {
	values, _ := value.([]any)
	for index, item := range values {
		validator.walkWithResource(item, fmt.Sprintf("%s[%d]", path, index), semanticSchema, resource)
	}
}

func (validator *referenceValidator) addAnchor(keyword string, raw any, path string, value any, resource string, dynamic bool) {
	if raw == nil {
		return
	}
	anchor, ok := raw.(string)
	if !ok || anchor == "" {
		return
	}
	anchors := validator.anchors[resource]
	if anchors == nil {
		anchors = make(map[string]referenceTarget)
		validator.anchors[resource] = anchors
	}
	dynamicAnchors := validator.dynamicAnchors[resource]
	if dynamicAnchors == nil {
		dynamicAnchors = make(map[string]referenceTarget)
		validator.dynamicAnchors[resource] = dynamicAnchors
	}
	if _, duplicate := anchors[anchor]; duplicate {
		validator.problem = &Problem{Path: appendJSONPath(path, keyword), Code: "invalid_reference", Message: "schema anchor must be unique within its resource"}
		return
	}
	if _, duplicate := dynamicAnchors[anchor]; duplicate {
		validator.problem = &Problem{Path: appendJSONPath(path, keyword), Code: "invalid_reference", Message: "schema anchor must be unique within its resource"}
		return
	}
	target := referenceTarget{value: value, path: path, resource: resource}
	if dynamic {
		dynamicAnchors[anchor] = target
	} else {
		anchors[anchor] = target
	}
}

func (validator *referenceValidator) addReference(raw any, path, keyword string, kind semanticKind, resource string) {
	ref, _ := raw.(string)
	validator.references = append(validator.references, referenceRecord{
		path: path, ref: ref, keyword: keyword, kind: kind, resource: resource,
	})
}

func kindSupportsReference(kind semanticKind) bool {
	return kind != semanticOperation && kind != semanticMediaType
}

func (validator *referenceValidator) resolve(reference referenceRecord) (referenceTarget, *Problem) {
	if reference.ref == "" {
		return referenceTarget{}, &Problem{Path: reference.path, Code: "invalid_reference", Message: reference.keyword + " must be a non-empty string"}
	}
	if !strings.HasPrefix(reference.ref, "#") {
		return referenceTarget{}, &Problem{Path: reference.path, Code: "external_reference", Message: "external references are not supported"}
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(reference.ref, "#"))
	if err != nil {
		return referenceTarget{}, &Problem{Path: reference.path, Code: "invalid_reference", Message: reference.keyword + " contains an invalid URI fragment"}
	}
	resource := validator.resources[schemaDocumentResource]
	if reference.kind == semanticSchema {
		if scoped, exists := validator.resources[reference.resource]; exists {
			resource = scoped
		}
	}
	if fragment == "" {
		return resource, nil
	}
	if !strings.HasPrefix(fragment, "/") {
		var target referenceTarget
		var exists bool
		if reference.keyword == "$dynamicRef" {
			target, exists = validator.dynamicAnchors[resource.resource][fragment]
		}
		if !exists {
			target, exists = validator.anchors[resource.resource][fragment]
		}
		if !exists {
			target, exists = validator.dynamicAnchors[resource.resource][fragment]
		}
		if !exists {
			return referenceTarget{}, &Problem{Path: reference.path, Code: "invalid_reference", Message: "local reference does not exist"}
		}
		return target, nil
	}
	target, ok := resolveJSONPointerFrom(resource.value, fragment, resource.path, resource.resource)
	if !ok {
		return referenceTarget{}, &Problem{Path: reference.path, Code: "invalid_reference", Message: "local reference does not exist"}
	}
	if scopedResource, exists := validator.resourceByPath[target.path]; exists {
		target.resource = scopedResource
	}
	return target, nil
}

func (validator *referenceValidator) visit(reference referenceRecord, depth int) *Problem {
	_, problem := validator.visitHeight(reference, depth)
	return problem
}

func (validator *referenceValidator) visitHeight(reference referenceRecord, depth int) (int, *Problem) {
	if depth >= maxReferenceDepth {
		return 0, &Problem{Path: "$", Code: "document_too_complex", Message: "reference graph exceeds the supported depth"}
	}
	target, problem := validator.resolve(reference)
	if problem != nil {
		return 0, problem
	}
	identity := fmt.Sprintf("%d:%s", reference.kind, target.path)
	if _, cycle := validator.visiting[identity]; cycle {
		return 0, &Problem{Path: reference.path, Code: "circular_reference", Message: "circular references are not supported"}
	}
	if height, done := validator.heights[identity]; done {
		if depth+height >= maxReferenceDepth {
			return 0, &Problem{Path: "$", Code: "document_too_complex", Message: "reference graph exceeds the supported depth"}
		}
		return height, nil
	}
	validator.nodes++
	if validator.nodes > maxReferenceNodes {
		return 0, &Problem{Path: "$", Code: "document_too_complex", Message: "reference graph exceeds the supported size"}
	}
	validator.visiting[identity] = struct{}{}
	collector := &referenceValidator{
		root: validator.root, anchors: validator.anchors, dynamicAnchors: validator.dynamicAnchors,
		resources: validator.resources, resourceByPath: validator.resourceByPath,
	}
	collector.walkWithResource(target.value, target.path, reference.kind, target.resource)
	if collector.problem != nil {
		delete(validator.visiting, identity)
		return 0, collector.problem
	}
	height := 0
	for _, nested := range collector.references {
		nestedHeight, problem := validator.visitHeight(nested, depth+1)
		if problem != nil {
			delete(validator.visiting, identity)
			return 0, problem
		}
		if nestedHeight+1 > height {
			height = nestedHeight + 1
		}
	}
	delete(validator.visiting, identity)
	validator.heights[identity] = height
	return height, nil
}

func resolveJSONPointer(root any, pointer string) (referenceTarget, bool) {
	return resolveJSONPointerFrom(root, pointer, "$", schemaDocumentResource)
}

func resolveJSONPointerFrom(root any, pointer, rootPath, resource string) (referenceTarget, bool) {
	if !strings.HasPrefix(pointer, "/") {
		return referenceTarget{}, false
	}
	current := root
	path := rootPath
	for _, encoded := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment, ok := decodeJSONPointerSegment(encoded)
		if !ok {
			return referenceTarget{}, false
		}
		switch typed := current.(type) {
		case map[string]any:
			current, ok = typed[segment]
			if !ok {
				return referenceTarget{}, false
			}
			path = appendJSONPath(path, segment)
		case []any:
			if segment == "" || segment != "0" && (segment[0] == '0' || segment[0] < '1' || segment[0] > '9') {
				return referenceTarget{}, false
			}
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return referenceTarget{}, false
			}
			current = typed[index]
			path = fmt.Sprintf("%s[%d]", path, index)
		default:
			return referenceTarget{}, false
		}
	}
	return referenceTarget{value: current, path: path, resource: resource}, true
}

func decodeJSONPointerSegment(encoded string) (string, bool) {
	var decoded strings.Builder
	for index := 0; index < len(encoded); index++ {
		if encoded[index] != '~' {
			decoded.WriteByte(encoded[index])
			continue
		}
		if index+1 >= len(encoded) || encoded[index+1] != '0' && encoded[index+1] != '1' {
			return "", false
		}
		index++
		if encoded[index] == '0' {
			decoded.WriteByte('~')
		} else {
			decoded.WriteByte('/')
		}
	}
	return decoded.String(), true
}

func sortedAnyKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
