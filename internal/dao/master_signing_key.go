package dao

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const (
	activeMasterSigningKeySlot = uint8(1)
	signingKeyBusyRetries      = 5
)

var errCorruptMasterSigningKey = errors.New("master signing key store: corrupt active key")

type SigningKeyStore interface {
	LoadOrCreateActive(ctx context.Context) (*models.MasterSigningKey, error)
}

type masterSigningKeyStore struct {
	db *gorm.DB
}

func NewMasterSigningKeyStore(db *gorm.DB) SigningKeyStore {
	return &masterSigningKeyStore{db: db}
}

func (s *masterSigningKeyStore) LoadOrCreateActive(ctx context.Context) (*models.MasterSigningKey, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("master signing key store: database is required")
	}
	if ctx == nil {
		return nil, errors.New("master signing key store: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	quietDB := s.db.Session(&gorm.Session{Logger: s.db.Logger.LogMode(gormlogger.Silent)})
	for attempt := 0; attempt < signingKeyBusyRetries; attempt++ {
		winner, err := loadOrCreateActiveSigningKey(ctx, quietDB)
		if err == nil {
			return cloneMasterSigningKey(winner), nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if !isSQLiteBusyError(err) || attempt == signingKeyBusyRetries-1 {
			return nil, fmt.Errorf("master signing key store: load active key: %w", err)
		}
		if err := waitForSigningKeyRetry(ctx, time.Duration(attempt+1)*5*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("master signing key store: retry limit reached")
}

func loadOrCreateActiveSigningKey(ctx context.Context, db *gorm.DB) (*models.MasterSigningKey, error) {
	var winner models.MasterSigningKey
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate, err := generateMasterSigningKey()
		if err != nil {
			return errors.New("generate signing key failed")
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(candidate).Error; err != nil {
			return err
		}
		if err := tx.Where("active_slot = ?", activeMasterSigningKeySlot).Take(&winner).Error; err != nil {
			return err
		}
		return validateMasterSigningKey(&winner)
	})
	if err != nil {
		return nil, err
	}
	return &winner, nil
}

func generateMasterSigningKey() (*models.MasterSigningKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(publicKey)
	activeSlot := activeMasterSigningKeySlot
	return &models.MasterSigningKey{
		KeyID:      hex.EncodeToString(digest[:]),
		PublicKey:  append([]byte(nil), publicKey...),
		PrivateKey: append([]byte(nil), privateKey...),
		ActiveSlot: &activeSlot,
	}, nil
}

func validateMasterSigningKey(key *models.MasterSigningKey) error {
	if key == nil ||
		key.ActiveSlot == nil ||
		*key.ActiveSlot != activeMasterSigningKeySlot ||
		len(key.PublicKey) != ed25519.PublicKeySize ||
		len(key.PrivateKey) != ed25519.PrivateKeySize {
		return errCorruptMasterSigningKey
	}
	privatePublic := ed25519.PrivateKey(key.PrivateKey).Public().(ed25519.PublicKey)
	if !bytes.Equal(privatePublic, key.PublicKey) {
		return errCorruptMasterSigningKey
	}
	digest := sha256.Sum256(key.PublicKey)
	if key.KeyID != hex.EncodeToString(digest[:]) {
		return errCorruptMasterSigningKey
	}
	return nil
}

func cloneMasterSigningKey(key *models.MasterSigningKey) *models.MasterSigningKey {
	if key == nil {
		return nil
	}
	cloned := *key
	cloned.PublicKey = append([]byte(nil), key.PublicKey...)
	cloned.PrivateKey = append([]byte(nil), key.PrivateKey...)
	if key.ActiveSlot != nil {
		activeSlot := *key.ActiveSlot
		cloned.ActiveSlot = &activeSlot
	}
	return &cloned
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "sqlite_busy")
}

func waitForSigningKeyRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
