package dao

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestNewContext_GetDB(t *testing.T) {
	ctx, db := setupAdminContext(t)
	if ctx.GetDB() != db {
		t.Fatal("GetDB should return the app's DB")
	}
}

func TestContextConstructorsRequireAndPreserveContext(t *testing.T) {
	application, _ := setupTestApp(t)
	t.Run("valid", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), struct{}{}, "value")
		admin := NewContextWithContext(application, ctx)
		user := NewUserContextWithContext(application, ctx, &app.UserInfo{UserID: 7})
		if admin.GetDB().Statement.Context != ctx {
			t.Fatal("admin DAO context did not preserve caller context")
		}
		if user.GetDB().Statement.Context != ctx {
			t.Fatal("user DAO context did not preserve caller context")
		}
	})
	t.Run("canceled cause", func(t *testing.T) {
		cause := errors.New("request canceled")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		admin := NewContextWithContext(application, ctx)
		user := NewUserContextWithContext(application, ctx, &app.UserInfo{UserID: 7})
		if got := context.Cause(admin.GetDB().Statement.Context); !errors.Is(got, cause) {
			t.Fatalf("admin DAO context cause = %v, want %v", got, cause)
		}
		if got := context.Cause(user.GetDB().Statement.Context); !errors.Is(got, cause) {
			t.Fatalf("user DAO context cause = %v, want %v", got, cause)
		}
	})
	for _, tt := range []struct {
		name string
		call func()
	}{
		{name: "admin nil", call: func() { NewContextWithContext(application, nil) }},
		{name: "user nil", call: func() { NewUserContextWithContext(application, nil, &app.UserInfo{UserID: 7}) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("nil context did not panic")
				}
			}()
			tt.call()
		})
	}
}

func TestNewUserContext_UserInfo(t *testing.T) {
	ctx, _ := setupUserContext(t, 42)
	ui := ctx.UserInfo()
	if ui.UserID != 42 {
		t.Fatalf("got UserID=%d, want 42", ui.UserID)
	}
}

func TestUserContext_UserDB_Scopes(t *testing.T) {
	a, db := setupTestApp(t)
	// Create two users with tokens
	db.Create(&models.User{Username: "u1", Password: "x"})
	db.Create(&models.User{Username: "u2", Password: "x"})
	db.Create(&models.Token{UserID: 1, Key: "k1", Name: "t1"})
	db.Create(&models.Token{UserID: 2, Key: "k2", Name: "t2"})

	uctx := NewUserContext(a, &app.UserInfo{UserID: 1})
	impl := uctx.(*userContextImpl)

	var tokens []models.Token
	impl.UserDB().Find(&tokens)
	if len(tokens) != 1 || tokens[0].Key != "k1" {
		t.Fatalf("UserDB should scope to user_id=1, got %d tokens", len(tokens))
	}
}

func TestWithTx_PreservesUserInfo(t *testing.T) {
	ctx, _ := setupUserContext(t, 99)
	db := ctx.GetDB()

	txCtx := ctx.WithTx(db)
	uc, ok := txCtx.(*userContextImpl)
	if !ok {
		t.Fatal("WithTx on UserContext should return *userContextImpl")
	}
	if uc.UserInfo().UserID != 99 {
		t.Fatal("WithTx should preserve UserInfo")
	}
}

func TestRunInTx_Commit(t *testing.T) {
	ctx, db := setupAdminContext(t)
	err := RunInTx(ctx, func(txCtx Context) error {
		return txCtx.GetDB().Create(&models.Setting{Key: "k1", Value: "v1"}).Error
	})
	if err != nil {
		t.Fatalf("RunInTx: %v", err)
	}
	var s models.Setting
	db.First(&s, "key = ?", "k1")
	if s.Value != "v1" {
		t.Fatal("transaction should have committed")
	}
}

func TestRunInTx_Rollback(t *testing.T) {
	ctx, db := setupAdminContext(t)
	err := RunInTx(ctx, func(txCtx Context) error {
		txCtx.GetDB().Create(&models.Setting{Key: "k2", Value: "v2"})
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var count int64
	db.Model(&models.Setting{}).Where("key = ?", "k2").Count(&count)
	if count != 0 {
		t.Fatal("transaction should have rolled back")
	}
}

func TestRunInTx_UserContext(t *testing.T) {
	ctx, _ := setupUserContext(t, 7)
	err := RunInTx(ctx, func(txCtx UserContext) error {
		if txCtx.UserInfo().UserID != 7 {
			t.Fatal("UserInfo should be preserved in tx")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTx[UserContext]: %v", err)
	}
}
