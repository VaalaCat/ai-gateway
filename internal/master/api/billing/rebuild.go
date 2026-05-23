package billing

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type RebuildRequest struct {
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	Targets   []string `json:"targets,omitempty"`
}

// RebuildResponse is the async submit ack. Clients should poll
// GET /admin/billing/rebuild/jobs/:id with the returned job_id for progress
// and final status.
type RebuildResponse struct {
	JobID       string `json:"job_id"`
	TotalSlices int64  `json:"total_slices"`
}

// Rebuild submits an async rebuild job and returns immediately. Runner.Submit
// validates the date range and computes total slices (24 per day).
func (h *Handler) Rebuild(c *app.Context, req RebuildRequest) (RebuildResponse, error) {
	if h.Runner == nil {
		return RebuildResponse{}, api.InternalError("rebuild runner not configured", nil)
	}
	job, err := h.Runner.Submit(dao.BillingRebuildFilter{
		StartDate: req.StartDate,
		EndDate:   req.EndDate,
		Targets:   req.Targets,
	})
	if err != nil {
		if errors.Is(err, dao.ErrInvalidRebuildTarget) {
			return RebuildResponse{}, api.BadRequestError(err.Error(), nil)
		}
		return RebuildResponse{}, api.BadRequestError(err.Error(), nil)
	}
	return RebuildResponse{
		JobID:       job.ID,
		TotalSlices: job.TotalSlices,
	}, nil
}
