package llmkit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConvertToolSchemas(t *testing.T) {
	input := `[{"type":"function","name":"read","strict":true,"input_schema":{
		"type":"object","required":["strict","exclusiveMinimum"],
		"properties":{
			"strict":{"type":"boolean","default":true},
			"exclusiveMinimum":{"type":"number","exclusiveMinimum":0,"minimum":-1},
			"nested":{"type":"array","items":{"anyOf":[{"type":"number","exclusiveMaximum":5},{"type":"null"}]}}
		},
		"$defs":{"exclusiveMaximum":{"type":"number","exclusiveMinimum":1}},
		"additionalProperties":{"type":"number","exclusiveMaximum":10},
		"default":{"strict":true,"exclusiveMinimum":2},
		"enum":[{"exclusiveMaximum":3}],
		"examples":[{"strict":true,"exclusiveMinimum":2}]
	}},{"type":"web_search","name":"web","raw_config":{"exclusiveMinimum":1}}]`
	want := `[{"type":"function","name":"read","description":"","input_schema":{
		"type":"object","required":["strict","exclusiveMinimum"],
		"properties":{
			"strict":{"type":"boolean","default":true},
			"exclusiveMinimum":{"type":"number","minimum":-1},
			"nested":{"type":"array","items":{"anyOf":[{"type":"number"},{"type":"null"}]}}
		},
		"$defs":{"exclusiveMaximum":{"type":"number"}},
		"additionalProperties":{"type":"number"},
		"default":{"strict":true,"exclusiveMinimum":2},
		"enum":[{"exclusiveMaximum":3}],
		"examples":[{"strict":true,"exclusiveMinimum":2}]
	}},{"type":"web_search","name":"web","description":"","input_schema":null,"raw_config":{"exclusiveMinimum":1}}]`

	for _, profile := range []ToolSchemaProfile{ToolSchemaProfileNone, ToolSchemaProfileGeminiCompat} {
		t.Run(string(profile), func(t *testing.T) {
			var tools []Tool
			require.NoError(t, json.Unmarshal([]byte(input), &tools))
			original, err := json.Marshal(tools)
			require.NoError(t, err)
			converted, changes := ConvertToolSchemas(tools, profile)
			encoded, err := json.Marshal(converted)
			require.NoError(t, err)
			if profile == ToolSchemaProfileNone {
				require.JSONEq(t, string(original), string(encoded))
				require.Empty(t, changes)
			} else {
				require.JSONEq(t, want, string(encoded))
				require.Equal(t, []ToolSchemaChange{
					{ToolName: "read", Path: "/tools/0/input_schema/$defs/exclusiveMaximum/exclusiveMinimum"},
					{ToolName: "read", Path: "/tools/0/input_schema/additionalProperties/exclusiveMaximum"},
					{ToolName: "read", Path: "/tools/0/input_schema/properties/exclusiveMinimum/exclusiveMinimum"},
					{ToolName: "read", Path: "/tools/0/input_schema/properties/nested/items/anyOf/0/exclusiveMaximum"},
					{ToolName: "read", Path: "/tools/0/strict"},
				}, changes)
				_, secondChanges := ConvertToolSchemas(converted, profile)
				require.Empty(t, secondChanges)
			}
			preserved, err := json.Marshal(tools)
			require.NoError(t, err)
			require.JSONEq(t, string(original), string(preserved), "conversion must preserve the original for another channel")
		})
	}
}

func TestConvertToolSchemasRawJSONPreservesNumbers(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"size":{"type":"integer","exclusiveMaximum":9007199254740993}},"default":{"size":9007199254740993}}`)
	tools := []Tool{{Type: "function", Name: "read", Namespace: "fs", InputSchema: raw}}
	converted, changes := ConvertToolSchemas(tools, ToolSchemaProfileGeminiCompat)
	encoded, err := json.Marshal(converted[0].InputSchema)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"size":9007199254740993`)
	require.NotContains(t, string(encoded), "exclusiveMaximum")
	require.Len(t, changes, 1)
	require.Equal(t, raw, tools[0].InputSchema)
}
