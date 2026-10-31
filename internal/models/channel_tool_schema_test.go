package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateToolSchemaCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name, settings string
		wantError      bool
	}{
		{"unset", `{}`, false},
		{"empty", `{"model_tool_schema_compatibility":[]}`, false},
		{"Go inline flags", `{"model_tool_schema_compatibility":[{"model_pattern":"(?i)^gemini-","profile":"gemini_compat"}]}`, false},
		{"none exclusion", `{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-excluded$","profile":"none"}]}`, false},
		{"lookahead unsupported by Go", `{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-(?=4)","profile":"none"}]}`, true},
		{"invalid pattern", `{"model_tool_schema_compatibility":[{"model_pattern":"[","profile":"gemini_compat"}]}`, true},
		{"missing pattern", `{"model_tool_schema_compatibility":[{"profile":"gemini_compat"}]}`, true},
		{"unknown profile", `{"model_tool_schema_compatibility":[{"model_pattern":"gemini","profile":"unknown"}]}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateToolSchemaCompatibility(tt.settings)
			if tt.wantError {
				require.ErrorContains(t, err, "model_tool_schema_compatibility")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
