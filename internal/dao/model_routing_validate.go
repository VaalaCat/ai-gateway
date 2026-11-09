package dao

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const (
	routingMaxNameLen = 128
	routingMaxMembers = 32
	routingMaxDepth   = 5

	ErrCodeNameRequired       = "name_required"
	ErrCodeNameTooLong        = "name_too_long"
	ErrCodeNameContainsComma  = "name_contains_comma"
	ErrCodeMembersInvalidJSON = "members_invalid_json"
	ErrCodeMembersEmpty       = "members_empty"
	ErrCodeMembersTooMany     = "members_too_many"
	ErrCodeRefEmpty           = "ref_empty"
	ErrCodeInvalidRef         = "invalid_ref"
	ErrCodeInvalidMemberKind  = "invalid_member_kind"
	ErrCodeCycleDetected      = "cycle_detected"
	ErrCodeMaxDepth           = "max_depth"
	ErrCodeReferencedBy       = "referenced_by"
	ErrCodeInvalidScope       = "invalid_scope"
	ErrCodeInvalidScopeOwner  = "invalid_scope_owner"
	ErrCodeTokenNotFound      = "token_not_found"
	ErrCodeDBError            = "db_error"
	ErrCodeNotFound           = "not_found"
)

// NameProvider 为校验函数提供名称查询能力，与 DB 解耦。
type NameProvider interface {
	HasModel(name string) bool
	HasToken(tokenID uint) bool
	GetGlobalRouting(name string) *models.ModelRouting
	AllGlobalRoutings() []*models.ModelRouting
	AllRoutings() []*models.ModelRouting
}

// ValidateError 的 code 与 msg 分离：API 层按 code 做 i18n（前端见 ROUTING_ERROR_KEYS），
// msg 仅用于日志和兜底文案。
type ValidateError struct {
	code    string
	msg     string
	details map[string]any
}

func (e *ValidateError) Code() string            { return e.code }
func (e *ValidateError) Error() string           { return e.msg }
func (e *ValidateError) Details() map[string]any { return e.details }

func newErr(code, msg string, details map[string]any) *ValidateError {
	return &ValidateError{code: code, msg: msg, details: details}
}

// ValidateRouting 校验 ModelRouting，包含名称规则、成员规则、引用检查、DFS 环检测和深度上限。
func ValidateRouting(r *models.ModelRouting, np NameProvider) *ValidateError {
	if err := validateRoutingOwner(r, np); err != nil {
		return err
	}
	if r.Name == "" {
		return newErr(ErrCodeNameRequired, "name is required", nil)
	}
	if len(r.Name) > routingMaxNameLen {
		return newErr(ErrCodeNameTooLong, "name exceeds 128 chars", nil)
	}
	if strings.Contains(r.Name, ",") {
		return newErr(ErrCodeNameContainsComma, "name cannot contain comma", nil)
	}

	var members []models.RoutingMember
	if err := json.Unmarshal([]byte(r.Members), &members); err != nil {
		return newErr(ErrCodeMembersInvalidJSON, "members must be valid JSON", nil)
	}
	if len(members) == 0 {
		return newErr(ErrCodeMembersEmpty, "members must be non-empty", nil)
	}
	if len(members) > routingMaxMembers {
		return newErr(ErrCodeMembersTooMany, "max 32 members", nil)
	}
	for i, m := range members {
		if m.Ref == "" {
			return newErr(ErrCodeRefEmpty, "member ref is empty", map[string]any{"index": i})
		}
		if m.Kind != models.RoutingMemberModel && m.Kind != models.RoutingMemberRouting {
			return newErr(ErrCodeInvalidMemberKind, "member kind must be model or routing", map[string]any{"index": i})
		}
		if (m.Kind == models.RoutingMemberModel && !np.HasModel(m.Ref)) ||
			(m.Kind == models.RoutingMemberRouting && np.GetGlobalRouting(m.Ref) == nil && !(r.Scope == models.RoutingScopeGlobal && m.Ref == r.Name)) {
			return newErr(ErrCodeInvalidRef, "ref does not exist in global namespace",
				map[string]any{"ref": m.Ref, "kind": m.Kind})
		}
	}

	if r.Scope == models.RoutingScopeGlobal {
		if err := dfsCycleAndDepth(r, np); err != nil {
			return err
		}
	}
	return nil
}

func validateRoutingOwner(r *models.ModelRouting, np NameProvider) *ValidateError {
	switch r.Scope {
	case models.RoutingScopeGlobal:
		if r.UserID != 0 || r.TokenID != 0 {
			return newErr(ErrCodeInvalidScopeOwner, "global routing cannot have an owner", nil)
		}
	case models.RoutingScopeUser:
		if r.UserID == 0 || r.TokenID != 0 {
			return newErr(ErrCodeInvalidScopeOwner, "user routing requires only user_id", nil)
		}
	case models.RoutingScopeToken:
		if r.UserID != 0 || r.TokenID == 0 {
			return newErr(ErrCodeInvalidScopeOwner, "token routing requires only token_id", nil)
		}
		if !np.HasToken(r.TokenID) {
			return newErr(ErrCodeTokenNotFound, "token does not exist", map[string]any{"token_id": r.TokenID})
		}
	default:
		return newErr(ErrCodeInvalidScope, "unsupported routing scope", map[string]any{"scope": r.Scope})
	}
	return nil
}

// Check the candidate graph from every root, including disabled routes and every
// priority group. Replacing the edited row is essential for back-edges and for
// edits which make an existing ancestor exceed the maximum depth.
func dfsCycleAndDepth(start *models.ModelRouting, np NameProvider) *ValidateError {
	graph := map[string]*models.ModelRouting{}
	for _, r := range np.AllGlobalRoutings() {
		if start.ID != 0 && r.ID == start.ID {
			continue
		}
		graph[r.Name] = r
	}
	graph[start.Name] = start
	active := map[string]bool{}
	longest := map[string][]string{}
	var visit func(string, []string) ([]string, *ValidateError)
	visit = func(name string, path []string) ([]string, *ValidateError) {
		if active[name] {
			return nil, newErr(ErrCodeCycleDetected, "cycle in routing graph", map[string]any{"path": append(path, name)})
		}
		if cached, ok := longest[name]; ok {
			return cached, nil
		}
		r := graph[name]
		if r == nil {
			return nil, newErr(ErrCodeInvalidRef, "routing target does not exist", map[string]any{"ref": name, "kind": models.RoutingMemberRouting})
		}
		var members []models.RoutingMember
		if err := json.Unmarshal([]byte(r.Members), &members); err != nil {
			return nil, newErr(ErrCodeMembersInvalidJSON, "invalid routing graph members", map[string]any{"ref": name})
		}
		active[name] = true
		defer delete(active, name)
		best := []string{name}
		for _, member := range members {
			if member.Kind == models.RoutingMemberModel {
				continue
			}
			if member.Kind != models.RoutingMemberRouting {
				return nil, newErr(ErrCodeInvalidMemberKind, "invalid routing graph member kind", map[string]any{"ref": name})
			}
			child, err := visit(member.Ref, append(append([]string{}, path...), name))
			if err != nil {
				return nil, err
			}
			if len(child)+1 > len(best) {
				best = append([]string{name}, child...)
			}
		}
		if len(best) > routingMaxDepth {
			return nil, newErr(ErrCodeMaxDepth, "nesting depth exceeds 5", map[string]any{"path": best})
		}
		longest[name] = best
		return best, nil
	}
	names := make([]string, 0, len(graph))
	for name := range graph {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := visit(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// ValidateDelete 检查是否有其他 global routing 引用待删除的 routing。
func ValidateDelete(target *models.ModelRouting, np NameProvider) *ValidateError {
	if target.Scope != models.RoutingScopeGlobal {
		return nil // user routing 不会被引用（user 不能引用 user）
	}
	var refs []string
	for _, r := range np.AllRoutings() {
		if r.ID == target.ID {
			continue
		}
		var members []models.RoutingMember
		if err := json.Unmarshal([]byte(r.Members), &members); err != nil {
			continue
		}
		for _, m := range members {
			if m.Kind == models.RoutingMemberRouting && m.Ref == target.Name {
				refs = append(refs, r.Name)
				break
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return newErr(ErrCodeReferencedBy, "routing is referenced by others",
		map[string]any{"refs": refs})
}
