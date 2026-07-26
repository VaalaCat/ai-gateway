package dao

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// AppProvider is the minimal interface dao needs from the application container.
// Satisfied by AppProvider without importing the app package (avoids import cycles).
type AppProvider interface {
	GetCoreDB() *gorm.DB
	GetLogDB() *gorm.DB
	GetDatabaseLayoutMode() app.DatabaseLayoutMode
}

var ErrLogDatabaseUnavailable = errors.New("log database unavailable")
var ErrInvalidDatabaseLayout = errors.New("invalid database layout")
var ErrCrossDatabaseTransaction = errors.New("cross-database transaction access")

type transactionRole uint8

const (
	transactionRoleNone transactionRole = iota
	transactionRoleCore
	transactionRoleLog
)

// Context is the base DAO context for admin-scoped operations.
type Context interface {
	GetCoreDB() *gorm.DB
	CoreDB() *gorm.DB
	LogDB() (*gorm.DB, error)
	RequestLogModel() (*gorm.DB, any, error)
	DatabaseLayoutMode() (app.DatabaseLayoutMode, error)
	WithTx(tx *gorm.DB) Context
	WithCoreTx(tx *gorm.DB) Context
	WithLogTx(tx *gorm.DB) Context
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
	app            AppProvider
	tx             *gorm.DB
	txRole         transactionRole
	requestContext context.Context
}

func (c *baseContext) CoreDB() *gorm.DB { return c.GetCoreDB() }

func (c *baseContext) DatabaseLayoutMode() (app.DatabaseLayoutMode, error) {
	if c.app == nil {
		return app.DatabaseLayoutLegacySingle, nil
	}
	mode := c.app.GetDatabaseLayoutMode()
	if err := mode.Validate(); err != nil {
		return mode, errors.Join(ErrInvalidDatabaseLayout, err)
	}
	return mode, nil
}

func (c *baseContext) LogDB() (*gorm.DB, error) {
	mode, err := c.DatabaseLayoutMode()
	if err != nil {
		return nil, err
	}
	if c.tx != nil {
		if mode == app.DatabaseLayoutLegacySingle || c.txRole == transactionRoleLog {
			return c.tx, nil
		}
		return nil, ErrCrossDatabaseTransaction
	}
	db := c.app.GetCoreDB()
	if mode == app.DatabaseLayoutSplit {
		db = c.app.GetLogDB()
	}
	if db == nil {
		return nil, ErrLogDatabaseUnavailable
	}
	ctx := c.requestContext
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(ctx), nil
}

// RequestLogModel returns the layout-specific database and strongly typed
// request-row model used by raw aggregate queries.
func (c *baseContext) RequestLogModel() (*gorm.DB, any, error) {
	db, err := c.LogDB()
	if err != nil {
		return nil, nil, err
	}
	mode, err := c.DatabaseLayoutMode()
	if err != nil {
		return nil, nil, err
	}
	if mode == app.DatabaseLayoutSplit {
		return db, &models.RequestLog{}, nil
	}
	return db, &models.UsageLog{}, nil
}

func (c *baseContext) GetCoreDB() *gorm.DB {
	if c.tx != nil {
		mode, err := c.DatabaseLayoutMode()
		if err != nil || (mode == app.DatabaseLayoutSplit && c.txRole == transactionRoleLog) {
			db := c.tx.Session(&gorm.Session{})
			db.AddError(errors.Join(ErrCrossDatabaseTransaction, err))
			return db
		}
		return c.tx
	}
	db := c.app.GetCoreDB()
	if _, err := c.DatabaseLayoutMode(); err != nil && db != nil {
		invalid := db.Session(&gorm.Session{})
		invalid.AddError(err)
		return invalid
	}
	if db != nil && c.requestContext != nil {
		return db.WithContext(c.requestContext)
	}
	return db
}

func (c *baseContext) WithTx(tx *gorm.DB) Context {
	return c.WithCoreTx(tx)
}

func (c *baseContext) WithCoreTx(tx *gorm.DB) Context {
	return &baseContext{app: c.app, tx: tx, txRole: transactionRoleCore, requestContext: c.requestContext}
}

func (c *baseContext) WithLogTx(tx *gorm.DB) Context {
	return &baseContext{app: c.app, tx: tx, txRole: transactionRoleLog, requestContext: c.requestContext}
}

type userContextImpl struct {
	baseContext
	userInfo *app.UserInfo
}

func (c *userContextImpl) UserInfo() *app.UserInfo { return c.userInfo }

// UserDB returns a *gorm.DB pre-scoped with user_id filter.
func (c *userContextImpl) UserDB() *gorm.DB {
	return c.GetCoreDB().Where("user_id = ?", c.userInfo.UserID)
}

func (c *userContextImpl) WithTx(tx *gorm.DB) Context {
	return c.WithCoreTx(tx)
}

func (c *userContextImpl) WithCoreTx(tx *gorm.DB) Context {
	return &userContextImpl{
		baseContext: baseContext{app: c.app, tx: tx, txRole: transactionRoleCore, requestContext: c.requestContext},
		userInfo:    c.userInfo,
	}
}

func (c *userContextImpl) WithLogTx(tx *gorm.DB) Context {
	return &userContextImpl{
		baseContext: baseContext{app: c.app, tx: tx, txRole: transactionRoleLog, requestContext: c.requestContext},
		userInfo:    c.userInfo,
	}
}

func WrapLogDatabaseError(err error) error {
	if err == nil || errors.Is(err, ErrLogDatabaseUnavailable) || errors.Is(err, ErrCrossDatabaseTransaction) || errors.Is(err, ErrInvalidDatabaseLayout) {
		return err
	}
	message := strings.ToLower(err.Error())
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) || strings.Contains(message, "database is closed") || strings.Contains(message, "sql: database is closed") {
		return errors.Join(ErrLogDatabaseUnavailable, err)
	}
	return err
}

// --- factory functions ---

// NewContext creates an admin-scoped DAO context.
func NewContext(application AppProvider) Context {
	return &baseContext{app: application}
}

func NewContextWithContext(application AppProvider, ctx context.Context) Context {
	if ctx == nil {
		panic("dao.NewContextWithContext: nil context")
	}
	return &baseContext{app: application, requestContext: ctx}
}

// NewUserContext creates a user-scoped DAO context.
func NewUserContext(application AppProvider, userInfo *app.UserInfo) UserContext {
	return &userContextImpl{
		baseContext: baseContext{app: application},
		userInfo:    userInfo,
	}
}

func NewUserContextWithContext(application AppProvider, ctx context.Context, userInfo *app.UserInfo) UserContext {
	if ctx == nil {
		panic("dao.NewUserContextWithContext: nil context")
	}
	return &userContextImpl{
		baseContext: baseContext{app: application, requestContext: ctx},
		userInfo:    userInfo,
	}
}
