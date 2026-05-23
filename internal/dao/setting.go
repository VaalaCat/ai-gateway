package dao

import (
	"strconv"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type AdminSettingQuery interface {
	Get(key string) (*models.Setting, error)
	Lookup(key string) (*models.Setting, bool, error)
	GetAll() ([]models.Setting, error)

	// LookupString returns the setting value for key, or fallback if the key
	// is missing or an error occurs while loading it.
	LookupString(key, fallback string) string
	// LookupInt parses the setting value as int. If the key is missing,
	// the value is empty, or strconv.Atoi fails, fallback is returned.
	LookupInt(key string, fallback int) int
	// LookupBool parses the setting value as bool. Accepted truthy spellings
	// (case-insensitive) are "true", "1", "yes"; falsy are "false", "0", "no".
	// If the key is missing, the value is empty, or it cannot be parsed as a
	// bool, fallback is returned.
	LookupBool(key string, fallback bool) bool
	// LookupFloat parses the setting value as float64. If the key is missing,
	// the value is empty, or strconv.ParseFloat fails, fallback is returned.
	LookupFloat(key string, fallback float64) float64
}

type AdminSettingMutation interface {
	Set(key string, value string) error
	Delete(key string) error
}

type adminSettingQuery struct{ ctx *baseContext }
type adminSettingMutation struct{ ctx *baseContext }

func (q *adminSettingQuery) Get(key string) (*models.Setting, error) {
	var s models.Setting
	err := q.ctx.GetDB().Where("key = ?", key).First(&s).Error
	return &s, err
}

func (q *adminSettingQuery) Lookup(key string) (*models.Setting, bool, error) {
	var s models.Setting
	tx := q.ctx.GetDB().Where("key = ?", key).Limit(1).Find(&s)
	if tx.Error != nil {
		return nil, false, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, false, nil
	}
	return &s, true, nil
}

func (q *adminSettingQuery) GetAll() ([]models.Setting, error) {
	var settings []models.Setting
	err := q.ctx.GetDB().Find(&settings).Error
	return settings, err
}

// lookupRaw returns the raw value plus whether a non-empty value is available.
// "missing" covers: row not found, DB error, or stored value is empty.
func (q *adminSettingQuery) lookupRaw(key string) (string, bool) {
	s, found, err := q.Lookup(key)
	if err != nil || !found || s == nil || s.Value == "" {
		return "", false
	}
	return s.Value, true
}

func (q *adminSettingQuery) LookupString(key, fallback string) string {
	s, found, err := q.Lookup(key)
	if err != nil || !found || s == nil {
		return fallback
	}
	return s.Value
}

func (q *adminSettingQuery) LookupInt(key string, fallback int) int {
	raw, ok := q.lookupRaw(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func (q *adminSettingQuery) LookupBool(key string, fallback bool) bool {
	raw, ok := q.lookupRaw(key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}

func (q *adminSettingQuery) LookupFloat(key string, fallback float64) float64 {
	raw, ok := q.lookupRaw(key)
	if !ok {
		return fallback
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return f
}

func (m *adminSettingMutation) Set(key string, value string) error {
	setting := models.Setting{Key: key}
	// Use map for Assign to ensure zero-value fields (e.g. empty string) are persisted.
	return m.ctx.GetDB().Where("key = ?", key).
		Assign(map[string]any{"value": value}).
		FirstOrCreate(&setting).Error
}

func (m *adminSettingMutation) Delete(key string) error {
	return m.ctx.GetDB().Where("key = ?", key).Delete(&models.Setting{}).Error
}
