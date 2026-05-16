package legacy

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/dto"
)

// ParseJSONMap parses a JSON string into a map[string]interface{}.
// Returns nil if the input is empty or invalid.
func ParseJSONMap(raw string) map[string]interface{} {
	if raw == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

// ParseChannelSetting parses a JSON string into dto.ChannelSettings.
func ParseChannelSetting(raw string) dto.ChannelSettings {
	var s dto.ChannelSettings
	if raw != "" {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

// ParseChannelOtherSettings parses a JSON string into dto.ChannelOtherSettings.
func ParseChannelOtherSettings(raw string) dto.ChannelOtherSettings {
	var s dto.ChannelOtherSettings
	if raw != "" {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}
