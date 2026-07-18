package agent

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

var errInvalidRelayConfiguration = errors.New("invalid relay configuration")
var errInvalidPeerRouteMode = errors.New("invalid peer route mode")

func normalizePeerRouteMode(mode string, defaultEmptyMode bool) (string, error) {
	if mode == "" && defaultEmptyMode {
		return consts.PeerRouteModeDirectFirst, nil
	}
	switch mode {
	case consts.PeerRouteModeDirectFirst, consts.PeerRouteModeRelayOnly:
		return mode, nil
	default:
		return "", errInvalidPeerRouteMode
	}
}

func normalizeRelayConfiguration(mode, relayURI string, defaultEmptyMode bool) (string, string, error) {
	if mode == "" && defaultEmptyMode {
		mode = consts.RelayModeInherit
	}
	switch mode {
	case consts.RelayModeInherit, consts.RelayModeCustom, consts.RelayModeDisabled:
	default:
		return "", "", errInvalidRelayConfiguration
	}

	if relayURI != "" {
		parsed, err := tunnel.ParseRelayURI(relayURI)
		if err != nil {
			return "", "", errInvalidRelayConfiguration
		}
		relayURI = parsed.URI.String()
	}
	if mode == consts.RelayModeCustom && relayURI == "" {
		return "", "", errInvalidRelayConfiguration
	}

	return mode, relayURI, nil
}

func mergeAgentPatch(current models.Agent, patch AgentPatch) (models.Agent, map[string]any, error) {
	merged := current
	updates := make(map[string]any, 8)

	if patch.Name != nil {
		merged.Name = *patch.Name
		updates["name"] = *patch.Name
	}
	if patch.Status != nil {
		if err := api.ValidateStatusValue(*patch.Status); err != nil {
			return models.Agent{}, nil, err
		}
		merged.Status = *patch.Status
		updates["status"] = *patch.Status
	}
	if patch.Tags != nil {
		merged.Tags = *patch.Tags
		updates["tags"] = *patch.Tags
	}
	if patch.HTTPAddresses != nil {
		merged.HTTPAddresses = *patch.HTTPAddresses
		updates["http_addresses"] = *patch.HTTPAddresses
	}
	if patch.ProxyURL != nil {
		merged.ProxyURL = *patch.ProxyURL
		updates["proxy_url"] = *patch.ProxyURL
	}
	if patch.RelayMode != nil {
		merged.RelayMode = *patch.RelayMode
	}
	if patch.RelayURI != nil {
		merged.RelayURI = *patch.RelayURI
	}
	if patch.PeerRouteMode != nil {
		mode, err := normalizePeerRouteMode(*patch.PeerRouteMode, false)
		if err != nil {
			return models.Agent{}, nil, err
		}
		merged.PeerRouteMode = mode
		updates["peer_route_mode"] = mode
	}

	mode, relayURI, err := normalizeRelayConfiguration(merged.RelayMode, merged.RelayURI, false)
	if err != nil {
		return models.Agent{}, nil, err
	}
	if patch.RelayMode != nil {
		merged.RelayMode = mode
		updates["relay_mode"] = mode
	}
	if patch.RelayURI != nil {
		merged.RelayURI = relayURI
		updates["relay_uri"] = relayURI
	}

	return merged, updates, nil
}
