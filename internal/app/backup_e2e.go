//go:build e2e

package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const backupE2EFileRootEnv = "WUKONGIM_BACKUP_E2E_FILE_ROOT"

func init() {
	productionRepositoryLoader := loadAppBackupRepository
	productionGarbageLoader := loadAppBackupGarbageRepository
	productionKeyLoader := loadAppBackupKeyService
	productionClockProbe := newAppBackupClockProbe
	loadAppBackupRepository = func(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int) (appBackupRepository, error) {
		root := strings.TrimSpace(os.Getenv(backupE2EFileRootEnv))
		if root == "" {
			return productionRepositoryLoader(ctx, name, endpoint, region, bucket, prefix, objectLockDays)
		}
		return backupinfra.NewFileRepository(name, filepath.Join(root, name))
	}
	loadAppBackupGarbageRepository = func(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int, roleARN string) (backupinfra.GarbageRepository, error) {
		root := strings.TrimSpace(os.Getenv(backupE2EFileRootEnv))
		if root == "" {
			return productionGarbageLoader(ctx, name, endpoint, region, bucket, prefix, objectLockDays, roleARN)
		}
		repositoryName := strings.TrimSuffix(name, "-gc")
		return backupinfra.NewFileRepository(name, filepath.Join(root, repositoryName))
	}
	loadAppBackupKeyService = func(ctx context.Context, region, endpoint string) (appBackupKeyService, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionKeyLoader(ctx, region, endpoint)
		}
		return newBackupE2EKeyService(), nil
	}
	newAppBackupClockProbe = func(endpoint string) (backupinfra.ClockProbe, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionClockProbe(endpoint)
		}
		return backupE2EClockProbe{}, nil
	}
}

type backupE2EKeyService struct {
	wrappingKey [32]byte
	signingKey  ed25519.PrivateKey
}

func newBackupE2EKeyService() *backupE2EKeyService {
	wrappingKey := sha256.Sum256([]byte("wukongim-backup-e2e-wrapping-key-v1"))
	signingSeed := sha256.Sum256([]byte("wukongim-backup-e2e-signing-key-v1"))
	return &backupE2EKeyService{wrappingKey: wrappingKey, signingKey: ed25519.NewKeyFromSeed(signingSeed[:])}
}

func (s *backupE2EKeyService) GenerateDataKey(_ context.Context, keyID string) (backupartifact.DataKey, error) {
	if s == nil || strings.TrimSpace(keyID) == "" {
		return backupartifact.DataKey{}, fmt.Errorf("backup e2e keys: encryption key id is required")
	}
	plaintext := make([]byte, 32)
	if _, err := rand.Read(plaintext); err != nil {
		return backupartifact.DataKey{}, err
	}
	block, err := aes.NewCipher(s.wrappingKey[:])
	if err != nil {
		return backupartifact.DataKey{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return backupartifact.DataKey{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return backupartifact.DataKey{}, err
	}
	wrapped := append(nonce, aead.Seal(nil, nonce, plaintext, []byte(keyID))...)
	return backupartifact.DataKey{Plaintext: plaintext, Wrapped: wrapped}, nil
}

func (s *backupE2EKeyService) UnwrapDataKey(_ context.Context, keyID string, wrapped []byte) ([]byte, error) {
	if s == nil || strings.TrimSpace(keyID) == "" {
		return nil, fmt.Errorf("backup e2e keys: encryption key id is required")
	}
	block, err := aes.NewCipher(s.wrappingKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(wrapped) <= aead.NonceSize() {
		return nil, fmt.Errorf("backup e2e keys: wrapped key is truncated")
	}
	return aead.Open(nil, wrapped[:aead.NonceSize()], wrapped[aead.NonceSize():], []byte(keyID))
}

func (s *backupE2EKeyService) Sign(_ context.Context, keyID string, message []byte) (backupartifact.ManifestSignature, error) {
	if s == nil || strings.TrimSpace(keyID) == "" {
		return backupartifact.ManifestSignature{}, fmt.Errorf("backup e2e keys: signing key id is required")
	}
	return backupartifact.ManifestSignature{Algorithm: "ED25519_E2E", KeyID: keyID, Value: ed25519.Sign(s.signingKey, message)}, nil
}

func (s *backupE2EKeyService) Verify(_ context.Context, signature backupartifact.ManifestSignature, message []byte) error {
	if s == nil || signature.Algorithm != "ED25519_E2E" || signature.KeyID == "" || !ed25519.Verify(s.signingKey.Public().(ed25519.PublicKey), message, signature.Value) {
		return fmt.Errorf("backup e2e keys: signature verification failed")
	}
	return nil
}

func (s *backupE2EKeyService) Check(ctx context.Context, encryptionKeyID, signingKeyID string) error {
	dataKey, err := s.GenerateDataKey(ctx, encryptionKeyID)
	if err != nil {
		return err
	}
	unwrapped, err := s.UnwrapDataKey(ctx, encryptionKeyID, dataKey.Wrapped)
	if err != nil || !bytes.Equal(unwrapped, dataKey.Plaintext) {
		return fmt.Errorf("backup e2e keys: envelope round trip failed: %w", err)
	}
	probe := []byte("wukongim-backup-e2e-key-doctor-v1")
	signature, err := s.Sign(ctx, signingKeyID, probe)
	if err != nil {
		return err
	}
	return s.Verify(ctx, signature, probe)
}

type backupE2EClockProbe struct{}

func (backupE2EClockProbe) UTC(context.Context) (time.Time, error) {
	return time.Now().UTC(), nil
}
