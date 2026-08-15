package token

import (
	"fmt"
	"math"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const maxExactlyRepresentableJSONInteger = float64(1<<53 - 1)

func parseAPIRoleUpdate(fields map[string]any) (*APIRoleUpdateRequest, error) {
	rawMode, hasMode := fields["api_role_mode"]
	rawRoleIDs, hasRoleIDs := fields["api_role_ids"]
	if !hasMode && !hasRoleIDs {
		return nil, nil
	}
	if !hasMode || !hasRoleIDs {
		return nil, fmt.Errorf("api_role_mode and api_role_ids must be provided together")
	}
	modeText, ok := rawMode.(string)
	if !ok {
		return nil, fmt.Errorf("api_role_mode must be a string")
	}
	mode := models.APIRoleMode(modeText)
	if mode != models.APIRoleModeInherit && mode != models.APIRoleModeExplicit {
		return nil, fmt.Errorf("api_role_mode must be inherit or explicit")
	}
	roleIDs, err := normalizeAPIRoleIDs(rawRoleIDs)
	if err != nil {
		return nil, err
	}
	if mode == models.APIRoleModeInherit && len(roleIDs) != 0 {
		return nil, fmt.Errorf("inherit api_role_mode requires empty api_role_ids")
	}
	return &APIRoleUpdateRequest{Mode: mode, RoleIDs: roleIDs}, nil
}

func normalizeAPIRoleIDs(raw any) ([]uint, error) {
	if raw == nil {
		return nil, fmt.Errorf("api_role_ids must be an array of positive integers")
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("api_role_ids must be an array of positive integers")
	}
	ids := make([]uint, 0, len(values))
	for _, value := range values {
		number, ok := value.(float64)
		if !ok || number <= 0 || math.Trunc(number) != number || number > maxExactlyRepresentableJSONInteger {
			return nil, fmt.Errorf("api_role_ids contains invalid role ID: %v", value)
		}
		ids = append(ids, uint(number))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := ids[:0]
	for _, id := range ids {
		if len(result) == 0 || result[len(result)-1] != id {
			result = append(result, id)
		}
	}
	return result, nil
}
