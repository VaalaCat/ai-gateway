package billing

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	mbilling "github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// RebuildJobView is the wire shape for rebuild job GETs. Built from
// RebuildJob.Snapshot() so external reads are race-free.
type RebuildJobView struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	DoneSlices   int64  `json:"done_slices"`
	TotalSlices  int64  `json:"total_slices"`
	ReplayedLogs int64  `json:"replayed_logs"`
	StartedAt    int64  `json:"started_at"`
	FinishedAt   int64  `json:"finished_at,omitempty"`
	Error        string `json:"error,omitempty"`
}

// GetRebuildJobRequest is the URI-bound shape for GET /billing/rebuild/jobs/:id.
type GetRebuildJobRequest struct {
	ID string `uri:"id" binding:"required"`
}

// GetRebuildJob returns the current state of the named rebuild job. 404 if
// unknown (never submitted, gc'd after retain expiry, or lost to master
// restart — clients should retrigger).
func (h *Handler) GetRebuildJob(c *app.Context, req GetRebuildJobRequest) (RebuildJobView, error) {
	if h.Runner == nil {
		return RebuildJobView{}, api.InternalError("rebuild runner not configured", nil)
	}
	job, ok := h.Runner.Get(req.ID)
	if !ok {
		return RebuildJobView{}, api.NotFoundError("rebuild job not found")
	}
	return jobToView(job), nil
}

// ListRebuildJobsResponse holds all currently-tracked rebuild jobs.
type ListRebuildJobsResponse struct {
	Jobs []RebuildJobView `json:"jobs"`
}

// ListRebuildJobs returns running + retained terminal jobs (in arbitrary order
// — the runner stores them in a map).
func (h *Handler) ListRebuildJobs(c *app.Context, _ api.EmptyRequest) (ListRebuildJobsResponse, error) {
	if h.Runner == nil {
		return ListRebuildJobsResponse{}, api.InternalError("rebuild runner not configured", nil)
	}
	jobs := h.Runner.List()
	views := make([]RebuildJobView, 0, len(jobs))
	for _, j := range jobs {
		views = append(views, jobToView(j))
	}
	return ListRebuildJobsResponse{Jobs: views}, nil
}

func jobToView(j *mbilling.RebuildJob) RebuildJobView {
	snap := j.Snapshot()
	return RebuildJobView{
		ID:           snap.ID,
		Status:       string(snap.Status),
		DoneSlices:   snap.DoneSlices,
		TotalSlices:  snap.TotalSlices,
		ReplayedLogs: snap.ReplayedLogs,
		StartedAt:    snap.StartedAt,
		FinishedAt:   snap.FinishedAt,
		Error:        snap.Error,
	}
}
