package models

import "gorm.io/datatypes"

type ModelConfig struct {
	ID               uint                                      `gorm:"primaryKey" json:"id"`
	ModelName        string                                    `gorm:"uniqueIndex;size:128" json:"model_name"`
	InputPrice       float64                                   `gorm:"default:0" json:"input_price"`
	OutputPrice      float64                                   `gorm:"default:0" json:"output_price"`
	CacheReadPrice   float64                                   `gorm:"default:0" json:"cache_read_price"`
	CacheWritePrice  float64                                   `gorm:"default:0" json:"cache_write_price"`
	Status           int                                       `gorm:"default:1" json:"status"`
	SyncedMetadata   datatypes.JSONType[ModelMetadata]         `gorm:"type:text;not null;default:'{}'" json:"synced_metadata"`
	MetadataOverride datatypes.JSONType[ModelMetadataOverride] `gorm:"type:text;not null;default:'{}'" json:"metadata_override"`
	CreatedAt        int64                                     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        int64                                     `gorm:"autoUpdateTime" json:"updated_at"`
}

func (m ModelConfig) EffectiveMetadata() ModelMetadata {
	return EffectiveModelMetadata(m.SyncedMetadata.Data(), m.MetadataOverride.Data())
}
