package api_access_grant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterapi "github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGrantRetryStopsWhenDeadlineExpiresBeforeNextTransaction(t *testing.T) {
	fx := newGrantFixture(t)
	requestCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	ctx := dao.NewContextWithContext(grantTestApp{db: fx.db}, requestCtx)
	busyErr := errors.New("database is locked")
	attempts := 0
	replaceGrantTransactionAttempt(t, func(attemptCtx dao.Context, _ func(dao.Context) error) error {
		attempts++
		<-attemptCtx.GetCoreDB().Statement.Context.Done()
		return busyErr
	})

	err := runGrantTransaction(ctx, func(dao.Context) error { return nil })

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, attempts)
}

func TestGrantRetryDoesNotRetryNonSQLiteBusyError(t *testing.T) {
	fx := newGrantFixture(t)
	db := fx.db.Session(&gorm.Session{NewDB: true})
	config := *db.Config
	config.Dialector = namedGrantTestDialector{Dialector: config.Dialector, name: "postgres"}
	db.Config = &config
	ctx := dao.NewContext(grantTestApp{db: db})
	busyErr := errors.New("database is locked")
	attempts := 0
	replaceGrantTransactionAttempt(t, func(dao.Context, func(dao.Context) error) error {
		attempts++
		return busyErr
	})

	err := runGrantTransaction(ctx, func(dao.Context) error { return nil })

	require.Same(t, busyErr, err)
	require.Equal(t, 1, attempts)
}

func TestGrantRetryDoesNotRetrySQLiteNonBusyError(t *testing.T) {
	fx := newGrantFixture(t)
	nonBusyErr := errors.New("role permission create failed")
	attempts := 0
	replaceGrantTransactionAttempt(t, func(dao.Context, func(dao.Context) error) error {
		attempts++
		return nonBusyErr
	})

	err := runGrantTransaction(fx.ctx, func(dao.Context) error { return nil })

	require.Same(t, nonBusyErr, err)
	require.Equal(t, 1, attempts)
}

func TestGrantRetryReturnsEighthSQLiteBusyErrorAfterExhaustion(t *testing.T) {
	fx := newGrantFixture(t)
	attemptErrors := make([]error, 8)
	for i := range attemptErrors {
		attemptErrors[i] = fmt.Errorf("database is locked on attempt %d", i+1)
	}
	attempts := 0
	replaceGrantTransactionAttempt(t, func(dao.Context, func(dao.Context) error) error {
		attempts++
		if attempts > len(attemptErrors) {
			return fmt.Errorf("database is locked on unexpected attempt %d", attempts)
		}
		return attemptErrors[attempts-1]
	})

	err := runGrantTransaction(fx.ctx, func(dao.Context) error { return nil })

	require.Same(t, attemptErrors[7], err)
	require.NotErrorIs(t, err, attemptErrors[0])
	require.Equal(t, 8, attempts)
}

func TestGrantHandlerPublishesCommittedReplaceAfterRequestCancellation(t *testing.T) {
	fx := newGrantFixture(t)
	application := app.NewApplication()
	application.SetCoreDB(fx.db)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	committed := make(chan struct{})
	publisher := &committedGrantPublisher{
		expectedPrincipal: principal,
		expectedRoleKey:   models.ManagedAPIRoleKey(principal.Type, principal.ID),
		expectedAction:    events.ActionUpdate,
	}
	handler := &Handler{
		App:       application,
		Publisher: publisher,
		Writer:    &commitCancelGrantWriter{delegate: GrantWriter{}, committed: committed},
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	appCtx := &app.Context{App: application, OwnerContext: requestCtx}
	result := make(chan grantHandlerResult, 1)

	go func() {
		grant, err := handler.Replace(appCtx, ReplaceRequest{
			PrincipalType: principal.Type,
			PrincipalID:   principal.ID,
			APIServiceID:  fx.serviceA.ID,
			Scope:         GrantScopeService,
		})
		result <- grantHandlerResult{grant: grant, err: err}
	}()

	got := cancelGrantRequestAfterCommit(t, committed, result, cancelRequest)
	require.NoError(t, got.err)
	require.Equal(t, ConfiguredGrant{
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		APIServiceID:  fx.serviceA.ID,
		Scope:         GrantScopeService,
	}, got.grant)
	require.Equal(t, []string{"role:update", "principal:user:1"}, publisher.calls)
}

func TestGrantHandlerPublishesCommittedDeleteAfterRequestCancellation(t *testing.T) {
	fx := newGrantFixture(t)
	application := app.NewApplication()
	application.SetCoreDB(fx.db)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))
	var role models.Role
	require.NoError(t, fx.db.Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).First(&role).Error)
	committed := make(chan struct{})
	publisher := &committedGrantPublisher{
		expectedPrincipal: principal,
		expectedRoleKey:   models.ManagedAPIRoleKey(principal.Type, principal.ID),
		expectedRoleID:    role.ID,
		expectedAction:    events.ActionDelete,
	}
	handler := &Handler{
		App:       application,
		Publisher: publisher,
		Writer:    &commitCancelGrantWriter{delegate: GrantWriter{}, committed: committed},
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	appCtx := &app.Context{App: application, OwnerContext: requestCtx}
	result := make(chan grantHandlerResult, 1)

	go func() {
		status, err := handler.Delete(appCtx, DeleteRequest{
			PrincipalType: principal.Type,
			PrincipalID:   principal.ID,
			APIServiceID:  fx.serviceA.ID,
		})
		result <- grantHandlerResult{status: status, err: err}
	}()

	got := cancelGrantRequestAfterCommit(t, committed, result, cancelRequest)
	require.NoError(t, got.err)
	require.Equal(t, masterapi.StatusResponse{Status: "ok"}, got.status)
	require.Equal(t, []string{"role:delete", "principal:user:1"}, publisher.calls)
}

func TestGrantHTTPDatabaseFailureRollsBackWithoutPublishingOrRetrying(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))
	application := app.NewApplication()
	application.SetCoreDB(fx.db)
	publisher := &grantPublisherProbe{db: fx.db}
	handler := &Handler{App: application, Publisher: publisher}
	router := gin.New()
	router.PUT("/grants/:principal_type/:principal_id/services/:service_id", masterapi.Adapt(masterapi.NewAdapter(nil, nil, application), masterapi.BindURIAndJSON, handler.Replace))
	callbackName := "test:fail_http_grant_role_permission_create"
	sentinel := errors.New("role permission create rejected")
	callbackAttempts := 0
	require.NoError(t, fx.db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "role_permissions" {
			callbackAttempts++
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = fx.db.Callback().Create().Remove(callbackName) })

	failed := grantHTTPRequest(router, http.MethodPut, "/grants/user/1/services/1", map[string]any{
		"scope":     "routes",
		"route_ids": []uint{fx.routeA1.ID},
	})

	require.Empty(t, publisher.calls)
	require.Equal(t, http.StatusBadRequest, failed.Code, failed.Body.String())
	require.Equal(t, 1, callbackAttempts)
	fx.requireManagedGrant(t, principal.Type, principal.ID, fx.serviceA.ID, GrantScopeService, nil)

	invalid := grantHTTPRequest(router, http.MethodPut, "/grants/user/1/services/1", map[string]any{
		"scope":     "routes",
		"route_ids": []uint{},
	})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	require.Equal(t, 1, callbackAttempts)
	require.Empty(t, publisher.calls)
}

func replaceGrantTransactionAttempt(t *testing.T, attempt func(dao.Context, func(dao.Context) error) error) {
	t.Helper()
	previous := grantTransactionAttempt
	grantTransactionAttempt = attempt
	t.Cleanup(func() { grantTransactionAttempt = previous })
}

type namedGrantTestDialector struct {
	gorm.Dialector
	name string
}

func (d namedGrantTestDialector) Name() string { return d.name }

type commitCancelGrantWriter struct {
	delegate  GrantWriter
	committed chan<- struct{}
}

func (w *commitCancelGrantWriter) Replace(ctx dao.Context, principal PrincipalRef, serviceID uint, scope GrantScope, routeIDs []uint) (ConfiguredGrant, error) {
	grant, err := w.delegate.Replace(ctx, principal, serviceID, scope, routeIDs)
	if err != nil {
		return ConfiguredGrant{}, err
	}
	w.waitForRequestCancellation(ctx)
	return grant, nil
}

func (w *commitCancelGrantWriter) Delete(ctx dao.Context, principal PrincipalRef, serviceID uint) error {
	if err := w.delegate.Delete(ctx, principal, serviceID); err != nil {
		return err
	}
	w.waitForRequestCancellation(ctx)
	return nil
}

func (w *commitCancelGrantWriter) waitForRequestCancellation(ctx dao.Context) {
	close(w.committed)
	<-ctx.GetCoreDB().Statement.Context.Done()
}

type grantHandlerResult struct {
	grant  ConfiguredGrant
	status masterapi.StatusResponse
	err    error
}

func cancelGrantRequestAfterCommit(
	t *testing.T,
	committed <-chan struct{},
	result <-chan grantHandlerResult,
	cancelRequest context.CancelFunc,
) grantHandlerResult {
	t.Helper()
	select {
	case <-committed:
		cancelRequest()
	case got := <-result:
		cancelRequest()
		require.FailNowf(t, "handler returned before the commit barrier", "error: %v", got.err)
	case <-time.After(time.Second):
		cancelRequest()
		require.FailNow(t, "writer did not commit before timeout")
	}
	select {
	case got := <-result:
		return got
	case <-time.After(time.Second):
		require.FailNow(t, "handler did not finish after request cancellation")
		return grantHandlerResult{}
	}
}

type committedGrantPublisher struct {
	expectedPrincipal PrincipalRef
	expectedRoleKey   string
	expectedRoleID    uint
	expectedAction    string
	calls             []string
}

func (p *committedGrantPublisher) PublishRole(ctx context.Context, query dao.AdminQuery, action string, roleID uint) error {
	if err := requireDetachedPublishContext(ctx); err != nil {
		return err
	}
	if len(p.calls) != 0 {
		return fmt.Errorf("role publication was not first: %v", p.calls)
	}
	if action != p.expectedAction {
		return fmt.Errorf("role action = %q, want %q", action, p.expectedAction)
	}
	if p.expectedRoleID != 0 && roleID != p.expectedRoleID {
		return fmt.Errorf("role id = %d, want %d", roleID, p.expectedRoleID)
	}
	role, err := query.APIRBAC().GetRoleByID(roleID)
	if action == events.ActionDelete {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("deleted committed role lookup error = %v, want record not found", err)
		}
	} else {
		if err != nil {
			return fmt.Errorf("read committed role through publisher query: %w", err)
		}
		if role.Key != p.expectedRoleKey || role.Kind != models.APIRoleKindManaged {
			return fmt.Errorf("committed role = {%q %q}, want {%q %q}", role.Key, role.Kind, p.expectedRoleKey, models.APIRoleKindManaged)
		}
	}
	p.calls = append(p.calls, "role:"+action)
	return nil
}

func (p *committedGrantPublisher) PublishRoleBindingChange(ctx context.Context, _ dao.AdminQuery, principalType models.APIPrincipalType, principalID uint) error {
	if err := requireDetachedPublishContext(ctx); err != nil {
		return err
	}
	wantRoleCall := "role:" + p.expectedAction
	if len(p.calls) != 1 || p.calls[0] != wantRoleCall {
		return fmt.Errorf("principal publication did not follow role publication: %v", p.calls)
	}
	if principalType != p.expectedPrincipal.Type || principalID != p.expectedPrincipal.ID {
		return fmt.Errorf("principal = %s:%d, want %s:%d", principalType, principalID, p.expectedPrincipal.Type, p.expectedPrincipal.ID)
	}
	p.calls = append(p.calls, fmt.Sprintf("principal:%s:%d", principalType, principalID))
	return nil
}

func requireDetachedPublishContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish context is canceled: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("publish context has no deadline")
	}
	if !deadline.After(time.Now()) {
		return fmt.Errorf("publish context deadline already expired: %s", deadline)
	}
	return nil
}
