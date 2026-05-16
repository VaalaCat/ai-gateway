package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// AppProvider is the minimal interface dao needs from the application container.
// Satisfied by AppProvider without importing the app package (avoids import cycles).
type AppProvider interface {
	GetDB() *gorm.DB
}

// Context is the base DAO context for admin-scoped operations.
type Context interface {
	GetDB() *gorm.DB
	WithTx(tx *gorm.DB) Context
}

// UserContext extends Context with authenticated user identity.
// Required for all user-scoped DAO operations.
type UserContext interface {
	Context
	UserInfo() *app.UserInfo
}

var _ Context = (*baseContext)(nil)
var _ UserContext = (*userContextImpl)(nil)

// --- internal implementations ---

type baseContext struct {
	app AppProvider
	tx  *gorm.DB
}

func (c *baseContext) GetDB() *gorm.DB {
	if c.tx != nil {
		return c.tx
	}
	return c.app.GetDB()
}

func (c *baseContext) WithTx(tx *gorm.DB) Context {
	return &baseContext{app: c.app, tx: tx}
}

type userContextImpl struct {
	baseContext
	userInfo *app.UserInfo
}

func (c *userContextImpl) UserInfo() *app.UserInfo { return c.userInfo }

// UserDB returns a *gorm.DB pre-scoped with user_id filter.
func (c *userContextImpl) UserDB() *gorm.DB {
	return c.GetDB().Where("user_id = ?", c.userInfo.UserID)
}

func (c *userContextImpl) WithTx(tx *gorm.DB) Context {
	return &userContextImpl{
		baseContext: baseContext{app: c.app, tx: tx},
		userInfo:    c.userInfo,
	}
}

// --- factory functions ---

// NewContext creates an admin-scoped DAO context.
func NewContext(application AppProvider) Context {
	return &baseContext{app: application}
}

// NewUserContext creates a user-scoped DAO context.
func NewUserContext(application AppProvider, userInfo *app.UserInfo) UserContext {
	return &userContextImpl{
		baseContext: baseContext{app: application},
		userInfo:    userInfo,
	}
}
