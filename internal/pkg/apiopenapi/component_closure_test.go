package apiopenapi

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildComponentGraphUsesLinearIndexedOperations(t *testing.T) {
	for _, size := range []int{1000, 4096} {
		t.Run(fmt.Sprintf("%d components", size), func(t *testing.T) {
			root := componentChainDocument(size)
			validator := newReferenceValidator(root)
			validator.collectDocument(root)
			require.Nil(t, validator.problem)
			components := root["components"].(map[string]any)

			graph, stats, err := buildComponentGraph(validator, componentIdentities(components))
			require.NoError(t, err)
			require.Len(t, graph.roots, 1)
			require.Equal(t, size, stats.componentEntries)
			require.Equal(t, size, stats.referenceResolutions)
			require.Equal(t, size*2, stats.ownerLookups)
			require.Equal(t, size, stats.edgeInsertions)
			require.LessOrEqual(t, stats.ownerPathSteps, stats.ownerLookups*128)
		})
	}
}

func TestBuildComponentGraphRejectsUnresolvedReference(t *testing.T) {
	root := map[string]any{
		"paths": map[string]any{"/items": map[string]any{
			"get": map[string]any{"responses": map[string]any{
				"200": map[string]any{"description": "ok", "content": map[string]any{
					"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Missing"}},
				}},
			}},
		}},
		"components": map[string]any{"schemas": map[string]any{}},
	}
	validator := newReferenceValidator(root)
	validator.collectDocument(root)

	_, _, err := buildComponentGraph(validator, componentIdentities(root["components"].(map[string]any)))
	require.ErrorContains(t, err, "invalid_reference")
}

func TestBuildComponentGraphHandlesEmptyComponents(t *testing.T) {
	root := map[string]any{"paths": map[string]any{}, "components": map[string]any{}}
	validator := newReferenceValidator(root)
	validator.collectDocument(root)

	graph, stats, err := buildComponentGraph(validator, componentIdentities(root["components"].(map[string]any)))
	require.NoError(t, err)
	require.Empty(t, graph.roots)
	require.Empty(t, graph.outgoing)
	require.Zero(t, stats.ownerLookups)
	require.Zero(t, stats.ownerPathSteps)
}

func componentChainDocument(size int) map[string]any {
	schemas := make(map[string]any, size)
	for index := 0; index < size; index++ {
		name := fmt.Sprintf("Schema%04d", index)
		schema := map[string]any{"type": "object"}
		if index+1 < size {
			schema["properties"] = map[string]any{"next": map[string]any{
				"$ref": fmt.Sprintf("#/components/schemas/Schema%04d", index+1),
			}}
		}
		schemas[name] = schema
	}
	return map[string]any{
		"paths": map[string]any{"/items": map[string]any{
			"get": map[string]any{"responses": map[string]any{
				"200": map[string]any{"description": "ok", "content": map[string]any{
					"application/json": map[string]any{"schema": map[string]any{
						"$ref": "#/components/schemas/Schema0000",
					}},
				}},
			}},
		}},
		"components": map[string]any{"schemas": schemas},
	}
}
