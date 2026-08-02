package channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type ChannelPatch struct {
	assignments  map[string]any
	applyFns     []func(*models.Channel)
	revisionBump bool
}

type channelPatchField struct {
	Decode      func(any) (any, error)
	Apply       func(*models.Channel, any)
	Assignments func(map[string]any, any)
	Lifecycle   bool
}

var readOnlyChannelPatchFields = map[string]struct{}{
	"id":                {},
	"name":              {},
	"created_at":        {},
	"updated_at":        {},
	"limit_state":       {},
	"auto_ban_state":    {},
	"auto_ban_revision": {},
}

var channelPatchFieldRegistry = map[string]channelPatchField{
	"status": buildStatusChannelPatchField(),
	"public_display_name": buildChannelPatchField(decodeChannelPatchPublicDisplayName, func(channel *models.Channel, value string) {
		channel.PublicDisplayName = value
	}),
	"type": buildChannelPatchField(decodeChannelPatchInt, func(channel *models.Channel, value int) {
		channel.Type = value
	}),
	"key": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Key = value
	}),
	"base_url": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.BaseURL = value
	}),
	"models": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Models = value
	}),
	"model_mapping": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.ModelMapping = value
	}),
	"weight": buildChannelPatchField(decodeChannelPatchUint, func(channel *models.Channel, value uint) {
		channel.Weight = value
	}),
	"priority": buildChannelPatchField(decodeChannelPatchInt, func(channel *models.Channel, value int) {
		channel.Priority = value
	}),
	"use_legacy_adaptor": buildChannelPatchField(decodeChannelPatchBool, func(channel *models.Channel, value bool) {
		channel.UseLegacyAdaptor = value
	}),
	"supported_api_types": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.SupportedAPITypes = value
	}),
	"endpoints": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Endpoints = value
	}),
	"passthrough_enabled": buildChannelPatchField(decodeChannelPatchBool, func(channel *models.Channel, value bool) {
		channel.PassthroughEnabled = value
	}),
	"system_prompt": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.SystemPrompt = value
	}),
	"system_prompt_in_input": buildChannelPatchField(decodeChannelPatchBool, func(channel *models.Channel, value bool) {
		channel.SystemPromptInInput = value
	}),
	"role_mapping": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.RoleMapping = value
	}),
	"proxy_url": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.ProxyURL = value
	}),
	"param_override": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.ParamOverride = value
	}),
	"header_override": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.HeaderOverride = value
	}),
	"tag": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Tag = value
	}),
	"remark": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Remark = value
	}),
	"setting": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Setting = value
	}),
	"organization": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.Organization = value
	}),
	"api_version": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.ApiVersion = value
	}),
	"test_model": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.TestModel = value
	}),
	"auto_ban": buildAutoBanChannelPatchField(),
	"status_code_mapping": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.StatusCodeMapping = value
	}),
	"other_settings": buildChannelPatchField(decodeChannelPatchString, func(channel *models.Channel, value string) {
		channel.OtherSettings = value
	}),
	"resilience": buildResilienceChannelPatchField(),
	"price_ratio": buildChannelPatchField(decodeChannelPatchPriceRatio, func(channel *models.Channel, value float64) {
		channel.PriceRatio = value
	}),
	"free": buildChannelPatchField(decodeChannelPatchBool, func(channel *models.Channel, value bool) {
		channel.Free = value
	}),
	"limit": buildChannelPatchField(decodeChannelPatchLimit, func(channel *models.Channel, value datatypes.JSONType[models.ChannelLimit]) {
		channel.Limit = value
	}),
	"affinity": buildChannelPatchField(decodeChannelPatchAffinity, func(channel *models.Channel, value datatypes.JSONType[models.ChannelAffinity]) {
		channel.Affinity = value
	}),
	"disable_keepalive": buildChannelPatchField(decodeChannelPatchBool, func(channel *models.Channel, value bool) {
		channel.DisableKeepalive = value
	}),
}

func ParseChannelPatch(fields map[string]any) (ChannelPatch, error) {
	patch := ChannelPatch{assignments: make(map[string]any, len(fields))}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	for _, name := range fieldNames {
		rawValue := fields[name]
		if _, readOnly := readOnlyChannelPatchFields[name]; readOnly {
			return ChannelPatch{}, fmt.Errorf("channel field %q is read-only", name)
		}
		field, ok := channelPatchFieldRegistry[name]
		if !ok {
			return ChannelPatch{}, fmt.Errorf("unknown channel field %q", name)
		}
		value, err := field.Decode(rawValue)
		if err != nil {
			return ChannelPatch{}, fmt.Errorf("invalid channel field %q: %w", name, err)
		}
		assignmentValue := cloneChannelPatchValue(value)
		if field.Assignments == nil {
			patch.assignments[name] = assignmentValue
		} else {
			field.Assignments(patch.assignments, assignmentValue)
		}
		apply := field.Apply
		applyValue := cloneChannelPatchValue(value)
		patch.applyFns = append(patch.applyFns, func(channel *models.Channel) {
			apply(channel, cloneChannelPatchValue(applyValue))
		})
		patch.revisionBump = patch.revisionBump || field.Lifecycle
	}
	if patch.revisionBump {
		patch.assignments["auto_ban_revision"] = gorm.Expr("auto_ban_revision + ?", 1)
	}
	return patch, nil
}

func (patch ChannelPatch) Apply(channel *models.Channel) error {
	if channel == nil {
		return fmt.Errorf("channel is required")
	}
	candidate := *channel
	for _, apply := range patch.applyFns {
		apply(&candidate)
	}
	if patch.revisionBump {
		candidate.AutoBanRevision++
	}
	if err := validateFinalChannel(&candidate); err != nil {
		return err
	}
	*channel = candidate
	return nil
}

func (patch ChannelPatch) Assignments() map[string]any {
	assignments := make(map[string]any, len(patch.assignments))
	for name, value := range patch.assignments {
		assignments[name] = cloneChannelPatchValue(value)
	}
	return assignments
}

func (patch ChannelPatch) Empty() bool {
	return len(patch.assignments) == 0
}

func validateFinalChannel(channel *models.Channel) error {
	if channel == nil {
		return fmt.Errorf("channel is required")
	}
	if channel.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(channel.Name) > 64 {
		return fmt.Errorf("name exceeds 64 bytes")
	}
	// behavior change: final validation observes unpatched fields without normalizing them.
	if _, err := validatePublicDisplayName(channel.PublicDisplayName); err != nil {
		return err
	}
	if err := api.ValidateStatusValue(channel.Status); err != nil {
		return err
	}
	if err := api.ValidateAutoBanValue(channel.AutoBan); err != nil {
		return err
	}
	if err := channel.Resilience.Data().Validate(); err != nil {
		return err
	}
	if err := validatePriceRatio(channel.PriceRatio); err != nil {
		return err
	}
	if err := channel.Limit.Data().Validate(); err != nil {
		return err
	}
	if err := channel.Affinity.Data().Validate(); err != nil {
		return err
	}
	return nil
}

// behavior change: mutable patch values are copied at every ownership boundary.
func cloneChannelPatchValue(value any) any {
	switch value := value.(type) {
	case datatypes.JSONType[models.ChannelResilience]:
		data := value.Data()
		data.MaxRetries = cloneChannelPatchPointer(data.MaxRetries)
		data.BackoffBaseMs = cloneChannelPatchPointer(data.BackoffBaseMs)
		data.BackoffMaxMs = cloneChannelPatchPointer(data.BackoffMaxMs)
		data.BreakerThreshold = cloneChannelPatchPointer(data.BreakerThreshold)
		data.BreakerCooldownMs = cloneChannelPatchPointer(data.BreakerCooldownMs)
		data.BreakerEnabled = cloneChannelPatchPointer(data.BreakerEnabled)
		return datatypes.NewJSONType(data)
	case datatypes.JSONType[models.ChannelAffinity]:
		data := value.Data()
		data.Enabled = cloneChannelPatchPointer(data.Enabled)
		data.TTLSec = cloneChannelPatchPointer(data.TTLSec)
		return datatypes.NewJSONType(data)
	case datatypes.JSONType[models.ChannelLimit]:
		data := value.Data()
		if data.Rules != nil {
			data.Rules = append(make([]models.LimitRule, 0, len(data.Rules)), data.Rules...)
		}
		return datatypes.NewJSONType(data)
	default:
		return value
	}
}

func cloneChannelPatchPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func buildChannelPatchField[T any](decode func(any) (T, error), apply func(*models.Channel, T)) channelPatchField {
	return channelPatchField{
		Decode: func(rawValue any) (any, error) {
			return decode(rawValue)
		},
		Apply: func(channel *models.Channel, value any) {
			apply(channel, value.(T))
		},
	}
}

func buildStatusChannelPatchField() channelPatchField {
	field := buildChannelPatchField(decodeChannelPatchStatus, func(channel *models.Channel, value int) {
		channel.Status = value
		channel.LimitState = datatypes.NewJSONType(models.ChannelDisableState{})
		channel.AutoBanState = datatypes.NewJSONType(models.ChannelDisableState{})
	})
	field.Assignments = func(assignments map[string]any, value any) {
		assignments["status"] = value
		assignments["limit_state"] = datatypes.NewJSONType(models.ChannelDisableState{})
		assignments["auto_ban_state"] = datatypes.NewJSONType(models.ChannelDisableState{})
	}
	field.Lifecycle = true
	return field
}

func buildAutoBanChannelPatchField() channelPatchField {
	field := buildChannelPatchField(decodeChannelPatchAutoBan, func(channel *models.Channel, value int) {
		channel.AutoBan = value
		channel.AutoBanState = datatypes.NewJSONType(models.ChannelDisableState{})
	})
	field.Assignments = func(assignments map[string]any, value any) {
		assignments["auto_ban"] = value
		assignments["auto_ban_state"] = datatypes.NewJSONType(models.ChannelDisableState{})
	}
	field.Lifecycle = true
	return field
}

func decodeChannelPatchAutoBan(rawValue any) (int, error) {
	if err := api.ValidateAutoBanValue(rawValue); err != nil {
		return 0, err
	}
	return decodeChannelPatchInt(rawValue)
}

func buildResilienceChannelPatchField() channelPatchField {
	field := buildChannelPatchField(decodeChannelPatchResilience, func(channel *models.Channel, value datatypes.JSONType[models.ChannelResilience]) {
		channel.Resilience = value
	})
	field.Assignments = func(assignments map[string]any, value any) {
		assignments["resilience"] = value
	}
	field.Lifecycle = true
	return field
}

func decodeChannelPatchStatus(rawValue any) (int, error) {
	if err := api.ValidateStatusValue(rawValue); err != nil {
		return 0, err
	}
	if api.StatusEqualsEnabled(rawValue) {
		return 1, nil
	}
	return 0, nil
}

func decodeChannelPatchPublicDisplayName(rawValue any) (string, error) {
	value, err := decodeChannelPatchString(rawValue)
	if err != nil {
		return "", err
	}
	return validatePublicDisplayName(value)
}

func decodeChannelPatchString(rawValue any) (string, error) {
	value, ok := rawValue.(string)
	if !ok {
		return "", fmt.Errorf("must be a string, got %T", rawValue)
	}
	return value, nil
}

func decodeChannelPatchBool(rawValue any) (bool, error) {
	value, ok := rawValue.(bool)
	if !ok {
		return false, fmt.Errorf("must be a boolean, got %T", rawValue)
	}
	return value, nil
}

func decodeChannelPatchInt(rawValue any) (int, error) {
	switch value := rawValue.(type) {
	case int:
		return value, nil
	case int8:
		return int(value), nil
	case int16:
		return int(value), nil
	case int32:
		return int(value), nil
	case int64:
		if int64(int(value)) != value {
			break
		}
		return int(value), nil
	case uint:
		if uint(int(value)) != value || int(value) < 0 {
			break
		}
		return int(value), nil
	case uint8:
		return int(value), nil
	case uint16:
		return int(value), nil
	case uint32:
		if uint64(value) > uint64(maxChannelPatchInt()) {
			break
		}
		return int(value), nil
	case uint64:
		if value > uint64(maxChannelPatchInt()) {
			break
		}
		return int(value), nil
	case float32:
		return decodeChannelPatchFloatInt(float64(value), 32)
	case float64:
		return decodeChannelPatchFloatInt(value, 64)
	default:
		return 0, fmt.Errorf("must be an integer, got %T", rawValue)
	}
	return 0, fmt.Errorf("must be an integer in range, got %v", rawValue)
}

func decodeChannelPatchUint(rawValue any) (uint, error) {
	switch value := rawValue.(type) {
	case uint:
		return value, nil
	case uint8:
		return uint(value), nil
	case uint16:
		return uint(value), nil
	case uint32:
		return uint(value), nil
	case uint64:
		if uint64(uint(value)) != value {
			break
		}
		return uint(value), nil
	case int:
		if value < 0 {
			break
		}
		return uint(value), nil
	case int8:
		if value < 0 {
			break
		}
		return uint(value), nil
	case int16:
		if value < 0 {
			break
		}
		return uint(value), nil
	case int32:
		if value < 0 {
			break
		}
		return uint(value), nil
	case int64:
		if value < 0 || uint64(value) > uint64(maxChannelPatchUint()) {
			break
		}
		return uint(value), nil
	case float32:
		return decodeChannelPatchFloatUint(float64(value), 32)
	case float64:
		return decodeChannelPatchFloatUint(value, 64)
	default:
		return 0, fmt.Errorf("must be a non-negative integer, got %T", rawValue)
	}
	return 0, fmt.Errorf("must be a non-negative integer in range, got %v", rawValue)
}

func decodeChannelPatchFloatInt(value float64, bitSize int) (int, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return 0, fmt.Errorf("must be an integer, got %v", value)
	}
	if !channelPatchFloatIntegerIsExact(value, bitSize) {
		return 0, fmt.Errorf("integer exceeds exact JSON range, got %v", value)
	}
	encoded := strconv.FormatFloat(value, 'f', -1, bitSize)
	parsed, err := strconv.ParseInt(encoded, 10, strconv.IntSize)
	if err != nil {
		return 0, fmt.Errorf("must be an integer in range, got %v", value)
	}
	return int(parsed), nil
}

func decodeChannelPatchFloatUint(value float64, bitSize int) (uint, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < 0 {
		return 0, fmt.Errorf("must be a non-negative integer, got %v", value)
	}
	if !channelPatchFloatIntegerIsExact(value, bitSize) {
		return 0, fmt.Errorf("integer exceeds exact JSON range, got %v", value)
	}
	encoded := strconv.FormatFloat(value, 'f', -1, bitSize)
	parsed, err := strconv.ParseUint(encoded, 10, strconv.IntSize)
	if err != nil {
		return 0, fmt.Errorf("must be a non-negative integer in range, got %v", value)
	}
	return uint(parsed), nil
}

func maxChannelPatchInt() int {
	return int(^uint(0) >> 1)
}

func maxChannelPatchUint() uint {
	return ^uint(0)
}

func channelPatchFloatIntegerIsExact(value float64, bitSize int) bool {
	const (
		maxExactFloat32Integer = float64(1<<24 - 1)
		maxExactFloat64Integer = float64(1<<53 - 1)
	)
	if bitSize == 32 {
		return math.Abs(value) <= maxExactFloat32Integer
	}
	return math.Abs(value) <= maxExactFloat64Integer
}

func decodeChannelPatchPriceRatio(rawValue any) (float64, error) {
	value, err := decodeChannelPatchFloat64(rawValue)
	if err != nil {
		return 0, err
	}
	if err := validatePriceRatio(value); err != nil {
		return 0, err
	}
	return value, nil
}

func decodeChannelPatchFloat64(rawValue any) (float64, error) {
	var value float64
	switch number := rawValue.(type) {
	case int:
		value = float64(number)
	case int8:
		value = float64(number)
	case int16:
		value = float64(number)
	case int32:
		value = float64(number)
	case int64:
		value = float64(number)
	case uint:
		value = float64(number)
	case uint8:
		value = float64(number)
	case uint16:
		value = float64(number)
	case uint32:
		value = float64(number)
	case uint64:
		value = float64(number)
	case float32:
		value = float64(number)
	case float64:
		value = number
	default:
		return 0, fmt.Errorf("must be a number, got %T", rawValue)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("must be a finite number")
	}
	return value, nil
}

func decodeChannelPatchResilience(rawValue any) (datatypes.JSONType[models.ChannelResilience], error) {
	return decodeChannelPatchJSON(rawValue, func(value models.ChannelResilience) error {
		return value.Validate()
	})
}

func decodeChannelPatchLimit(rawValue any) (datatypes.JSONType[models.ChannelLimit], error) {
	return decodeChannelPatchJSON(rawValue, func(value models.ChannelLimit) error {
		return value.Validate()
	})
}

func decodeChannelPatchAffinity(rawValue any) (datatypes.JSONType[models.ChannelAffinity], error) {
	return decodeChannelPatchJSON(rawValue, func(value models.ChannelAffinity) error {
		return value.Validate()
	})
}

func decodeChannelPatchJSON[T any](rawValue any, validate func(T) error) (datatypes.JSONType[T], error) {
	if rawValue == nil {
		return datatypes.JSONType[T]{}, fmt.Errorf("must be an object, got nil")
	}
	if err := rejectUnsafeChannelPatchJSONIntegers(rawValue); err != nil {
		return datatypes.JSONType[T]{}, err
	}
	encoded, err := json.Marshal(rawValue)
	if err != nil {
		return datatypes.JSONType[T]{}, fmt.Errorf("encode object: %w", err)
	}
	var value T
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return datatypes.JSONType[T]{}, fmt.Errorf("decode object: %w", err)
	}
	if err := validate(value); err != nil {
		return datatypes.JSONType[T]{}, err
	}
	return datatypes.NewJSONType(value), nil
}

func rejectUnsafeChannelPatchJSONIntegers(rawValue any) error {
	switch value := rawValue.(type) {
	case float32:
		if math.Trunc(float64(value)) == float64(value) && !channelPatchFloatIntegerIsExact(float64(value), 32) {
			return fmt.Errorf("integer exceeds exact JSON range, got %v", value)
		}
	case float64:
		if math.Trunc(value) == value && !channelPatchFloatIntegerIsExact(value, 64) {
			return fmt.Errorf("integer exceeds exact JSON range, got %v", value)
		}
	case map[string]any:
		for name, nestedValue := range value {
			if err := rejectUnsafeChannelPatchJSONIntegers(nestedValue); err != nil {
				return fmt.Errorf("field %q: %w", name, err)
			}
		}
	case []any:
		for index, nestedValue := range value {
			if err := rejectUnsafeChannelPatchJSONIntegers(nestedValue); err != nil {
				return fmt.Errorf("index %d: %w", index, err)
			}
		}
	}
	return nil
}
