package models

import (
	"encoding/json"

	"gorm.io/datatypes"
)

type TokenTemplate struct {
	ID                uint                       `gorm:"primarykey" json:"id"`
	Name              string                     `gorm:"size:64;not null" json:"name"`
	Models            string                     `gorm:"type:text" json:"models"`
	ExpiryDays        int                        `gorm:"not null;default:-1" json:"expiry_days"`
	Status            int                        `gorm:"not null;default:1" json:"status"`
	AllowedChannelIDs datatypes.JSONSlice[uint]  `gorm:"type:text" json:"allowed_channel_ids"`
	CreatedAt         int64                      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         int64                      `gorm:"autoUpdateTime" json:"updated_at"`
}

func TokenFieldsEqual(tplModelsJSON string, tplChannelIDs []uint, tok *Token) bool {
	if !modelsEqual(tplModelsJSON, tok.Models) {
		return false
	}
	return channelsEqual(tplChannelIDs, []uint(tok.AllowedChannelIDs))
}

func modelsEqual(a, b string) bool {
	aSet, aOK := parseModelSet(a)
	bSet, bOK := parseModelSet(b)
	if !aOK || !bOK {
		return a == b
	}
	if len(aSet) != len(bSet) {
		return false
	}
	for k := range aSet {
		if _, ok := bSet[k]; !ok {
			return false
		}
	}
	return true
}

func parseModelSet(s string) (map[string]struct{}, bool) {
	if s == "" {
		return map[string]struct{}{}, true
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, false
	}
	set := make(map[string]struct{}, len(arr))
	for _, m := range arr {
		set[m] = struct{}{}
	}
	return set, true
}

func channelsEqual(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	aSet := make(map[uint]struct{}, len(a))
	for _, id := range a {
		aSet[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := aSet[id]; !ok {
			return false
		}
	}
	return true
}
