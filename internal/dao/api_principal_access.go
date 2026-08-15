package dao

import (
	"errors"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LockAPIPrincipal serializes deletion with every transaction that can add an
// API role binding or managed grant for the same principal.
func LockAPIPrincipal(db *gorm.DB, principalType models.APIPrincipalType, principalID uint) error {
	modelsByType := map[models.APIPrincipalType]any{
		models.APIPrincipalUser:      &models.User{},
		models.APIPrincipalUserGroup: &models.UserGroup{},
		models.APIPrincipalToken:     &models.Token{},
	}
	model, ok := modelsByType[principalType]
	if !ok || principalID == 0 {
		return fmt.Errorf("invalid API principal %q:%d", principalType, principalID)
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(model, principalID).Error
}

// DeleteAPIPrincipalAccess removes every binding owned by one principal and
// deletes its optional managed grant role. The caller owns the transaction.
func DeleteAPIPrincipalAccess(db *gorm.DB, principalType models.APIPrincipalType, principalID uint) (uint, error) {
	var role models.Role
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("`key` = ?", models.ManagedAPIRoleKey(principalType, principalID)).First(&role).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		role = models.Role{}
	} else if role.Kind != models.APIRoleKindManaged {
		return 0, errors.New("managed API access role key is occupied by a non-managed role")
	}
	if err := db.Where("principal_type = ? AND principal_id = ?", principalType, principalID).
		Delete(&models.RoleBinding{}).Error; err != nil {
		return 0, err
	}
	if role.ID == 0 {
		return 0, nil
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
