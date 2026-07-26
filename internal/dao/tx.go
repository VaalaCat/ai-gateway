package dao

import (
	"fmt"

	"gorm.io/gorm"
)

// RunInTx executes fn within a database transaction.
// The transaction is committed if fn returns nil, rolled back otherwise.
// Type-safe: works with both Context and UserContext.
func RunInTx[T Context](ctx T, fn func(T) error) error {
	return RunInCoreTx(ctx, fn)
}

func RunInCoreTx[T Context](ctx T, fn func(T) error) error {
	db := ctx.GetCoreDB()
	return db.Transaction(func(tx *gorm.DB) error {
		raw := ctx.WithCoreTx(tx)
		txCtx, ok := raw.(T)
		if !ok {
			return fmt.Errorf("dao: WithTx returned %T, expected %T", raw, ctx)
		}
		return fn(txCtx)
	})
}

func RunInLogTx[T Context](ctx T, fn func(T) error) error {
	db, err := ctx.LogDB()
	if err != nil {
		return err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		raw := ctx.WithLogTx(tx)
		txCtx, ok := raw.(T)
		if !ok {
			return fmt.Errorf("dao: WithLogTx returned %T, expected %T", raw, ctx)
		}
		return WrapLogDatabaseError(fn(txCtx))
	})
	return WrapLogDatabaseError(err)
}
