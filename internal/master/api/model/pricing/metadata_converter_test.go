package pricing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelMetadataConverterMapsApprovedFields(t *testing.T) {
	metadata := ConvertModelsDevMetadata([]byte(`{
		"openai": {"name":"OpenAI","models": {
			"gpt-5": {
				"name":"GPT-5", "description":"Flagship model",
				"modalities":{"input":["text","image"],"output":["text"]},
				"limit":{"context":400000,"output":128000},
				"supported_parameters":["temperature","top_p"],
				"tool_call":true,"structured_output":true,"reasoning":true,
				"cost":{"input":1.25,"output":10,"cache_read":0}
			}
		}}
	}`))

	require.Equal(t, "GPT-5", metadata["gpt-5"].DisplayName)
	require.Equal(t, "Flagship model", metadata["gpt-5"].Description)
	require.Equal(t, "openai", metadata["gpt-5"].Provider)
	require.Equal(t, []string{"text", "image"}, metadata["gpt-5"].InputModalities)
	require.Equal(t, []string{"text"}, metadata["gpt-5"].OutputModalities)
	require.EqualValues(t, 400000, metadata["gpt-5"].ContextLength)
	require.EqualValues(t, 128000, metadata["gpt-5"].MaxOutputTokens)
	require.Equal(t, []string{"temperature", "top_p"}, metadata["gpt-5"].SupportedParameters)
	require.True(t, metadata["gpt-5"].ToolCalling)
	require.True(t, metadata["gpt-5"].StructuredOutput)
	require.True(t, metadata["gpt-5"].Reasoning)
	require.True(t, metadata["gpt-5"].PromptCache, "a present zero-priced cache field still means cache is supported")
}

func TestModelMetadataConverterKeepsMissingCollectionsEmpty(t *testing.T) {
	metadata := ConvertModelsDevMetadata([]byte(`{"provider":{"models":{"plain":{"name":"Plain","modalities":{},"limit":{},"cost":{}}}}}`))
	plain := metadata["plain"]
	require.NotNil(t, plain.InputModalities)
	require.Empty(t, plain.InputModalities)
	require.NotNil(t, plain.OutputModalities)
	require.Empty(t, plain.OutputModalities)
	require.NotNil(t, plain.SupportedParameters)
	require.Empty(t, plain.SupportedParameters)
	require.False(t, plain.PromptCache)
}

func TestModelMetadataConverterRecognizesEitherCacheCostField(t *testing.T) {
	metadata := ConvertModelsDevMetadata([]byte(`{"provider":{"models":{
		"read":{"cost":{"cache_read":0}},
		"write":{"cost":{"cache_write":0}},
		"none":{"cost":{"input":1,"output":2}}
	}}}`))
	require.True(t, metadata["read"].PromptCache)
	require.True(t, metadata["write"].PromptCache)
	require.False(t, metadata["none"].PromptCache)
}

func TestModelMetadataConverterCollapsesDuplicateModelsByProviderKeyOrder(t *testing.T) {
	raw := []byte(`{
		"z-provider":{"models":{"shared":{"name":"Zed"}}},
		"a-provider":{"models":{"shared":{"name":"Alpha"}}}
	}`)
	for range 100 {
		metadata := ConvertModelsDevMetadata(raw)
		require.Equal(t, "a-provider", metadata["shared"].Provider)
		require.Equal(t, "Alpha", metadata["shared"].DisplayName)
	}
}

func TestModelMetadataConverterInvalidJSONReturnsEmptyMap(t *testing.T) {
	require.Empty(t, ConvertModelsDevMetadata([]byte("not-json")))
}

func TestModelMetadataConverterValidationRejectsWrongJSONShape(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("not-json"),
		[]byte("[]"),
		[]byte(`{"provider":{"models":"broken"}}`),
		[]byte(`{"provider":{}}`),
	} {
		require.Error(t, ValidateModelsDevMetadata(data))
	}
	require.NoError(t, ValidateModelsDevMetadata([]byte(`{"provider":{"models":{}}}`)))
}
