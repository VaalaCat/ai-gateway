package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func metadataPointer[T any](value T) *T { return &value }

func TestEffectiveModelMetadataInheritsSyncedValuesWhenOverrideFieldsAreAbsent(t *testing.T) {
	synced := ModelMetadata{
		DisplayName:         "Synced name",
		Description:         "Synced description",
		Provider:            "openai",
		InputModalities:     []string{"text", "image"},
		OutputModalities:    []string{"text"},
		ContextLength:       128000,
		MaxOutputTokens:     16384,
		SupportedParameters: []string{"temperature"},
		ToolCalling:         true,
		StructuredOutput:    true,
		Reasoning:           true,
		PromptCache:         true,
	}

	effective := EffectiveModelMetadata(synced, ModelMetadataOverride{})
	require.Equal(t, synced, effective)
	effective.InputModalities[0] = "audio"
	require.Equal(t, "text", synced.InputModalities[0], "effective metadata must not alias synced slices")
}

func TestEffectiveModelMetadataAppliesExplicitScalarZeroValues(t *testing.T) {
	effective := EffectiveModelMetadata(
		ModelMetadata{
			DisplayName:      "Synced name",
			Description:      "Synced description",
			Provider:         "openai",
			ContextLength:    128000,
			MaxOutputTokens:  16384,
			ToolCalling:      true,
			StructuredOutput: true,
			Reasoning:        true,
			PromptCache:      true,
		},
		ModelMetadataOverride{
			DisplayName:      metadataPointer(""),
			Description:      metadataPointer(""),
			Provider:         metadataPointer(""),
			ContextLength:    metadataPointer[int64](0),
			MaxOutputTokens:  metadataPointer[int64](0),
			ToolCalling:      metadataPointer(false),
			StructuredOutput: metadataPointer(false),
			Reasoning:        metadataPointer(false),
			PromptCache:      metadataPointer(false),
		},
	)

	require.Empty(t, effective.DisplayName)
	require.Empty(t, effective.Description)
	require.Empty(t, effective.Provider)
	require.Zero(t, effective.ContextLength)
	require.Zero(t, effective.MaxOutputTokens)
	require.False(t, effective.ToolCalling)
	require.False(t, effective.StructuredOutput)
	require.False(t, effective.Reasoning)
	require.False(t, effective.PromptCache)
}

func TestEffectiveModelMetadataAppliesExplicitEmptyLists(t *testing.T) {
	empty := datatypes.JSONSlice[string]{}
	effective := EffectiveModelMetadata(
		ModelMetadata{
			InputModalities:     []string{"text", "image"},
			OutputModalities:    []string{"text"},
			SupportedParameters: []string{"temperature", "top_p"},
		},
		ModelMetadataOverride{
			InputModalities:     &empty,
			OutputModalities:    &empty,
			SupportedParameters: &empty,
		},
	)

	require.NotNil(t, effective.InputModalities)
	require.Empty(t, effective.InputModalities)
	require.NotNil(t, effective.OutputModalities)
	require.Empty(t, effective.OutputModalities)
	require.NotNil(t, effective.SupportedParameters)
	require.Empty(t, effective.SupportedParameters)
}

func TestModelConfigEffectiveMetadataUsesPersistedSyncedAndOverrideValues(t *testing.T) {
	config := ModelConfig{
		SyncedMetadata: datatypes.NewJSONType(ModelMetadata{ToolCalling: true, ContextLength: 128000}),
		MetadataOverride: datatypes.NewJSONType(ModelMetadataOverride{
			ToolCalling:   metadataPointer(false),
			ContextLength: metadataPointer[int64](0),
		}),
	}

	require.Equal(t, ModelMetadata{ToolCalling: false, ContextLength: 0}, config.EffectiveMetadata())
}

func TestModelMetadataOverrideJSONPreservesPresentEmptyList(t *testing.T) {
	var override ModelMetadataOverride
	require.NoError(t, json.Unmarshal([]byte(`{"input_modalities":[]}`), &override))
	require.NotNil(t, override.InputModalities)
	require.Empty(t, *override.InputModalities)

	raw, err := json.Marshal(override)
	require.NoError(t, err)
	require.JSONEq(t, `{"input_modalities":[]}`, string(raw))
}
