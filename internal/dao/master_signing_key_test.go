package dao

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const masterSigningKeyAttemptCallback = "test:count-master-signing-key-insert-attempts"

type lockedMasterSigningKeyDB struct {
	db                 *gorm.DB
	sqlDB              *sql.DB
	lockConn           *sql.Conn
	callbackRegistered bool
	attempts           atomic.Int32
	attemptCh          chan int32
}

type signingKeyLoadResult struct {
	key *models.MasterSigningKey
	err error
}

func newValidMasterSigningKey(t *testing.T) models.MasterSigningKey {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	digest := sha256.Sum256(publicKey)
	one := uint8(1)
	return models.MasterSigningKey{
		KeyID:      hex.EncodeToString(digest[:]),
		PublicKey:  append([]byte(nil), publicKey...),
		PrivateKey: append([]byte(nil), privateKey...),
		ActiveSlot: &one,
	}
}

func requireValidMasterSigningKey(t *testing.T, key *models.MasterSigningKey) {
	t.Helper()
	require.NotNil(t, key)
	require.Len(t, key.KeyID, 64)
	require.Len(t, key.PublicKey, ed25519.PublicKeySize)
	require.Len(t, key.PrivateKey, ed25519.PrivateKeySize)
	require.NotNil(t, key.ActiveSlot)
	require.Equal(t, uint8(1), *key.ActiveSlot)
	digest := sha256.Sum256(key.PublicKey)
	require.Equal(t, hex.EncodeToString(digest[:]), key.KeyID)
	require.True(t, bytes.Equal(ed25519.PrivateKey(key.PrivateKey).Public().(ed25519.PublicKey), key.PublicKey))
}

func newLockedMasterSigningKeyDB(t *testing.T) *lockedMasterSigningKeyDB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "busy-retry.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	fixture := &lockedMasterSigningKeyDB{
		db:        db,
		sqlDB:     sqlDB,
		attemptCh: make(chan int32, signingKeyBusyRetries+1),
	}
	t.Cleanup(func() { fixture.closeResources(t) })
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	require.NoError(t, db.AutoMigrate(&models.MasterSigningKey{}))

	lockConn, err := sqlDB.Conn(t.Context())
	require.NoError(t, err)
	_, err = lockConn.ExecContext(t.Context(), "BEGIN IMMEDIATE")
	if err != nil {
		_ = lockConn.Close()
		t.Fatalf("acquire SQLite write lock: %v", err)
	}
	fixture.lockConn = lockConn

	registerErr := db.Callback().Create().After("gorm:create").Register(masterSigningKeyAttemptCallback, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "master_signing_keys" {
			return
		}
		// GORM runs callbacks registered after gorm:create even when the real INSERT returns SQLITE_BUSY.
		attempt := fixture.attempts.Add(1)
		fixture.attemptCh <- attempt
	})
	fixture.callbackRegistered = true
	require.NoError(t, registerErr)
	return fixture
}

func (f *lockedMasterSigningKeyDB) closeResources(t *testing.T) {
	t.Helper()
	if f.callbackRegistered {
		if err := f.db.Callback().Create().Remove(masterSigningKeyAttemptCallback); err != nil {
			t.Errorf("remove signing-key attempt callback: %v", err)
		}
		f.callbackRegistered = false
	}
	if f.lockConn != nil {
		if _, err := f.lockConn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
			t.Errorf("release SQLite write lock: %v", err)
		}
		if err := f.lockConn.Close(); err != nil {
			t.Errorf("close SQLite write-lock connection: %v", err)
		}
		f.lockConn = nil
	}
	if f.sqlDB != nil {
		if err := f.sqlDB.Close(); err != nil {
			t.Errorf("close busy-retry database: %v", err)
		}
		f.sqlDB = nil
	}
}

func (f *lockedMasterSigningKeyDB) requireAttempt(t *testing.T, want int32) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case got := <-f.attemptCh:
		require.Equal(t, want, got)
	case <-timer.C:
		t.Fatalf("timed out waiting for signing-key INSERT attempt %d; observed %d", want, f.attempts.Load())
	}
}

func (f *lockedMasterSigningKeyDB) stopCountingAttempts(t *testing.T) {
	t.Helper()
	if !f.callbackRegistered {
		return
	}
	err := f.db.Callback().Create().Remove(masterSigningKeyAttemptCallback)
	f.callbackRegistered = false
	require.NoError(t, err)
}

func (f *lockedMasterSigningKeyDB) releaseWriteLock(t *testing.T) {
	t.Helper()
	if f.lockConn == nil {
		return
	}
	_, rollbackErr := f.lockConn.ExecContext(t.Context(), "ROLLBACK")
	closeErr := f.lockConn.Close()
	f.lockConn = nil
	require.NoError(t, rollbackErr)
	require.NoError(t, closeErr)
}

func TestMasterSigningKeyStoreCreatesAndLoadsOneActiveIdentity(t *testing.T) {
	db := setupTestDB(t)
	store := NewMasterSigningKeyStore(db)

	first, err := store.LoadOrCreateActive(context.Background())
	require.NoError(t, err)
	requireValidMasterSigningKey(t, first)

	second, err := store.LoadOrCreateActive(context.Background())
	require.NoError(t, err)
	requireValidMasterSigningKey(t, second)
	require.Equal(t, first.KeyID, second.KeyID)
	require.Equal(t, first.PublicKey, second.PublicKey)
	require.Equal(t, first.PrivateKey, second.PrivateKey)

	var activeCount int64
	require.NoError(t, db.Model(&models.MasterSigningKey{}).Where("active_slot = ?", 1).Count(&activeCount).Error)
	require.Equal(t, int64(1), activeCount)
}

func TestMasterSigningKeyStoreLoadsExistingIdentityWithoutReplacingIt(t *testing.T) {
	db := setupTestDB(t)
	existing := newValidMasterSigningKey(t)
	require.NoError(t, db.Create(&existing).Error)

	got, err := NewMasterSigningKeyStore(db).LoadOrCreateActive(context.Background())
	require.NoError(t, err)
	require.Equal(t, existing.KeyID, got.KeyID)
	require.Equal(t, existing.PublicKey, got.PublicKey)
	require.Equal(t, existing.PrivateKey, got.PrivateKey)

	var count int64
	require.NoError(t, db.Model(&models.MasterSigningKey{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestMasterSigningKeyStoreReturnsDefensiveCopies(t *testing.T) {
	db := setupTestDB(t)
	store := NewMasterSigningKeyStore(db)

	first, err := store.LoadOrCreateActive(context.Background())
	require.NoError(t, err)
	wantKeyID := first.KeyID
	wantPublic := append([]byte(nil), first.PublicKey...)
	wantPrivate := append([]byte(nil), first.PrivateKey...)
	first.KeyID = strings.Repeat("0", 64)
	first.PublicKey[0] ^= 0xff
	first.PrivateKey[0] ^= 0xff
	*first.ActiveSlot = 9

	second, err := store.LoadOrCreateActive(context.Background())
	require.NoError(t, err)
	require.Equal(t, wantKeyID, second.KeyID)
	require.Equal(t, wantPublic, second.PublicKey)
	require.Equal(t, wantPrivate, second.PrivateKey)
	require.Equal(t, uint8(1), *second.ActiveSlot)
}

func TestMasterSigningKeyStoreConcurrentCreationIsAtomic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "signing-keys.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(20)
	require.NoError(t, models.AutoMigrate(db))

	store := NewMasterSigningKeyStore(db)
	start := make(chan struct{})
	p := pool.NewWithResults[*models.MasterSigningKey]().WithErrors().WithCollectErrored().WithMaxGoroutines(20)
	for range 20 {
		p.Go(func() (*models.MasterSigningKey, error) {
			<-start
			return store.LoadOrCreateActive(context.Background())
		})
	}
	close(start)
	keys, err := p.Wait()
	require.NoError(t, err)
	require.Len(t, keys, 20)

	winnerID := keys[0].KeyID
	for _, key := range keys {
		requireValidMasterSigningKey(t, key)
		require.Equal(t, winnerID, key.KeyID)
	}

	var allRows int64
	require.NoError(t, db.Model(&models.MasterSigningKey{}).Count(&allRows).Error)
	require.Equal(t, int64(1), allRows)
	var activeRows int64
	require.NoError(t, db.Model(&models.MasterSigningKey{}).Where("active_slot = ?", 1).Count(&activeRows).Error)
	require.Equal(t, int64(1), activeRows)
}

func TestMasterSigningKeyStoreRejectsCorruptActiveRowsWithoutReplacingThem(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *models.MasterSigningKey)
	}{
		{name: "short public key", mutate: func(_ *testing.T, key *models.MasterSigningKey) {
			key.PublicKey = key.PublicKey[:ed25519.PublicKeySize-1]
		}},
		{name: "short private key", mutate: func(_ *testing.T, key *models.MasterSigningKey) {
			key.PrivateKey = key.PrivateKey[:ed25519.PrivateKeySize-1]
		}},
		{name: "public private mismatch", mutate: func(t *testing.T, key *models.MasterSigningKey) {
			_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			key.PrivateKey = otherPrivate
		}},
		{name: "key id mismatch", mutate: func(_ *testing.T, key *models.MasterSigningKey) { key.KeyID = strings.Repeat("0", 64) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTestDB(t)
			corrupt := newValidMasterSigningKey(t)
			tc.mutate(t, &corrupt)
			require.NoError(t, db.Create(&corrupt).Error)
			storedPrivate := append([]byte(nil), corrupt.PrivateKey...)

			got, err := NewMasterSigningKeyStore(db).LoadOrCreateActive(context.Background())
			require.Error(t, err)
			require.Nil(t, got)
			require.NotContains(t, err.Error(), string(storedPrivate))
			require.NotContains(t, err.Error(), base64.StdEncoding.EncodeToString(storedPrivate))

			var rows []models.MasterSigningKey
			require.NoError(t, db.Find(&rows).Error)
			require.Len(t, rows, 1, "a corrupt active identity must not be rotated or overwritten implicitly")
			require.Equal(t, storedPrivate, rows[0].PrivateKey)
		})
	}
}

func TestMasterSigningKeyStoreRejectsNilCanceledAndFailedDatabaseInputs(t *testing.T) {
	t.Run("nil database", func(t *testing.T) {
		key, err := NewMasterSigningKeyStore(nil).LoadOrCreateActive(context.Background())
		require.Error(t, err)
		require.Nil(t, key)
	})

	t.Run("nil context", func(t *testing.T) {
		key, err := NewMasterSigningKeyStore(setupTestDB(t)).LoadOrCreateActive(nil)
		require.Error(t, err)
		require.Nil(t, key)
	})

	t.Run("canceled context", func(t *testing.T) {
		db := setupTestDB(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		key, err := NewMasterSigningKeyStore(db).LoadOrCreateActive(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, key)
		var count int64
		require.NoError(t, db.Model(&models.MasterSigningKey{}).Count(&count).Error)
		require.Zero(t, count)
	})

	t.Run("closed database", func(t *testing.T) {
		db := setupTestDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		key, loadErr := NewMasterSigningKeyStore(db).LoadOrCreateActive(context.Background())
		require.Error(t, loadErr)
		require.Nil(t, key)
	})
}

func TestMasterSigningKeyStoreBusyRetryCanBeCanceled(t *testing.T) {
	fixture := newLockedMasterSigningKeyDB(t)
	store := NewMasterSigningKeyStore(fixture.db)
	ctx, cancel := context.WithCancel(t.Context())

	p := pool.NewWithResults[signingKeyLoadResult]().WithMaxGoroutines(1)
	p.Go(func() signingKeyLoadResult {
		key, err := store.LoadOrCreateActive(ctx)
		return signingKeyLoadResult{key: key, err: err}
	})
	workerJoined := false
	defer func() {
		cancel()
		if !workerJoined {
			p.Wait()
		}
	}()
	fixture.requireAttempt(t, 1)
	fixture.requireAttempt(t, 2)
	cancel()
	results := p.Wait()
	workerJoined = true
	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].err, context.Canceled)
	require.Nil(t, results[0].key)
	attempts := fixture.attempts.Load()
	require.GreaterOrEqual(t, attempts, int32(2))
	require.Less(t, attempts, int32(signingKeyBusyRetries))
	require.Equal(t, 1, fixture.sqlDB.Stats().InUse, "only the write-lock connection may remain checked out")
	t.Logf("observed %d real busy INSERT attempts before cancellation", attempts)

	fixture.stopCountingAttempts(t)
	fixture.releaseWriteLock(t)
	key, err := store.LoadOrCreateActive(t.Context())
	require.NoError(t, err)
	requireValidMasterSigningKey(t, key)
}

func TestMasterSigningKeyStoreBusyRetryIsBounded(t *testing.T) {
	fixture := newLockedMasterSigningKeyDB(t)
	store := NewMasterSigningKeyStore(fixture.db)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	key, err := store.LoadOrCreateActive(ctx)
	require.Error(t, err)
	require.Nil(t, key)
	require.False(t, errors.Is(err, context.DeadlineExceeded), "retry limit must win before the guard deadline")
	require.False(t, errors.Is(err, context.Canceled))
	require.True(t, isSQLiteBusyError(err), "expected the real SQLite busy error, got %v", err)
	attempts := fixture.attempts.Load()
	require.Equal(t, int32(signingKeyBusyRetries), attempts)
	require.Equal(t, 1, fixture.sqlDB.Stats().InUse, "only the write-lock connection may remain checked out")
	t.Logf("observed %d real busy INSERT attempts before the retry limit", attempts)

	fixture.stopCountingAttempts(t)
	fixture.releaseWriteLock(t)
	key, err = store.LoadOrCreateActive(t.Context())
	require.NoError(t, err)
	requireValidMasterSigningKey(t, key)
}

func TestMasterSigningKeyIsNotExposedThroughAdminDAO(t *testing.T) {
	adminQuery := reflect.TypeOf((*AdminQuery)(nil)).Elem()
	_, exposedByQuery := adminQuery.MethodByName("MasterSigningKey")
	require.False(t, exposedByQuery)
	adminMutation := reflect.TypeOf((*AdminMutation)(nil)).Elem()
	_, exposedByMutation := adminMutation.MethodByName("MasterSigningKey")
	require.False(t, exposedByMutation)
}
