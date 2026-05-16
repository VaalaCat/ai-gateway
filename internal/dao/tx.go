package dao

import (
	"fmt"

	"gorm.io/gorm"
)

// RunInTx executes fn within a database transaction.
// The transaction is committed if fn returns nil, rolled back otherwise.
// Type-safe: works with both Context and UserContext.
func RunInTx[T Context](ctx T, fn func(T) error) error {
	db := ctx.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		raw := ctx.WithTx(tx)
		txCtx, ok := raw.(T)
		if !ok {
			return fmt.Errorf("dao: WithTx returned %T, expected %T", raw, ctx)
		}
		return fn(txCtx)
	})
}
