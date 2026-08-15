package dao

import (
	"fmt"
	"slices"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type APIRBACQuery interface {
	ListUsersByIDs(ids []uint) ([]models.User, error)
	GetRoleByID(id uint) (*models.Role, error)
	GetPermissionByID(id uint) (*models.Permission, error)
	ListEnabledRoles() ([]models.Role, error)
	MaxEnabledRoleID() (uint, error)
	ListEnabledRolesKeyset(afterID, snapshotMaxID uint, limit int) ([]models.Role, error)
	CountEnabledRolesThroughID(snapshotMaxID uint) (int64, error)
	ListRolesByKey(key string) ([]models.Role, error)
	ListRoles(opts ListOptions, filter APIRoleFilter) ([]models.Role, int64, error)
	ListPermissions() ([]models.Permission, error)
	ListRolePermissions(roleID uint) ([]models.RolePermission, error)
	ListRolePermissionsByRoleIDs(roleIDs []uint) ([]models.RolePermission, error)
	ListPermissionsByIDs(permissionIDs []uint) ([]models.Permission, error)
	ListRoleBindings(roleID uint) ([]models.RoleBinding, error)
	ListRoleBindingsByRoleIDs(roleIDs []uint) ([]models.RoleBinding, error)
	ListRoleBindingsPage(opts ListOptions, filter APIRoleBindingFilter) ([]models.RoleBinding, int64, error)
	ListRoleBindingsByPrincipal(principalType models.APIPrincipalType, principalID uint) ([]models.RoleBinding, error)
	ListRoleSetBindingsByPrincipal(principalType models.APIPrincipalType, principalID uint) ([]models.RoleBinding, error)
	ListRoleSetBindingsByPrincipals(principalType models.APIPrincipalType, principalIDs []uint) ([]models.RoleBinding, error)
}

type APIRoleFilter struct {
	Search     string
	Status     *int
	Assignable *bool
	RoleKind   *models.APIRoleKind
}

type APIRoleBindingFilter struct {
	PrincipalType *models.APIPrincipalType
	PrincipalID   *uint
	RoleID        *uint
	RoleKind      *models.APIRoleKind
}

func (q *apiRBACQuery) MaxEnabledRoleID() (uint, error) {
	var id uint
	err := q.ctx.GetCoreDB().Model(&models.Role{}).Where("status = ?", consts.StatusEnabled).
		Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}

func (q *apiRBACQuery) ListEnabledRolesKeyset(afterID, snapshotMaxID uint, limit int) ([]models.Role, error) {
	var rows []models.Role
	err := q.ctx.GetCoreDB().Where("status = ? AND id > ? AND id <= ?", consts.StatusEnabled, afterID, snapshotMaxID).
		Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) CountEnabledRolesThroughID(snapshotMaxID uint) (int64, error) {
	var total int64
	err := q.ctx.GetCoreDB().Model(&models.Role{}).
		Where("status = ? AND id <= ?", consts.StatusEnabled, snapshotMaxID).Count(&total).Error
	return total, err
}

type APIRBACMutation interface {
	CreateRole(role *models.Role) error
	UpdateRole(id uint, patch map[string]any) error
	DeleteRole(id uint) error
	CreatePermission(permission *models.Permission) error
	UpdatePermission(id uint, patch map[string]any) error
	DeletePermission(id uint) error
	CreateRolePermission(permission *models.RolePermission) error
	DeleteRolePermission(id uint) error
	CreateRoleBinding(binding *models.RoleBinding) error
	ReplaceRoleBindingsByPrincipal(principalType models.APIPrincipalType, principalID uint, roleIDs []uint) error
	ReplaceCustomRoleBindingsByPrincipal(principalType models.APIPrincipalType, principalID uint, roleIDs []uint) error
	UpdateRoleBinding(id uint, patch map[string]any) error
	DeleteRoleBinding(id uint) error
}

type apiRBACQuery struct{ ctx *baseContext }
type apiRBACMutation struct{ ctx *baseContext }

const apiRBACQueryChunkSize = 400

func listAPIRBACRowsInIDChunks[T any](ids []uint, find func([]uint) ([]T, error)) ([]T, error) {
	normalizedIDs := slices.Clone(ids)
	slices.Sort(normalizedIDs)
	normalizedIDs = slices.Compact(normalizedIDs)
	rows := make([]T, 0)
	for start := 0; start < len(normalizedIDs); start += apiRBACQueryChunkSize {
		end := min(start+apiRBACQueryChunkSize, len(normalizedIDs))
		chunkRows, err := find(normalizedIDs[start:end])
		rows = append(rows, chunkRows...)
		if err != nil {
			return rows, err
		}
	}
	return rows, nil
}

func (q *apiRBACQuery) ListUsersByIDs(ids []uint) ([]models.User, error) {
	rows, err := listAPIRBACRowsInIDChunks(ids, func(chunk []uint) ([]models.User, error) {
		var chunkRows []models.User
		err := q.ctx.GetCoreDB().Where("id IN ?", chunk).Find(&chunkRows).Error
		return chunkRows, err
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, err
}

func (q *apiRBACQuery) GetRoleByID(id uint) (*models.Role, error) {
	var role models.Role
	err := q.ctx.GetCoreDB().First(&role, id).Error
	return &role, err
}

func (q *apiRBACQuery) GetPermissionByID(id uint) (*models.Permission, error) {
	var permission models.Permission
	err := q.ctx.GetCoreDB().First(&permission, id).Error
	return &permission, err
}

func (q *apiRBACQuery) ListEnabledRoles() ([]models.Role, error) {
	var rows []models.Role
	err := q.ctx.GetCoreDB().Where("status = ?", consts.StatusEnabled).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRolesByKey(key string) ([]models.Role, error) {
	var rows []models.Role
	err := q.ctx.GetCoreDB().Where("`key` = ?", key).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRoles(opts ListOptions, filter APIRoleFilter) ([]models.Role, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.Role{})
	if filter.Search != "" {
		db = db.Where("name LIKE ? OR `key` LIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	if filter.RoleKind != nil {
		db = db.Where("kind = ?", *filter.RoleKind)
	}
	if filter.Assignable != nil && *filter.Assignable {
		db = db.Where("status = ? AND built_in = ? AND `key` <> ?", consts.StatusEnabled, false, models.GatewayAdminRoleKey)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.Role
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (q *apiRBACQuery) ListPermissions() ([]models.Permission, error) {
	var rows []models.Permission
	err := q.ctx.GetCoreDB().Order("id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRolePermissions(roleID uint) ([]models.RolePermission, error) {
	var rows []models.RolePermission
	err := q.ctx.GetCoreDB().Where("role_id = ?", roleID).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRolePermissionsByRoleIDs(roleIDs []uint) ([]models.RolePermission, error) {
	rows, err := listAPIRBACRowsInIDChunks(roleIDs, func(chunk []uint) ([]models.RolePermission, error) {
		var chunkRows []models.RolePermission
		err := q.ctx.GetCoreDB().Where("role_id IN ?", chunk).Find(&chunkRows).Error
		return chunkRows, err
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RoleID != rows[j].RoleID {
			return rows[i].RoleID < rows[j].RoleID
		}
		return rows[i].PermissionID < rows[j].PermissionID
	})
	return rows, err
}

func (q *apiRBACQuery) ListPermissionsByIDs(permissionIDs []uint) ([]models.Permission, error) {
	rows, err := listAPIRBACRowsInIDChunks(permissionIDs, func(chunk []uint) ([]models.Permission, error) {
		var chunkRows []models.Permission
		err := q.ctx.GetCoreDB().Where("id IN ?", chunk).Find(&chunkRows).Error
		return chunkRows, err
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, err
}

func (q *apiRBACQuery) ListRoleBindings(roleID uint) ([]models.RoleBinding, error) {
	var rows []models.RoleBinding
	err := q.ctx.GetCoreDB().Where("role_id = ?", roleID).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRoleBindingsByRoleIDs(roleIDs []uint) ([]models.RoleBinding, error) {
	rows, err := listAPIRBACRowsInIDChunks(roleIDs, func(chunk []uint) ([]models.RoleBinding, error) {
		var chunkRows []models.RoleBinding
		err := q.ctx.GetCoreDB().Where("role_id IN ?", chunk).Find(&chunkRows).Error
		return chunkRows, err
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RoleID != rows[j].RoleID {
			return rows[i].RoleID < rows[j].RoleID
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, err
}

func (q *apiRBACQuery) ListRoleBindingsPage(opts ListOptions, filter APIRoleBindingFilter) ([]models.RoleBinding, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.RoleBinding{})
	if filter.RoleKind != nil {
		roleIDs := q.ctx.GetCoreDB().Model(&models.Role{}).Select("id").Where("kind = ?", *filter.RoleKind)
		db = db.Where("role_id IN (?)", roleIDs)
	}
	if filter.PrincipalType != nil {
		db = db.Where("principal_type = ?", *filter.PrincipalType)
	}
	if filter.PrincipalID != nil {
		db = db.Where("principal_id = ?", *filter.PrincipalID)
	}
	if filter.RoleID != nil {
		db = db.Where("role_id = ?", *filter.RoleID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.RoleBinding
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (q *apiRBACQuery) ListRoleBindingsByPrincipal(
	principalType models.APIPrincipalType,
	principalID uint,
) ([]models.RoleBinding, error) {
	var rows []models.RoleBinding
	err := q.ctx.GetCoreDB().Where(
		"principal_type = ? AND principal_id = ?", principalType, principalID,
	).Order("role_id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRoleSetBindingsByPrincipal(
	principalType models.APIPrincipalType,
	principalID uint,
) ([]models.RoleBinding, error) {
	var rows []models.RoleBinding
	err := q.ctx.GetCoreDB().Where(
		"principal_type = ? AND principal_id = ? AND role_id NOT IN (?)",
		principalType,
		principalID,
		q.ctx.GetCoreDB().Model(&models.Role{}).Select("id").Where("`key` = ?", models.GatewayAdminRoleKey),
	).Order("role_id ASC").Find(&rows).Error
	return rows, err
}

func (q *apiRBACQuery) ListRoleSetBindingsByPrincipals(
	principalType models.APIPrincipalType,
	principalIDs []uint,
) ([]models.RoleBinding, error) {
	rows, err := listAPIRBACRowsInIDChunks(principalIDs, func(chunk []uint) ([]models.RoleBinding, error) {
		var chunkRows []models.RoleBinding
		err := q.ctx.GetCoreDB().Where(
			"principal_type = ? AND principal_id IN ? AND role_id NOT IN (SELECT id FROM roles WHERE `key` = ?)",
			principalType,
			chunk,
			models.GatewayAdminRoleKey,
		).Find(&chunkRows).Error
		return chunkRows, err
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PrincipalID != rows[j].PrincipalID {
			return rows[i].PrincipalID < rows[j].PrincipalID
		}
		return rows[i].RoleID < rows[j].RoleID
	})
	return rows, err
}

func (m *apiRBACMutation) CreateRole(role *models.Role) error {
	return m.ctx.GetCoreDB().Create(role).Error
}

func (m *apiRBACMutation) UpdateRole(id uint, patch map[string]any) error {
	var role models.Role
	if err := m.ctx.GetCoreDB().First(&role, id).Error; err != nil {
		return err
	}
	for key, value := range patch {
		switch key {
		case "key":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			role.Key = v
		case "name":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			role.Name = v
		case "description":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			role.Description = v
		case "built_in":
			v, ok := value.(bool)
			if !ok {
				return gorm.ErrInvalidData
			}
			role.BuiltIn = v
		case "status":
			v, ok := value.(int)
			if !ok {
				return gorm.ErrInvalidData
			}
			role.Status = v
		default:
			return gorm.ErrInvalidData
		}
	}
	if err := role.Validate(); err != nil {
		return err
	}
	return m.ctx.GetCoreDB().Save(&role).Error
}

func (m *apiRBACMutation) DeleteRole(id uint) error {
	return RunInCoreTx[Context](m.ctx, func(ctx Context) error {
		db := ctx.GetCoreDB()
		if err := db.Where("role_id = ?", id).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err := db.Where("role_id = ?", id).Delete(&models.RoleBinding{}).Error; err != nil {
			return err
		}
		return db.Delete(&models.Role{}, id).Error
	})
}

func (m *apiRBACMutation) CreatePermission(permission *models.Permission) error {
	return m.ctx.GetCoreDB().Create(permission).Error
}

func (m *apiRBACMutation) UpdatePermission(id uint, patch map[string]any) error {
	var permission models.Permission
	if err := m.ctx.GetCoreDB().First(&permission, id).Error; err != nil {
		return err
	}
	for key, value := range patch {
		switch key {
		case "resource":
			v, ok := value.(models.APIResource)
			if !ok {
				return gorm.ErrInvalidData
			}
			permission.Resource = v
		case "resource_id":
			v, ok := value.(uint)
			if !ok {
				return gorm.ErrInvalidData
			}
			permission.ResourceID = v
		case "action":
			v, ok := value.(models.APIPermissionAction)
			if !ok {
				return gorm.ErrInvalidData
			}
			permission.Action = v
		default:
			return gorm.ErrInvalidData
		}
	}
	if err := permission.Validate(); err != nil {
		return err
	}
	return m.ctx.GetCoreDB().Save(&permission).Error
}

func (m *apiRBACMutation) DeletePermission(id uint) error {
	return RunInCoreTx[Context](m.ctx, func(ctx Context) error {
		db := ctx.GetCoreDB()
		if err := db.Where("permission_id = ?", id).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		return db.Delete(&models.Permission{}, id).Error
	})
}

func (m *apiRBACMutation) CreateRolePermission(permission *models.RolePermission) error {
	if permission == nil || permission.RoleID == 0 || permission.PermissionID == 0 {
		return gorm.ErrInvalidData
	}
	return m.ctx.GetCoreDB().Create(permission).Error
}
func (m *apiRBACMutation) DeleteRolePermission(id uint) error {
	return m.ctx.GetCoreDB().Delete(&models.RolePermission{}, id).Error
}
func (m *apiRBACMutation) CreateRoleBinding(binding *models.RoleBinding) error {
	if binding == nil {
		return gorm.ErrInvalidData
	}
	if err := m.validateBindableRole(binding.RoleID); err != nil {
		return err
	}
	return m.ctx.GetCoreDB().Create(binding).Error
}

func (m *apiRBACMutation) ReplaceRoleBindingsByPrincipal(
	principalType models.APIPrincipalType,
	principalID uint,
	roleIDs []uint,
) error {
	return RunInCoreTx[Context](m.ctx, func(ctx Context) error {
		txMutation := &apiRBACMutation{ctx: getBaseContext(ctx)}
		return txMutation.replaceRoleBindingsByPrincipal(principalType, principalID, roleIDs)
	})
}

// ReplaceCustomRoleBindingsByPrincipal keeps internal managed bindings intact
// while replacing the ordinary custom-role assignments selected by an admin.
func (m *apiRBACMutation) ReplaceCustomRoleBindingsByPrincipal(
	principalType models.APIPrincipalType,
	principalID uint,
	roleIDs []uint,
) error {
	return RunInCoreTx[Context](m.ctx, func(ctx Context) error {
		txMutation := &apiRBACMutation{ctx: getBaseContext(ctx)}
		return txMutation.replaceCustomRoleBindingsByPrincipal(principalType, principalID, roleIDs)
	})
}

func (m *apiRBACMutation) replaceRoleBindingsByPrincipal(
	principalType models.APIPrincipalType,
	principalID uint,
	roleIDs []uint,
) error {
	probe := models.RoleBinding{PrincipalType: principalType, PrincipalID: principalID, RoleID: 1}
	if err := probe.Validate(); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if err := m.validateBindableRole(roleID); err != nil {
			return err
		}
	}
	if err := m.ctx.GetCoreDB().Where(
		"principal_type = ? AND principal_id = ?", principalType, principalID,
	).Delete(&models.RoleBinding{}).Error; err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if err := m.ctx.GetCoreDB().Create(&models.RoleBinding{
			PrincipalType: principalType, PrincipalID: principalID, RoleID: roleID,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (m *apiRBACMutation) replaceCustomRoleBindingsByPrincipal(
	principalType models.APIPrincipalType,
	principalID uint,
	roleIDs []uint,
) error {
	probe := models.RoleBinding{PrincipalType: principalType, PrincipalID: principalID, RoleID: 1}
	if err := probe.Validate(); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if err := m.validateBindableRole(roleID); err != nil {
			return err
		}
	}
	db := m.ctx.GetCoreDB()
	customRoleIDs := db.Model(&models.Role{}).Select("id").Where("kind = ?", models.APIRoleKindCustom)
	if err := db.Where("principal_type = ? AND principal_id = ? AND role_id IN (?)", principalType, principalID, customRoleIDs).Delete(&models.RoleBinding{}).Error; err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if err := db.Create(&models.RoleBinding{PrincipalType: principalType, PrincipalID: principalID, RoleID: roleID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (m *apiRBACMutation) UpdateRoleBinding(id uint, patch map[string]any) error {
	var binding models.RoleBinding
	if err := m.ctx.GetCoreDB().First(&binding, id).Error; err != nil {
		return err
	}
	for key, value := range patch {
		switch key {
		case "principal_type":
			v, ok := value.(models.APIPrincipalType)
			if !ok {
				return gorm.ErrInvalidData
			}
			binding.PrincipalType = v
		case "principal_id":
			v, ok := value.(uint)
			if !ok {
				return gorm.ErrInvalidData
			}
			binding.PrincipalID = v
		case "role_id":
			v, ok := value.(uint)
			if !ok {
				return gorm.ErrInvalidData
			}
			binding.RoleID = v
		default:
			return gorm.ErrInvalidData
		}
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := m.validateBindableRole(binding.RoleID); err != nil {
		return err
	}
	return m.ctx.GetCoreDB().Save(&binding).Error
}

func (m *apiRBACMutation) DeleteRoleBinding(id uint) error {
	return m.ctx.GetCoreDB().Delete(&models.RoleBinding{}, id).Error
}

func (m *apiRBACMutation) validateBindableRole(roleID uint) error {
	if roleID == 0 {
		return gorm.ErrInvalidData
	}
	var role models.Role
	if err := m.ctx.GetCoreDB().First(&role, roleID).Error; err != nil {
		return err
	}
	if role.Kind != models.APIRoleKindCustom || role.Key == models.GatewayAdminRoleKey {
		return fmt.Errorf("gateway_admin API role cannot be bound to a principal")
	}
	if role.Status != consts.StatusEnabled {
		return fmt.Errorf("API role %d is disabled", roleID)
	}
	return nil
}
