package api_access_grant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GrantWriter owns the internal managed-role representation for quick API access grants.
type GrantWriter struct{}

var grantTransactionAttempt = func(ctx dao.Context, fn func(dao.Context) error) error {
	return dao.RunInCoreTx[dao.Context](ctx, fn)
}

// RemoveManagedRole deletes a principal's internal quick-grant role and all of
// its associations. Callers already inside a Core transaction can use it when
// the principal becomes ineligible for managed grants.
func RemoveManagedRole(db *gorm.DB, principal PrincipalRef) (uint, error) {
	role, err := findManagedRole(db, principal)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := db.Where("role_id = ?", role.ID).Delete(&models.RolePermission{}).Error; err != nil {
		return 0, err
	}
	if err := db.Where("role_id = ?", role.ID).Delete(&models.RoleBinding{}).Error; err != nil {
		return 0, err
	}
	if err := db.Delete(&role).Error; err != nil {
		return 0, err
	}
	return role.ID, nil
}

func (GrantWriter) Replace(ctx dao.Context, principal PrincipalRef, serviceID uint, scope GrantScope, routeIDs []uint) (ConfiguredGrant, error) {
	grant, err := normalizeGrant(principal, serviceID, scope, routeIDs)
	if err != nil {
		return ConfiguredGrant{}, err
	}
	err = runGrantTransaction(ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		if err := validateGrantSubjects(db, principal, serviceID, grant.RouteIDs); err != nil {
			return err
		}
		role, err := findOrCreateManagedRole(db, principal)
		if err != nil {
			return err
		}
		if err := replaceServicePermissions(db, role.ID, serviceID, grant.Scope, grant.RouteIDs); err != nil {
			return err
		}
		return ensureManagedBinding(db, principal, role.ID)
	})
	return grant, err
}

func (GrantWriter) Delete(ctx dao.Context, principal PrincipalRef, serviceID uint) error {
	if err := validatePrincipalRef(principal); err != nil {
		return err
	}
	if serviceID == 0 {
		return errors.New("api service id must not be zero")
	}
	return runGrantTransaction(ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		if err := validatePrincipal(db, principal); err != nil {
			return err
		}
		if err := db.First(&models.APIService{}, serviceID).Error; err != nil {
			return err
		}
		role, err := findManagedRole(db, principal)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := removeServicePermissions(db, role.ID, serviceID); err != nil {
			return err
		}
		return deleteManagedRoleWhenEmpty(db, role)
	})
}

// SQLite admits one writer at a time. A deferred read transaction can fail to
// upgrade after another writer commits, so retry the complete transaction
// rather than retrying an individual statement with a stale snapshot.
func runGrantTransaction(ctx dao.Context, fn func(dao.Context) error) error {
	retryCtx := ctx.GetCoreDB().Statement.Context
	if retryCtx == nil {
		retryCtx = context.Background()
	}
	for attempt := 0; ; attempt++ {
		err := grantTransactionAttempt(ctx, fn)
		if err == nil || !isSQLiteBusy(ctx, err) || attempt == 7 {
			return err
		}
		if err := waitForGrantRetry(retryCtx, time.Duration(1<<attempt)*time.Millisecond); err != nil {
			return err
		}
	}
}

func waitForGrantRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isSQLiteBusy(ctx dao.Context, err error) bool {
	if ctx.GetCoreDB().Dialector.Name() != "sqlite" {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") || strings.Contains(message, "database is deadlocked")
}

func normalizeGrant(principal PrincipalRef, serviceID uint, scope GrantScope, routeIDs []uint) (ConfiguredGrant, error) {
	if err := validatePrincipalRef(principal); err != nil {
		return ConfiguredGrant{}, err
	}
	if serviceID == 0 {
		return ConfiguredGrant{}, errors.New("api service id must not be zero")
	}
	ids := slices.Clone(routeIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	switch scope {
	case GrantScopeService:
		if len(ids) != 0 {
			return ConfiguredGrant{}, errors.New("service grant must not include route_ids")
		}
	case GrantScopeRoutes:
		if len(ids) == 0 || ids[0] == 0 {
			return ConfiguredGrant{}, errors.New("route grant requires non-empty route_ids")
		}
	default:
		return ConfiguredGrant{}, fmt.Errorf("api access grant scope is invalid: %q", scope)
	}
	return ConfiguredGrant{PrincipalType: principal.Type, PrincipalID: principal.ID, APIServiceID: serviceID, Scope: scope, RouteIDs: ids}, nil
}

func validatePrincipalRef(principal PrincipalRef) error {
	if principal.ID == 0 {
		return errors.New("api access grant principal id must not be zero")
	}
	switch principal.Type {
	case models.APIPrincipalUser, models.APIPrincipalUserGroup, models.APIPrincipalToken:
		return nil
	default:
		return fmt.Errorf("api access grant principal type is invalid: %q", principal.Type)
	}
}

func validateGrantSubjects(db *gorm.DB, principal PrincipalRef, serviceID uint, routeIDs []uint) error {
	if err := validatePrincipal(db, principal); err != nil {
		return err
	}
	if err := db.First(&models.APIService{}, serviceID).Error; err != nil {
		return err
	}
	return validateRoutesInService(db, serviceID, routeIDs)
}

func validatePrincipal(db *gorm.DB, principal PrincipalRef) error {
	if err := dao.LockAPIPrincipal(db, principal.Type, principal.ID); err != nil {
		return err
	}
	switch principal.Type {
	case models.APIPrincipalToken:
		var token models.Token
		if err := db.First(&token, principal.ID).Error; err != nil {
			return err
		}
		if token.APIRoleMode != models.APIRoleModeExplicit {
			return errors.New("API access grant token must use explicit api_role_mode")
		}
		return nil
	case models.APIPrincipalUser:
		return nil
	case models.APIPrincipalUserGroup:
		return nil
	default:
		return validatePrincipalRef(principal)
	}
}

func validateRoutesInService(db *gorm.DB, serviceID uint, routeIDs []uint) error {
	if len(routeIDs) == 0 {
		return nil
	}
	var count int64
	if err := db.Model(&models.APIRoute{}).Where("api_service_id = ? AND id IN ?", serviceID, routeIDs).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(routeIDs)) {
		return errors.New("api access grant routes must belong to the target service")
	}
	return nil
}

func findOrCreateManagedRole(db *gorm.DB, principal PrincipalRef) (models.Role, error) {
	role, err := findManagedRole(db, principal)
	if err == nil {
		return role, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Role{}, err
	}
	role = models.Role{
		Key:    models.ManagedAPIRoleKey(principal.Type, principal.ID),
		Name:   fmt.Sprintf("Managed API access: %s %d", principal.Type, principal.ID),
		Kind:   models.APIRoleKindManaged,
		Status: consts.StatusEnabled,
	}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoNothing: true}).Create(&role).Error; err != nil {
		return models.Role{}, err
	}
	if role.ID != 0 {
		return role, nil
	}
	return findManagedRole(db, principal)
}

func findManagedRole(db *gorm.DB, principal PrincipalRef) (models.Role, error) {
	var role models.Role
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).First(&role).Error
	if err == nil && role.Kind != models.APIRoleKindManaged {
		return models.Role{}, errors.New("managed API access role key is occupied by a non-managed role")
	}
	return role, err
}

func replaceServicePermissions(db *gorm.DB, roleID, serviceID uint, scope GrantScope, routeIDs []uint) error {
	if err := removeServicePermissions(db, roleID, serviceID); err != nil {
		return err
	}
	if scope == GrantScopeService {
		return ensureRolePermission(db, roleID, models.APIResourceService, serviceID)
	}
	for _, routeID := range routeIDs {
		if err := ensureRolePermission(db, roleID, models.APIResourceRoute, routeID); err != nil {
			return err
		}
	}
	return nil
}

func removeServicePermissions(db *gorm.DB, roleID, serviceID uint) error {
	routeIDs := db.Model(&models.APIRoute{}).Select("id").Where("api_service_id = ?", serviceID)
	permissionIDs := db.Model(&models.Permission{}).Select("id").Where(
		"action = ? AND ((resource = ? AND resource_id = ?) OR (resource = ? AND resource_id IN (?)))",
		models.APIPermissionInvoke, models.APIResourceService, serviceID, models.APIResourceRoute, routeIDs,
	)
	return db.Where("role_id = ? AND permission_id IN (?)", roleID, permissionIDs).Delete(&models.RolePermission{}).Error
}

func ensureRolePermission(db *gorm.DB, roleID uint, resource models.APIResource, resourceID uint) error {
	permission := models.Permission{Resource: resource, ResourceID: resourceID, Action: models.APIPermissionInvoke}
	if err := db.Where("resource = ? AND resource_id = ? AND action = ?", resource, resourceID, models.APIPermissionInvoke).First(&permission).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource"}, {Name: "resource_id"}, {Name: "action"}}, DoNothing: true}).Create(&permission).Error; err != nil {
			return err
		}
		if permission.ID == 0 {
			if err := db.Where("resource = ? AND resource_id = ? AND action = ?", resource, resourceID, models.APIPermissionInvoke).First(&permission).Error; err != nil {
				return err
			}
		}
	}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "role_id"}, {Name: "permission_id"}}, DoNothing: true}).Create(&models.RolePermission{RoleID: roleID, PermissionID: permission.ID}).Error
}

func ensureManagedBinding(db *gorm.DB, principal PrincipalRef, roleID uint) error {
	binding := models.RoleBinding{PrincipalType: principal.Type, PrincipalID: principal.ID, RoleID: roleID}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "principal_type"}, {Name: "principal_id"}, {Name: "role_id"}}, DoNothing: true}).Create(&binding).Error
}

func deleteManagedRoleWhenEmpty(db *gorm.DB, role models.Role) error {
	var count int64
	if err := db.Model(&models.RolePermission{}).Where("role_id = ?", role.ID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	if err := db.Where("role_id = ?", role.ID).Delete(&models.RoleBinding{}).Error; err != nil {
		return err
	}
	return db.Delete(&role).Error
}
