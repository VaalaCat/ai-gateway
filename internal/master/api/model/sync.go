package model

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/model/pricing"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
	"gorm.io/datatypes"
)

type modelReconciliationPlan struct {
	Creates []models.ModelConfig
	Deletes []models.ModelConfig
}

type modelMetadataUpdate struct {
	Config models.ModelConfig
	Synced models.ModelMetadata
}

func (h *Handler) Sync(c *app.Context, _ api.EmptyRequest) (SyncResponse, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())

	response, err := reconcileModelsFromChannels(c, daoCtx)
	if err != nil {
		return SyncResponse{}, err
	}

	q := dao.NewAdminQuery(daoCtx)
	metadata, err := h.fetchModelsDevMetadata(c, q)
	if err != nil {
		response.MetadataSourceError = err.Error()
		return response, nil
	}
	response.MetadataUpdated, err = writeSyncedModelMetadata(c, daoCtx, metadata)
	if err != nil {
		return SyncResponse{}, err
	}
	return response, nil
}

func reconcileModelsFromChannels(
	c *app.Context,
	daoCtx dao.Context,
) (SyncResponse, error) {
	plan, err := buildModelReconciliationPlan(dao.NewAdminQuery(daoCtx))
	if err != nil {
		return SyncResponse{}, api.InternalError("sync models failed", err)
	}
	if err := applyModelReconciliation(daoCtx, &plan); err != nil {
		return SyncResponse{}, api.InternalError("sync models failed", err)
	}
	if err := publishModelReconciliation(c, plan); err != nil {
		return SyncResponse{}, err
	}
	return SyncResponse{Created: len(plan.Creates), Removed: len(plan.Deletes)}, nil
}

func buildModelReconciliationPlan(q dao.AdminQuery) (modelReconciliationPlan, error) {
	channels, err := q.Channel().ListAll()
	if err != nil {
		return modelReconciliationPlan{}, err
	}

	desiredNames := make(map[string]struct{})
	for _, ch := range channels {
		for _, mn := range strings.Split(ch.Models, ",") {
			mn = strings.TrimSpace(mn)
			if mn != "" {
				desiredNames[mn] = struct{}{}
			}
		}
	}

	configs, err := q.ModelConfig().ListAll()
	if err != nil {
		return modelReconciliationPlan{}, err
	}
	existingNames := make(map[string]struct{}, len(configs))
	plan := modelReconciliationPlan{}
	for _, config := range configs {
		existingNames[config.ModelName] = struct{}{}
		if _, retained := desiredNames[config.ModelName]; !retained {
			plan.Deletes = append(plan.Deletes, config)
		}
	}
	for modelName := range desiredNames {
		if _, exists := existingNames[modelName]; exists {
			continue
		}
		plan.Creates = append(plan.Creates, models.ModelConfig{
			ModelName: modelName,
			Status:    consts.StatusEnabled,
		})
	}
	sort.Slice(plan.Creates, func(left, right int) bool {
		return plan.Creates[left].ModelName < plan.Creates[right].ModelName
	})
	sort.Slice(plan.Deletes, func(left, right int) bool {
		return plan.Deletes[left].ModelName < plan.Deletes[right].ModelName
	})
	return plan, nil
}

func applyModelReconciliation(daoCtx dao.Context, plan *modelReconciliationPlan) error {
	return dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		mutation := dao.NewAdminMutation(txCtx).ModelConfig()
		for index := range plan.Creates {
			if err := mutation.Create(&plan.Creates[index]); err != nil {
				return fmt.Errorf("create model %q: %w", plan.Creates[index].ModelName, err)
			}
		}
		for _, config := range plan.Deletes {
			if err := mutation.Delete(config.ID); err != nil {
				return fmt.Errorf("delete model %q: %w", config.ModelName, err)
			}
		}
		return nil
	})
}

func publishModelReconciliation(c *app.Context, plan modelReconciliationPlan) error {
	for _, config := range plan.Creates {
		if err := events.PublishModelCreate(context.Background(), c.GetBus(), config); err != nil {
			return api.InternalError("publish model.create failed", err)
		}
	}
	for _, config := range plan.Deletes {
		if err := events.PublishModelDelete(context.Background(), c.GetBus(), config); err != nil {
			return api.InternalError("publish model.delete failed", err)
		}
	}
	return nil
}

func (h *Handler) fetchModelsDevMetadata(c *app.Context, q dao.AdminQuery) (map[string]models.ModelMetadata, error) {
	dbProxy := ""
	if setting, found, err := q.Setting().Lookup("proxy_url"); err == nil && found {
		dbProxy = setting.Value
	}
	configProxy := ""
	if c.Settings != nil {
		configProxy = c.Settings.Master.ProxyURL
	}
	proxyURL := httputil.ResolveProxyURL(dbProxy, configProxy)
	fetch := h.FetchModelsDev
	if fetch == nil {
		fetch = pricing.Fetch
	}
	data, err := fetch(pricing.ModelsDevURL, proxyURL)
	if err != nil {
		return nil, err
	}
	if err := pricing.ValidateModelsDevMetadata(data); err != nil {
		return nil, err
	}
	return pricing.ConvertModelsDevMetadata(data), nil
}

func writeSyncedModelMetadata(
	c *app.Context,
	daoCtx dao.Context,
	metadata map[string]models.ModelMetadata,
) (int, error) {
	updates, err := buildModelMetadataUpdates(dao.NewAdminQuery(daoCtx), metadata)
	if err != nil {
		return 0, api.InternalError("sync model metadata failed", err)
	}
	committed, err := applyModelMetadataUpdates(daoCtx, updates)
	if err != nil {
		return 0, api.InternalError("sync model metadata failed", err)
	}
	for _, config := range committed {
		if err := events.PublishModelUpdate(context.Background(), c.GetBus(), config); err != nil {
			return 0, api.InternalError("publish model.update failed", err)
		}
	}
	return len(committed), nil
}

func buildModelMetadataUpdates(
	q dao.AdminQuery,
	metadata map[string]models.ModelMetadata,
) ([]modelMetadataUpdate, error) {
	configs, err := q.ModelConfig().ListAll()
	if err != nil {
		return nil, err
	}

	updates := make([]modelMetadataUpdate, 0, len(configs))
	for _, config := range configs {
		synced, found := metadata[config.ModelName]
		if !found {
			continue
		}
		updates = append(updates, modelMetadataUpdate{Config: config, Synced: synced})
	}
	sort.Slice(updates, func(left, right int) bool {
		return updates[left].Config.ModelName < updates[right].Config.ModelName
	})
	return updates, nil
}

func applyModelMetadataUpdates(
	daoCtx dao.Context,
	updates []modelMetadataUpdate,
) ([]models.ModelConfig, error) {
	if len(updates) == 0 {
		return nil, nil
	}
	committed := make([]models.ModelConfig, 0, len(updates))
	err := dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		mutation := dao.NewAdminMutation(txCtx).ModelConfig()
		for _, update := range updates {
			if err := mutation.Update(update.Config.ID, map[string]any{
				"synced_metadata": datatypes.NewJSONType(update.Synced),
			}); err != nil {
				return fmt.Errorf("update metadata for model %q: %w", update.Config.ModelName, err)
			}
		}

		query := dao.NewAdminQuery(txCtx).ModelConfig()
		for _, update := range updates {
			config, err := query.GetByID(update.Config.ID)
			if err != nil {
				return fmt.Errorf("read updated model %q: %w", update.Config.ModelName, err)
			}
			committed = append(committed, *config)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return committed, nil
}
