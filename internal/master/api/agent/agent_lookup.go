package agent

import (
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

func findAgentByID(c *app.Context, rawID string) (models.Agent, error) {
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return models.Agent{}, apiErr
	}
	id, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil {
		return models.Agent{}, api.NotFoundError("agent not found")
	}
	agent, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Agent().GetByID(uint(id))
	if err == nil {
		return *agent, nil
	}
	// behavior change: request cancellation and database failures must not be misclassified as not found.
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return models.Agent{}, apiErr
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Agent{}, api.NotFoundError("agent not found")
	}
	return models.Agent{}, api.InternalError("get agent failed", err)
}
