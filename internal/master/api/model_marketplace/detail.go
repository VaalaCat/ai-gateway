package model_marketplace

import (
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type UsageAvailability string

const (
	UsageAvailable     UsageAvailability = "available"
	UsageUnavailable   UsageAvailability = "unavailable"
	UsageNotApplicable UsageAvailability = "not_applicable"
)

type DetailRequest struct {
	TokenID  uint   `form:"token_id"`
	Model    string `form:"model"`
	Window   string `form:"window"`
	OfferRef string `form:"offer_ref"`
}

type UserModelDetailResponse struct {
	SelectedToken UserSelectedTokenDTO    `json:"selected_token"`
	Window        UsageWindow             `json:"window"`
	UsageStatus   UsageAvailability       `json:"usage_status"`
	Model         UserMarketplaceModelDTO `json:"model"`
}

func (h *Handler) Detail(c *app.Context, req DetailRequest) (UserModelDetailResponse, error) {
	viewer, err := h.gate.RequireUser(c, req.TokenID)
	if err != nil {
		return UserModelDetailResponse{}, err
	}
	window, err := normalizedUsageWindow(req.Window)
	if err != nil {
		return UserModelDetailResponse{}, err
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return UserModelDetailResponse{}, api.BadRequestError("marketplace model is required", nil)
	}
	composed, err := h.compose(c.RequestContext(), viewer, window, false)
	if err != nil {
		return UserModelDetailResponse{}, err
	}
	for index := range composed.real {
		if composed.real[index].model.ModelName != modelName {
			continue
		}
		if !composed.real[index].selectOffer(req.OfferRef, h.encoder) {
			return UserModelDetailResponse{}, api.NotFoundError(consts.ErrNotFound)
		}
		usageStatus := UsageAvailable
		if h.usage == nil {
			usageStatus = UsageUnavailable
		} else {
			references, usageErr := h.usage.Find(c.RequestContext(), viewer, composedOffers(composed.real[index]), window)
			if usageErr != nil {
				usageStatus = UsageUnavailable
			} else {
				composed.real[index].attachUsage(references)
			}
		}
		return UserModelDetailResponse{
			SelectedToken: mapUserSelectedToken(viewer), Window: window, UsageStatus: usageStatus,
			Model: mapUserRealModel(composed.real[index]),
		}, nil
	}
	if strings.TrimSpace(req.OfferRef) != "" {
		return UserModelDetailResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	for _, routingModel := range composed.routing {
		if routingModel.ModelName == modelName {
			return UserModelDetailResponse{
				SelectedToken: mapUserSelectedToken(viewer), Window: window, UsageStatus: UsageNotApplicable,
				Model: mapUserRoutingModel(routingModel),
			}, nil
		}
	}
	return UserModelDetailResponse{}, api.NotFoundError(consts.ErrNotFound)
}

func normalizedUsageWindow(raw string) (UsageWindow, error) {
	window := UsageWindow(strings.TrimSpace(raw))
	if window == "" {
		return UsageWindow24Hours, nil
	}
	switch window {
	case UsageWindow24Hours, UsageWindow7Days, UsageWindow30Days:
		return window, nil
	default:
		return "", api.BadRequestError("invalid marketplace window", nil)
	}
}
