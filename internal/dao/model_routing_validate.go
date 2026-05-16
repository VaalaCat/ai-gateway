package dao

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const (
	routingMaxNameLen = 128
	routingMaxMembers = 32
	routingMaxDepth   = 5

	ErrCodeNameRequired        = "name_required"
	ErrCodeNameTooLong         = "name_too_long"
	ErrCodeNameContainsComma   = "name_contains_comma"
	ErrCodeMembersInvalidJSON  = "members_invalid_json"
	ErrCodeMembersEmpty        = "members_empty"
	ErrCodeMembersTooMany      = "members_too_many"
	ErrCodeRefEmpty            = "ref_empty"
	ErrCodeInvalidRef          = "invalid_ref"
	ErrCodeCycleDetected       = "cycle_detected"
	ErrCodeMaxDepth            = "max_depth"
	ErrCodeReferencedBy        = "referenced_by"
	ErrCodeDBError             = "db_error"
	ErrCodeNotFound            = "not_found"
)

// NameProvider 为校验函数提供名称查询能力，与 DB 解耦。
type NameProvider interface {
	HasModel(name string) bool
	GetGlobalRouting(name string) *models.ModelRouting
	AllGlobalRoutings() []*models.ModelRouting
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
		if !np.HasModel(m.Ref) && np.GetGlobalRouting(m.Ref) == nil {
			return newErr(ErrCodeInvalidRef, "ref does not exist in global namespace",
				map[string]any{"ref": m.Ref})
		}
	}

	if r.Scope == models.RoutingScopeGlobal {
		if err := dfsCycleAndDepth(r, np, members); err != nil {
			return err
		}
	}
	return nil
}

func dfsCycleAndDepth(start *models.ModelRouting, np NameProvider, startMembers []models.RoutingMember) *ValidateError {
	visited := map[string]bool{start.Name: true}
	path := []string{start.Name}
	return dfsRouting(startMembers, np, visited, path, 1)
}

func dfsRouting(members []models.RoutingMember, np NameProvider,
	visited map[string]bool, path []string, depth int) *ValidateError {

	if depth > routingMaxDepth {
		return newErr(ErrCodeMaxDepth, "nesting depth exceeds 5", map[string]any{"path": path})
	}
	for _, m := range members {
		// 如果 ref 名称已在当前路径上，直接判环（包含 start 本身不在 np 的情形）。
		if visited[m.Ref] {
			cycle := append(append([]string{}, path...), m.Ref)
			return newErr(ErrCodeCycleDetected, "cycle in routing graph",
				map[string]any{"path": cycle})
		}
		r := np.GetGlobalRouting(m.Ref)
		if r == nil {
			continue // 真实 model，跳过
		}
		var childMembers []models.RoutingMember
		_ = json.Unmarshal([]byte(r.Members), &childMembers)
		visited[r.Name] = true
		newPath := append(append([]string{}, path...), r.Name)
		if e := dfsRouting(childMembers, np, visited, newPath, depth+1); e != nil {
			return e
		}
		delete(visited, r.Name)
	}
	return nil
}

// ValidateDelete 检查是否有其他 global routing 引用待删除的 routing。
func ValidateDelete(target *models.ModelRouting, np NameProvider) *ValidateError {
	if target.Scope != models.RoutingScopeGlobal {
		return nil // user routing 不会被引用（user 不能引用 user）
	}
	var refs []string
	for _, r := range np.AllGlobalRoutings() {
		if r.ID == target.ID {
			continue
		}
		var members []models.RoutingMember
		if err := json.Unmarshal([]byte(r.Members), &members); err != nil {
			continue
		}
		for _, m := range members {
			if m.Ref == target.Name {
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
