package models

import "gorm.io/datatypes"

// ModelMetadata is the normalized model information synced from models.dev.
// It deliberately contains only the first marketplace version's approved fields.
type ModelMetadata struct {
	DisplayName         string   `json:"display_name"`
	Description         string   `json:"description"`
	Provider            string   `json:"provider"`
	InputModalities     []string `json:"input_modalities"`
	OutputModalities    []string `json:"output_modalities"`
	ContextLength       int64    `json:"context_length"`
	MaxOutputTokens     int64    `json:"max_output_tokens"`
	SupportedParameters []string `json:"supported_parameters"`
	ToolCalling         bool     `json:"tool_calling"`
	StructuredOutput    bool     `json:"structured_output"`
	Reasoning           bool     `json:"reasoning"`
	PromptCache         bool     `json:"prompt_cache"`
}

// ModelMetadataOverride distinguishes an absent field (inherit the synced
// value) from an explicitly supplied zero value. List pointers preserve the
// same distinction for an explicitly empty array.
type ModelMetadataOverride struct {
	DisplayName         *string                      `json:"display_name,omitempty"`
	Description         *string                      `json:"description,omitempty"`
	Provider            *string                      `json:"provider,omitempty"`
	InputModalities     *datatypes.JSONSlice[string] `json:"input_modalities,omitempty"`
	OutputModalities    *datatypes.JSONSlice[string] `json:"output_modalities,omitempty"`
	ContextLength       *int64                       `json:"context_length,omitempty"`
	MaxOutputTokens     *int64                       `json:"max_output_tokens,omitempty"`
	SupportedParameters *datatypes.JSONSlice[string] `json:"supported_parameters,omitempty"`
	ToolCalling         *bool                        `json:"tool_calling,omitempty"`
	StructuredOutput    *bool                        `json:"structured_output,omitempty"`
	Reasoning           *bool                        `json:"reasoning,omitempty"`
	PromptCache         *bool                        `json:"prompt_cache,omitempty"`
}

type metadataOverrideApplier func(*ModelMetadata, ModelMetadataOverride)

var metadataOverrideAppliers = []metadataOverrideApplier{
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.DisplayName != nil {
			target.DisplayName = *override.DisplayName
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.Description != nil {
			target.Description = *override.Description
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.Provider != nil {
			target.Provider = *override.Provider
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.InputModalities != nil {
			target.InputModalities = cloneMetadataList(*override.InputModalities)
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.OutputModalities != nil {
			target.OutputModalities = cloneMetadataList(*override.OutputModalities)
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.ContextLength != nil {
			target.ContextLength = *override.ContextLength
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.MaxOutputTokens != nil {
			target.MaxOutputTokens = *override.MaxOutputTokens
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.SupportedParameters != nil {
			target.SupportedParameters = cloneMetadataList(*override.SupportedParameters)
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.ToolCalling != nil {
			target.ToolCalling = *override.ToolCalling
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.StructuredOutput != nil {
			target.StructuredOutput = *override.StructuredOutput
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.Reasoning != nil {
			target.Reasoning = *override.Reasoning
		}
	},
	func(target *ModelMetadata, override ModelMetadataOverride) {
		if override.PromptCache != nil {
			target.PromptCache = *override.PromptCache
		}
	},
}

// EffectiveModelMetadata returns synced metadata with every explicitly present
// admin override applied. The returned slices never alias persisted values.
func EffectiveModelMetadata(synced ModelMetadata, override ModelMetadataOverride) ModelMetadata {
	effective := synced
	effective.InputModalities = cloneMetadataList(synced.InputModalities)
	effective.OutputModalities = cloneMetadataList(synced.OutputModalities)
	effective.SupportedParameters = cloneMetadataList(synced.SupportedParameters)
	for _, apply := range metadataOverrideAppliers {
		apply(&effective, override)
	}
	return effective
}

func cloneMetadataList(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
