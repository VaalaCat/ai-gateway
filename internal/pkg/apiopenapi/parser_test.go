package apiopenapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseJSONOpenAPI30PreservesPlatformDocument(t *testing.T) {
	parsed, err := ParseJSON(readFixture(t, "openapi-3.0.json"))
	require.NoError(t, err)
	require.Equal(t, "User API", parsed.Name)
	require.Equal(t, "Manage users", parsed.Description)
	require.Equal(t, "3.0.3", parsed.Document.Version)
	require.Equal(t, "1.2.0", parsed.Document.Info.Version)
	require.Len(t, parsed.Document.Servers, 2)
	require.Equal(t, "https://backup.example.test/v1", parsed.Document.Servers[1].URL)
	require.JSONEq(t, `"users-v1"`, string(parsed.Document.Extensions["x-document-id"]))
	require.JSONEq(t, `"identity"`, string(parsed.Document.Info.Extensions["x-info-owner"]))
	require.Contains(t, parsed.Document.Components.SecuritySchemes, "ApiKeyAuth")
	require.JSONEq(t, `{"type":"object","required":["id","name"],"properties":{"id":{"type":"string"},"name":{"type":"string"}},"x-schema-storage":"primary"}`, string(parsed.Document.Components.Schemas["User"]))

	users := parsed.Paths["/users"]
	require.Equal(t, "#/components/parameters/TraceID", users.Parameters[0].Ref)
	require.Contains(t, users.Operations, "GET")
	require.Contains(t, users.Operations, "POST")
	require.NotContains(t, users.Operations, "get")
	require.Equal(t, "listUsers", users.Operations["GET"].OperationID)
	require.Equal(t, "#/components/requestBodies/NewUser", users.Operations["POST"].RequestBody.Ref)
	require.JSONEq(t, `"identity"`, string(users.Extensions["x-path-owner"]))
	require.JSONEq(t, `"admin"`, string(users.Operations["GET"].Extensions["x-operation-audience"]))
}

func TestParseJSONOpenAPI31PreservesJSONSchema31(t *testing.T) {
	parsed, err := ParseJSON(readFixture(t, "openapi-3.1.json"))
	require.NoError(t, err)
	require.Equal(t, "3.1.1", parsed.Document.Version)
	require.Equal(t, "https://json-schema.org/draft/2020-12/schema", parsed.Document.JSONSchemaDialect)
	require.Equal(t, "Order operations", parsed.Document.Info.Summary)
	require.Contains(t, parsed.Paths["/orders"].Operations, "POST")
	header := parsed.Paths["/orders"].Operations["POST"].Responses["201"].Headers["X-Order-Mode"]
	require.True(t, header.AllowEmptyValue)
	require.True(t, header.AllowReserved)
	require.JSONEq(t, `{"type":"object","properties":{"note":{"type":["string","null"]},"kind":{"const":"standard"}},"unevaluatedProperties":false}`, string(parsed.Document.Components.Schemas["OrderInput"]))
	require.JSONEq(t, `2`, string(parsed.Document.Extensions["x-document-generation"]))
}

func TestParseJSONRejectsUnsupportedWebhooks(t *testing.T) {
	_, err := ParseJSON([]byte(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"webhooks":{"event":{"post":{"responses":{"200":{"description":"ok"}}}}}}`))
	requireParseProblem(t, err, "$.webhooks", "unsupported_feature")
}

func TestParseJSONValidatesOnlySemanticReferences(t *testing.T) {
	raw := []byte(`{
      "openapi":"3.1.0","info":{"title":"API","version":"1"},
      "paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{
        "type":"object","default":{"$ref":"missing-default"},"examples":[{"$ref":"missing-example"}],
        "properties":{"value":{"type":"string","x-data":{"$ref":"missing-extension"}}}
      },"example":{"$ref":"missing-media-example"}}}}}}}},
      "components":{"examples":{"Data":{"value":{"$ref":"missing-component-example"}}}},
      "x-document-data":{"$ref":"missing-document-extension"}
    }`)
	parsed, err := ParseJSON(raw)
	require.NoError(t, err)
	require.JSONEq(t, `{"$ref":"missing-document-extension"}`, string(parsed.Document.Extensions["x-document-data"]))
}

func TestParseJSONSupportsLocalAnchorAndEscapedJSONPointers(t *testing.T) {
	tests := []struct {
		name string
		ref  string
	}{
		{name: "anchor", ref: "#itemAnchor"},
		{name: "percent space", ref: "#/components/schemas/Item%20Record"},
		{name: "percent slash delimiters", ref: "#%2Fcomponents%2Fschemas%2FItem%20Record"},
		{name: "RFC6901 slash", ref: "#/components/schemas/Slash~1Name"},
		{name: "array index", ref: "#/components/schemas/Parts/allOf/0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{
          "openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":%q}}}}}}}},
          "components":{"schemas":{"Anchored":{"$anchor":"itemAnchor","type":"string"},"Item Record":{"type":"string"},"Slash/Name":{"type":"string"},"Parts":{"allOf":[{"type":"string"}]}}}
        }`, tt.ref)
			_, err := ParseJSON([]byte(raw))
			require.NoError(t, err)
		})
	}
}

func TestParseJSONValidatesUnevaluatedItemsReferences(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{
          "Leaf":{"type":"string"},
          "List":{"type":"array","unevaluatedItems":{"$ref":"#/components/schemas/Leaf"}}
        }`))
		require.NoError(t, err)
	})

	t.Run("external", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{
          "List":{"type":"array","unevaluatedItems":{"$ref":"other.json#/Leaf"}}
        }`))
		requireParseProblem(t, err, "$.components.schemas.List.unevaluatedItems.$ref", "external_reference")
	})

	t.Run("cycle", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{
          "A":{"type":"array","unevaluatedItems":{"$ref":"#/components/schemas/B"}},
          "B":{"$ref":"#/components/schemas/A"}
        }`))
		requireParseProblem(t, err, "$.components.schemas.A.unevaluatedItems.$ref", "circular_reference")
	})
}

func TestParseJSONSupportsDynamicReferences(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{
          "Target":{"$dynamicAnchor":"node","type":"string"},
          "Use":{"$dynamicRef":"#node"}
        }`))
		require.NoError(t, err)
	})

	t.Run("external", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{"Use":{"$dynamicRef":"https://schemas.example.test/types.json#node"}}`))
		requireParseProblem(t, err, "$.components.schemas.Use.$dynamicRef", "external_reference")
	})

	t.Run("cycle", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{
          "A":{"$dynamicAnchor":"a","$dynamicRef":"#b"},
          "B":{"$dynamicAnchor":"b","$dynamicRef":"#a"}
        }`))
		requireParseProblem(t, err, "$.components.schemas.A.$dynamicRef", "circular_reference")
	})

	t.Run("data keyword", func(t *testing.T) {
		_, err := ParseJSON(openAPIWithSchemas(`{
          "Data":{"type":"object","default":{"$dynamicRef":"https://data.example.test/not-a-schema"}}
        }`))
		require.NoError(t, err)
	})
}

func TestParseJSONScopesAnchorsBySchemaResource(t *testing.T) {
	raw := openAPIWithSchemas(`{
      "First":{
        "$id":"https://schemas.example.test/first",
        "$defs":{"Target":{"$anchor":"item","type":"string"},"Use":{"$ref":"#item"}}
      },
      "Second":{
        "$id":"https://schemas.example.test/second",
        "$defs":{"Target":{"$anchor":"item","type":"integer"},"Use":{"$ref":"#item"}}
      },
      "DynamicFirst":{
        "$id":"urn:example:dynamic-first",
        "$defs":{"Target":{"$dynamicAnchor":"node","type":"string"},"Use":{"$dynamicRef":"#node"}}
      },
      "DynamicSecond":{
        "$id":"urn:example:dynamic-second",
        "$defs":{"Target":{"$dynamicAnchor":"node","type":"integer"},"Use":{"$dynamicRef":"#node"}}
      }
    }`)
	_, err := ParseJSON(raw)
	require.NoError(t, err, "absolute $id values establish local resources but must not trigger network fetches")

	var root any
	require.NoError(t, json.Unmarshal(raw, &root))
	validator := newReferenceValidator(root)
	validator.collectDocument(root)
	require.Nil(t, validator.problem)
	assertReferenceTargetPath(t, validator, "$.components.schemas.First.$defs.Use.$ref", "$.components.schemas.First.$defs.Target")
	assertReferenceTargetPath(t, validator, "$.components.schemas.Second.$defs.Use.$ref", "$.components.schemas.Second.$defs.Target")
	assertReferenceTargetPath(t, validator, "$.components.schemas.DynamicFirst.$defs.Use.$dynamicRef", "$.components.schemas.DynamicFirst.$defs.Target")
	assertReferenceTargetPath(t, validator, "$.components.schemas.DynamicSecond.$defs.Use.$dynamicRef", "$.components.schemas.DynamicSecond.$defs.Target")

	_, err = ParseJSON(openAPIWithSchemas(`{
      "Scoped":{
        "$id":"https://schemas.example.test/scoped/root.json",
        "$defs":{"Use":{"$ref":"child.json#item"}}
      }
    }`))
	requireParseProblem(t, err, "$.components.schemas.Scoped.$defs.Use.$ref", "external_reference")
}

func TestParseJSONBoundsDynamicReferenceGraphComplexity(t *testing.T) {
	var schemas strings.Builder
	for index := 0; index < 300; index++ {
		if index > 0 {
			schemas.WriteByte(',')
		}
		if index == 0 {
			fmt.Fprintf(&schemas, `"S%d":{"$dynamicAnchor":"a%d","type":"string"}`, index, index)
		} else {
			fmt.Fprintf(&schemas, `"S%d":{"$dynamicAnchor":"a%d","$dynamicRef":"#a%d"}`, index, index, index-1)
		}
	}
	_, err := ParseJSON(openAPIWithSchemas("{" + schemas.String() + "}"))
	requireParseProblem(t, err, "$", "document_too_complex")
}

func TestParseJSONRejectsMalformedLocalReferences(t *testing.T) {
	for _, tt := range []struct {
		name, ref string
	}{
		{name: "bad percent", ref: "#/components/schemas/Bad%ZZ"},
		{name: "bad tilde", ref: "#/components/schemas/Bad~2Name"},
		{name: "leading zero array index", ref: "#/components/schemas/Parts/allOf/00"},
		{name: "missing anchor", ref: "#missingAnchor"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":%q}}}}}}}},"components":{"schemas":{"Parts":{"allOf":[{"type":"string"}]}}}}`, tt.ref)
			_, err := ParseJSON([]byte(raw))
			requireParseProblem(t, err, `$.paths["/items"].get.responses["200"].content["application/json"].schema.$ref`, "invalid_reference")
		})
	}
}

func TestParseJSONRejectsInvalidComponentPathItemMethod(t *testing.T) {
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"pathItems":{"Shared":{"fetch":{"responses":{"200":{"description":"ok"}}}}}}}`)
	_, err := ParseJSON(raw)
	requireParseProblem(t, err, "$.components.pathItems.Shared.fetch", "invalid_method")
}

func TestParseJSONDocumentSizeBoundary(t *testing.T) {
	base := []byte(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{}}`)
	atLimit := append(append([]byte(nil), base...), bytes.Repeat([]byte(" "), MaxDocumentBytes-len(base))...)
	parsed, err := ParseJSON(atLimit)
	require.NoError(t, err)
	require.Equal(t, "API", parsed.Name)

	tooLarge := append(atLimit, ' ')
	_, err = ParseJSON(tooLarge)
	requireParseProblem(t, err, "$", "document_too_large")
}

func TestParseJSONBoundsReferenceGraphComplexity(t *testing.T) {
	var schemas strings.Builder
	for index := 0; index < 300; index++ {
		if index > 0 {
			schemas.WriteByte(',')
		}
		if index == 0 {
			fmt.Fprintf(&schemas, `"S%d":{"type":"string"}`, index)
		} else {
			fmt.Fprintf(&schemas, `"S%d":{"$ref":"#/components/schemas/S%d"}`, index, index-1)
		}
	}
	longChain := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":{%s}}}`, schemas.String())
	_, err := ParseJSON([]byte(longChain))
	requireParseProblem(t, err, "$", "document_too_complex")

	var properties strings.Builder
	for index := 0; index < 1000; index++ {
		if index > 0 {
			properties.WriteByte(',')
		}
		fmt.Fprintf(&properties, `"p%d":{"$ref":"#/components/schemas/Leaf"}`, index)
	}
	wideDAG := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":{"Leaf":{"type":"string"},"Root":{"type":"object","properties":{%s}}}}}`, properties.String())
	_, err = ParseJSON([]byte(wideDAG))
	require.NoError(t, err)
}

func TestParseJSONUsesStableValidationMessage(t *testing.T) {
	_, err := ParseJSON([]byte(`{"openapi":"3.1.0","info":{"title":"API"},"paths":{}}`))
	var parseErr *ParseError
	require.ErrorAs(t, err, &parseErr)
	require.Equal(t, "document failed OpenAPI validation", parseErr.Problems[0].Message)
	require.NotContains(t, parseErr.Error(), "version")
}

func TestParseJSONRejectsInvalidDocumentsWithStructuredProblems(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantPath string
		wantCode string
	}{
		{name: "invalid JSON", raw: `{"openapi":`, wantPath: "$", wantCode: "invalid_json"},
		{name: "YAML", raw: "openapi: 3.1.0\ninfo:\n  title: API", wantPath: "$", wantCode: "invalid_json"},
		{name: "missing version", raw: `{"info":{"title":"API","version":"1"},"paths":{}}`, wantPath: "$.openapi", wantCode: "unsupported_version"},
		{name: "Swagger 2", raw: `{"swagger":"2.0","info":{"title":"API","version":"1"},"paths":{}}`, wantPath: "$.openapi", wantCode: "unsupported_version"},
		{name: "illegal version", raw: `{"openapi":"banana","info":{"title":"API","version":"1"},"paths":{}}`, wantPath: "$.openapi", wantCode: "unsupported_version"},
		{name: "unsupported version", raw: `{"openapi":"3.2.0","info":{"title":"API","version":"1"},"paths":{}}`, wantPath: "$.openapi", wantCode: "unsupported_version"},
		{name: "invalid path", raw: `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"items":{"get":{"responses":{"200":{"description":"ok"}}}}}}`, wantPath: "$.paths.items", wantCode: "invalid_path"},
		{name: "invalid method", raw: `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/items":{"fetch":{"responses":{"200":{"description":"ok"}}}}}}`, wantPath: `$.paths["/items"].fetch`, wantCode: "invalid_method"},
		{name: "uppercase method", raw: `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/items":{"GET":{"responses":{"200":{"description":"ok"}}}}}}`, wantPath: `$.paths["/items"].GET`, wantCode: "invalid_method"},
		{name: "non-string ref", raw: validDocumentWith(`{"$ref":42}`), wantPath: `$.paths["/items"].get.responses["200"].content["application/json"].schema.$ref`, wantCode: "invalid_reference"},
		{name: "illegal fragment ref", raw: validDocumentWith(`{"$ref":"#missing"}`), wantPath: `$.paths["/items"].get.responses["200"].content["application/json"].schema.$ref`, wantCode: "invalid_reference"},
		{name: "missing local ref", raw: validDocumentWith(`{"$ref":"#/components/schemas/Missing"}`), wantPath: `$.paths["/items"].get.responses["200"].content["application/json"].schema.$ref`, wantCode: "invalid_reference"},
		{name: "external file ref", raw: validDocumentWith(`{"$ref":"other.json#/Item"}`), wantPath: `$.paths["/items"].get.responses["200"].content["application/json"].schema.$ref`, wantCode: "external_reference"},
		{name: "external URL ref", raw: validDocumentWith(`{"$ref":"https://example.test/item.json#/Item"}`), wantPath: `$.paths["/items"].get.responses["200"].content["application/json"].schema.$ref`, wantCode: "external_reference"},
		{name: "circular ref", raw: `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":{"A":{"$ref":"#/components/schemas/B"},"B":{"$ref":"#/components/schemas/A"}}}}`, wantPath: `$.components.schemas.A.$ref`, wantCode: "circular_reference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseJSON([]byte(tt.raw))
			var parseErr *ParseError
			require.ErrorAs(t, err, &parseErr)
			require.NotEmpty(t, parseErr.Problems)
			require.Equal(t, tt.wantPath, parseErr.Problems[0].Path)
			require.Equal(t, tt.wantCode, parseErr.Problems[0].Code)
			require.NotEmpty(t, parseErr.Problems[0].Message)
			require.NotEqual(t, tt.raw, parseErr.Error(), "errors must not echo uploaded documents")
		})
	}
}

func TestParseJSONAcceptsEmptyPaths(t *testing.T) {
	parsed, err := ParseJSON([]byte(`{"openapi":"3.1.0","info":{"title":"Empty API","version":"1"},"paths":{}}`))
	require.NoError(t, err)
	require.Equal(t, "Empty API", parsed.Name)
	require.Empty(t, parsed.Paths)
}

func TestParseErrorSupportsErrorsAs(t *testing.T) {
	err := error(&ParseError{Problems: []Problem{{Path: "$", Code: "invalid_json", Message: "invalid JSON"}}})
	var target *ParseError
	require.True(t, errors.As(err, &target))
	require.Contains(t, err.Error(), "invalid_json")
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	return raw
}

func validDocumentWith(schema string) string {
	return `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":` + schema + `}}}}}}}}`
}

func openAPIWithSchemas(schemas string) []byte {
	return []byte(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":` + schemas + `}}`)
}

func assertReferenceTargetPath(t *testing.T, validator *referenceValidator, referencePath, targetPath string) {
	t.Helper()
	for _, reference := range validator.references {
		if reference.path != referencePath {
			continue
		}
		target, problem := validator.resolve(reference)
		require.Nil(t, problem)
		require.Equal(t, targetPath, target.path)
		return
	}
	t.Fatalf("reference %s was not collected", referencePath)
}

func requireParseProblem(t *testing.T, err error, path, code string) {
	t.Helper()
	var parseErr *ParseError
	require.ErrorAs(t, err, &parseErr)
	require.NotEmpty(t, parseErr.Problems)
	require.Equal(t, path, parseErr.Problems[0].Path)
	require.Equal(t, code, parseErr.Problems[0].Code)
}
