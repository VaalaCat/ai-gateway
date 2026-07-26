package master

import (
	"context"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type masterAgentTransportPolicyFinder struct{ application dao.AppProvider }

func (f masterAgentTransportPolicyFinder) FindAgentTransportPolicies(ctx context.Context, agentIDs []string) (map[string]models.Agent, error) {
	unique := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		if agentID = strings.TrimSpace(agentID); agentID != "" {
			unique[agentID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(unique))
	for agentID := range unique {
		ids = append(ids, agentID)
	}
	if len(ids) == 0 {
		return map[string]models.Agent{}, nil
	}
	sort.Strings(ids)
	agents, err := dao.NewAdminQuery(dao.NewContextWithContext(f.application, ctx)).Agent().ListByAgentIDs(ids)
	if err != nil {
		return nil, err
	}
	policies := make(map[string]models.Agent, len(agents))
	for _, agent := range agents {
		policies[agent.AgentID] = agent
	}
	return policies, nil
}
