package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/datatypes"
)

type modelUpdateFieldDecoder func(any) (any, error)

var modelUpdateFieldRegistry = map[string]modelUpdateFieldDecoder{
	"model_name":        decodeModelUpdateString,
	"input_price":       decodeModelUpdatePriceNumber,
	"output_price":      decodeModelUpdatePriceNumber,
	"cache_read_price":  decodeModelUpdatePriceNumber,
	"cache_write_price": decodeModelUpdatePriceNumber,
	"status":            decodeModelUpdateStatus,
	"metadata_override": decodeModelMetadataOverride,
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.ModelConfig, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.ModelConfig().GetByID(uint(id))
	if err != nil {
		return models.ModelConfig{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates, err := buildModelUpdates(req.Fields)
	if err != nil {
		return models.ModelConfig{}, api.BadRequestError(err.Error(), err)
	}
	if err := validateModelPriceBuckets(modelPriceBucketsAfterUpdates(*existing, updates)); err != nil {
		return models.ModelConfig{}, api.BadRequestError(err.Error(), err)
	}

	if err := m.ModelConfig().Update(uint(id), updates); err != nil {
		return models.ModelConfig{}, api.InternalError("update model failed", err)
	}

	mc, err := q.ModelConfig().GetByID(uint(id))
	if err != nil {
		return models.ModelConfig{}, api.InternalError("update model failed", err)
	}

	if err := events.PublishModelUpdate(context.Background(), c.GetBus(), *mc); err != nil {
		return models.ModelConfig{}, api.InternalError("publish model.update failed", err)
	}
	return *mc, nil
}

func buildModelUpdates(fields map[string]any) (map[string]any, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("fields cannot be empty")
	}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)

	updates := make(map[string]any, len(fields))
	for _, name := range fieldNames {
		decode, allowed := modelUpdateFieldRegistry[name]
		if !allowed {
			return nil, fmt.Errorf("field %s is not writable", name)
		}
		value, err := decode(fields[name])
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", name, err)
		}
		updates[name] = value
	}
	return updates, nil
}

func decodeModelUpdateString(value any) (any, error) {
	decoded, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("must be a string")
	}
	return decoded, nil
}

func decodeModelUpdateNumber(value any) (any, error) {
	decoded, ok := value.(float64)
	if !ok {
		return nil, fmt.Errorf("must be a JSON number")
	}
	if math.IsNaN(decoded) || math.IsInf(decoded, 0) {
		return nil, fmt.Errorf("must be finite")
	}
	return decoded, nil
}

func decodeModelUpdatePriceNumber(value any) (any, error) {
	decoded, ok := value.(float64)
	if !ok {
		return nil, fmt.Errorf("must be a JSON number")
	}
	return decoded, nil
}

func decodeModelUpdateStatus(value any) (any, error) {
	decoded, err := decodeModelUpdateNumber(value)
	if err != nil {
		return nil, err
	}
	status := decoded.(float64)
	if status != float64(consts.StatusDisabled) && status != float64(consts.StatusEnabled) {
		return nil, fmt.Errorf("must be %d or %d", consts.StatusDisabled, consts.StatusEnabled)
	}
	return int(status), nil
}

func decodeModelMetadataOverride(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var override *models.ModelMetadataOverride
	if err := decoder.Decode(&override); err != nil {
		return nil, err
	}
	if override == nil {
		return nil, fmt.Errorf("must be an object")
	}
	return datatypes.NewJSONType(*override), nil
}
