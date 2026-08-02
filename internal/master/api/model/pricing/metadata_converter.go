package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type modelsDevMetadataProvider struct {
	Models map[string]modelsDevMetadataModel `json:"models"`
}

type modelsDevMetadataModel struct {
	Name                string                      `json:"name"`
	Description         string                      `json:"description"`
	Modalities          modelsDevMetadataModalities `json:"modalities"`
	Limit               modelsDevMetadataLimit      `json:"limit"`
	SupportedParameters []string                    `json:"supported_parameters"`
	ToolCalling         bool                        `json:"tool_call"`
	StructuredOutput    bool                        `json:"structured_output"`
	Reasoning           bool                        `json:"reasoning"`
	Cost                *modelsDevCost              `json:"cost"`
}

type modelsDevMetadataModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type modelsDevMetadataLimit struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

// ValidateModelsDevMetadata distinguishes a valid models.dev payload from
// syntactically valid JSON with an incompatible shape. ConvertModelsDevMetadata
// keeps its map-only contract, while sync uses this validation error for its
// metadata_source_error response.
func ValidateModelsDevMetadata(data []byte) error {
	var providers map[string]modelsDevMetadataProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		return fmt.Errorf("invalid models.dev metadata: %w", err)
	}
	if len(providers) == 0 {
		return errors.New("invalid models.dev metadata: provider map is empty")
	}
	for providerKey, provider := range providers {
		if provider.Models == nil {
			return fmt.Errorf("invalid models.dev metadata: provider %q has no models object", providerKey)
		}
	}
	return nil
}

// ConvertModelsDevMetadata normalizes the approved marketplace fields. When a
// model ID appears under multiple providers, the lexicographically first
// provider key wins; this makes the one-model-to-one-metadata collapse stable
// without inventing a provider priority policy.
func ConvertModelsDevMetadata(data []byte) map[string]models.ModelMetadata {
	var providers map[string]modelsDevMetadataProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		return map[string]models.ModelMetadata{}
	}

	providerKeys := make([]string, 0, len(providers))
	for providerKey := range providers {
		providerKeys = append(providerKeys, providerKey)
	}
	sort.Strings(providerKeys)

	result := make(map[string]models.ModelMetadata)
	for _, providerKey := range providerKeys {
		for modelName, source := range providers[providerKey].Models {
			if _, exists := result[modelName]; exists {
				continue
			}
			result[modelName] = models.ModelMetadata{
				DisplayName:         source.Name,
				Description:         source.Description,
				Provider:            providerKey,
				InputModalities:     nonNilMetadataList(source.Modalities.Input),
				OutputModalities:    nonNilMetadataList(source.Modalities.Output),
				ContextLength:       source.Limit.Context,
				MaxOutputTokens:     source.Limit.Output,
				SupportedParameters: nonNilMetadataList(source.SupportedParameters),
				ToolCalling:         source.ToolCalling,
				StructuredOutput:    source.StructuredOutput,
				Reasoning:           source.Reasoning,
				PromptCache:         source.Cost != nil && (source.Cost.CacheRead != nil || source.Cost.CacheWrite != nil),
			}
		}
	}
	return result
}

func nonNilMetadataList(values []string) []string {
	return append([]string{}, values...)
}
