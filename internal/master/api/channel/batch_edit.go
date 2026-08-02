package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

const maxChannelBatchEditIDs = 500

type ChannelBatchEditor struct {
	Bus    app.EventBus
	Logger *zap.Logger
}

func (h *Handler) BatchEdit(c *app.Context, req BatchEditRequest) (BatchEditResponse, error) {
	ids, err := normalizeBatchEditRequestIDs(req.IDs)
	if err != nil {
		return BatchEditResponse{}, api.BadRequestError(err.Error(), err)
	}
	patch, err := ParseChannelPatch(req.Fields)
	if err != nil {
		return BatchEditResponse{}, api.BadRequestError(err.Error(), err)
	}
	if patch.Empty() {
		err := newChannelBatchInputError("fields cannot be empty", nil)
		return BatchEditResponse{}, api.BadRequestError(err.Error(), err)
	}

	editor := ChannelBatchEditor{Bus: c.GetBus(), Logger: c.Logger}
	updated, err := editor.Edit(
		c.RequestContext(),
		dao.NewContextWithContext(c.App, c.RequestContext()),
		ids,
		patch,
	)
	if err != nil {
		return BatchEditResponse{}, channelBatchEditAPIError(err)
	}
	updatedIDs := make([]uint, len(updated))
	for index := range updated {
		updatedIDs[index] = updated[index].ID
	}
	return BatchEditResponse{UpdatedCount: len(updatedIDs), UpdatedIDs: updatedIDs}, nil
}

func (e ChannelBatchEditor) Edit(
	ctx context.Context,
	daoCtx dao.Context,
	ids []uint,
	patch ChannelPatch,
) ([]models.Channel, error) {
	normalizedIDs, err := normalizeChannelBatchIDs(ids)
	if err != nil {
		return nil, err
	}
	if patch.Empty() {
		return nil, newChannelBatchInputError("fields cannot be empty", nil)
	}

	var updated []models.Channel
	err = dao.RunInTx(daoCtx, func(txCtx dao.Context) error {
		var err error
		updated, err = prepareChannelBatchUpdate(txCtx, normalizedIDs, patch)
		return err
	})
	if err != nil {
		return nil, err
	}
	e.publishUpdates(ctx, updated)
	return updated, nil
}

func prepareChannelBatchUpdate(txCtx dao.Context, ids []uint, patch ChannelPatch) ([]models.Channel, error) {
	query := dao.NewAdminQuery(txCtx).Channel()
	channels, err := query.ListByIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("list channels for batch edit: %w", err)
	}
	if len(channels) != len(ids) {
		return nil, &channelBatchMissingError{requested: len(ids), found: len(channels)}
	}

	candidates := append([]models.Channel(nil), channels...)
	for index := range candidates {
		if err := patch.Apply(&candidates[index]); err != nil {
			return nil, newChannelBatchFinalStateError(
				fmt.Sprintf("channel %d has invalid final state", candidates[index].ID),
				err,
			)
		}
	}
	rowsAffected, err := dao.NewAdminMutation(txCtx).Channel().UpdateByIDs(ids, patch.Assignments())
	if err != nil {
		return nil, fmt.Errorf("update channels for batch edit: %w", err)
	}
	if rowsAffected != int64(len(ids)) {
		return nil, fmt.Errorf(
			"batch edit rows affected mismatch: got %d, want %d",
			rowsAffected,
			len(ids),
		)
	}
	updated, err := query.ListByIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("list updated channels after batch edit: %w", err)
	}
	if len(updated) != len(ids) {
		return nil, fmt.Errorf(
			"batch edit updated row count mismatch: got %d, want %d",
			len(updated),
			len(ids),
		)
	}
	return updated, nil
}

func (e ChannelBatchEditor) publishUpdates(ctx context.Context, channels []models.Channel) {
	for index := range channels {
		channel := channels[index]
		if err := events.PublishChannelUpdate(ctx, e.Bus, channel); err != nil && e.Logger != nil {
			e.Logger.Warn(
				"publish channel.update failed after commit",
				zap.Uint("channel_id", channel.ID),
				zap.Error(err),
			)
		}
	}
}

func normalizeBatchEditRequestIDs(rawIDs []int64) ([]uint, error) {
	ids := make([]uint, len(rawIDs))
	for index, rawID := range rawIDs {
		if rawID <= 0 {
			return nil, newChannelBatchInputError(
				fmt.Sprintf("ids[%d] must be a positive integer", index),
				nil,
			)
		}
		if uint64(rawID) > uint64(^uint(0)) {
			return nil, newChannelBatchInputError(
				fmt.Sprintf("ids[%d] exceeds the supported integer range", index),
				nil,
			)
		}
		ids[index] = uint(rawID)
	}
	return normalizeChannelBatchIDs(ids)
}

func normalizeChannelBatchIDs(ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return nil, newChannelBatchInputError("ids cannot be empty", nil)
	}
	unique := make(map[uint]struct{}, len(ids))
	for index, id := range ids {
		if id == 0 {
			return nil, newChannelBatchInputError(
				fmt.Sprintf("ids[%d] must be a positive integer", index),
				nil,
			)
		}
		unique[id] = struct{}{}
	}
	if len(unique) > maxChannelBatchEditIDs {
		return nil, newChannelBatchInputError(
			fmt.Sprintf("ids cannot contain more than %d unique values", maxChannelBatchEditIDs),
			nil,
		)
	}
	normalized := make([]uint, 0, len(unique))
	for id := range unique {
		normalized = append(normalized, id)
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left] < normalized[right]
	})
	return normalized, nil
}

func channelBatchEditAPIError(err error) error {
	var finalStateErr *channelBatchFinalStateError
	if errors.As(err, &finalStateErr) {
		return api.ErrorWithCode(
			http.StatusUnprocessableEntity,
			"channel_batch_final_state_invalid",
			err.Error(),
			nil,
		)
	}
	var inputErr *channelBatchInputError
	if errors.As(err, &inputErr) {
		return api.BadRequestError(err.Error(), err)
	}
	var missingErr *channelBatchMissingError
	if errors.As(err, &missingErr) {
		return api.NotFoundError(err.Error())
	}
	return api.InternalError("batch edit channels failed", err)
}

type channelBatchFinalStateError struct {
	message string
	cause   error
}

func newChannelBatchFinalStateError(message string, cause error) error {
	return &channelBatchFinalStateError{message: message, cause: cause}
}

func (e *channelBatchFinalStateError) Error() string {
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

func (e *channelBatchFinalStateError) Unwrap() error {
	return e.cause
}

type channelBatchInputError struct {
	message string
	cause   error
}

func newChannelBatchInputError(message string, cause error) error {
	return &channelBatchInputError{message: message, cause: cause}
}

func (e *channelBatchInputError) Error() string {
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

func (e *channelBatchInputError) Unwrap() error {
	return e.cause
}

type channelBatchMissingError struct {
	requested int
	found     int
}

func (e *channelBatchMissingError) Error() string {
	return fmt.Sprintf("one or more channels not found: requested %d, found %d", e.requested, e.found)
}
