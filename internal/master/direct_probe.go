package master

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	directProbeCallTimeout = 30 * time.Second
	relayProbeCallTimeout  = 30 * time.Second
)

type sessionProbeCaller interface {
	CallSessionContext(ctx context.Context, agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error)
}

type masterProbeCaller struct{ control sessionProbeCaller }

func (c masterProbeCaller) CallDirectProbe(ctx context.Context, sourceID string, sourceGeneration uint64, target protocol.DirectProbeTarget) (protocol.DirectProbeResult, error) {
	if ctx == nil {
		return protocol.DirectProbeResult{}, fmt.Errorf("master direct probe: nil context")
	}
	if c.control == nil {
		return protocol.DirectProbeResult{}, fmt.Errorf("master direct probe: control hub is required")
	}
	raw, err := c.control.CallSessionContext(ctx, sourceID, sourceGeneration, consts.RPCAgentDirectProbe, target, directProbeCallTimeout)
	if err != nil {
		return protocol.DirectProbeResult{}, err
	}
	var result protocol.DirectProbeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.DirectProbeResult{}, fmt.Errorf("master direct probe response: %w", err)
	}
	return result, nil
}

func (c masterProbeCaller) CallRelayProbe(ctx context.Context, sourceID string, sourceGeneration uint64, target protocol.RelayProbeTarget) (protocol.RelayProbeResult, error) {
	if ctx == nil {
		return protocol.RelayProbeResult{}, fmt.Errorf("master relay probe: nil context")
	}
	if c.control == nil {
		return protocol.RelayProbeResult{}, fmt.Errorf("master relay probe: control hub is required")
	}
	raw, err := c.control.CallSessionContext(ctx, sourceID, sourceGeneration, consts.RPCAgentRelayProbe, target, relayProbeCallTimeout)
	if err != nil {
		return protocol.RelayProbeResult{}, err
	}
	var result protocol.RelayProbeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.RelayProbeResult{}, fmt.Errorf("master relay probe response: %w", err)
	}
	return result, nil
}

type probeTargetControl interface {
	GetAgentAddresses(agentID string, dbHTTPAddrs string) []agentproxy.Address
	GetControlSession(agentID string) (connectivity.ControlSessionFact, bool)
}

type masterProbeTargetFinder struct {
	application app.Application
	control     probeTargetControl
	globalProxy string
}

func (f masterProbeTargetFinder) FindEnabledProbeTargets(ctx context.Context, targetAgentIDs []string) ([]connectivity.ProbeTarget, error) {
	if ctx == nil {
		return nil, fmt.Errorf("master probe targets: nil context")
	}
	if f.application == nil {
		return nil, fmt.Errorf("master probe targets: application is required")
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(f.application, ctx)).Agent()
	var agents []models.Agent
	var err error
	if targetAgentIDs == nil {
		agents, err = query.ListActive("")
	} else if len(targetAgentIDs) == 0 {
		return make([]connectivity.ProbeTarget, 0), nil
	} else {
		agents, err = query.ListByAgentIDs(targetAgentIDs)
	}
	if err != nil {
		return nil, err
	}
	targets := make([]connectivity.ProbeTarget, 0, len(agents))
	for _, agent := range agents {
		if agent.Status != consts.StatusEnabled {
			continue
		}
		addresses := agentproxy.ParseAddresses(agent.HTTPAddresses)
		generation := uint64(0)
		capabilities := make([]string, 0)
		if f.control != nil {
			addresses = f.control.GetAgentAddresses(agent.AgentID, agent.HTTPAddresses)
			if fact, ok := f.control.GetControlSession(agent.AgentID); ok {
				generation = fact.Generation
			}
			if source, ok := f.control.(interface{ Capabilities(string) []string }); ok {
				capabilities = source.Capabilities(agent.AgentID)
			}
		}
		targets = append(targets, connectivity.ProbeTarget{
			AgentID: agent.AgentID, Name: agent.Name, Tags: splitProbeTags(agent.Tags),
			Addresses: addresses, EffectiveProxy: agentproxy.ResolveProxyURL(agent.ProxyURL, f.globalProxy),
			ControlGeneration: generation, Capabilities: capabilities, PeerRouteMode: agent.PeerRouteMode,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].AgentID < targets[j].AgentID })
	return targets, nil
}

func (f masterProbeTargetFinder) FindEnabledProbeSource(ctx context.Context, sourceAgentID string) (connectivity.ProbeTarget, error) {
	if strings.TrimSpace(sourceAgentID) == "" {
		return connectivity.ProbeTarget{}, fmt.Errorf("master probe source: agent ID is required")
	}
	targets, err := f.FindEnabledProbeTargets(ctx, []string{sourceAgentID})
	if err != nil {
		return connectivity.ProbeTarget{}, err
	}
	if len(targets) != 1 || targets[0].AgentID != sourceAgentID {
		return connectivity.ProbeTarget{}, fmt.Errorf("master probe source: enabled agent %s not found", sourceAgentID)
	}
	return targets[0], nil
}

func splitProbeTags(raw string) []string {
	seen := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
