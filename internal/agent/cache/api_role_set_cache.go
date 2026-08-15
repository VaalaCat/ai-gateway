package cache

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func (s *Store) registerAPIRoleSetInvalidationHandlers() {
	s.syncHandlers = map[string]func(string, []byte){
		events.EntityUserAPIRoleSet:  s.invalidateUserAPIRoleSet,
		events.EntityTokenAPIRoleSet: s.invalidateTokenAPIRoleSet,
	}
}

func (s *Store) invalidateUserAPIRoleSet(action string, data []byte) {
	if action != events.ActionInvalidate {
		return
	}
	var payload protocol.APIRoleSetInvalidate
	if json.Unmarshal(data, &payload) == nil {
		s.DeleteUserAPIRoleSet(payload.PrincipalID)
	}
}

func (s *Store) invalidateTokenAPIRoleSet(action string, data []byte) {
	if action != events.ActionInvalidate {
		return
	}
	var payload protocol.APIRoleSetInvalidate
	if json.Unmarshal(data, &payload) == nil {
		s.DeleteTokenAPIRoleSet(payload.PrincipalID)
	}
}
