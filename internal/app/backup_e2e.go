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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	backupE2EFileRootEnv       = "WUKONGIM_BACKUP_E2E_FILE_ROOT"
	backupE2ERemoteLatencyEnv  = "WUKONGIM_BACKUP_E2E_REMOTE_LATENCY"
	backupE2ELatencyTriggerEnv = "WUKONGIM_BACKUP_E2E_LATENCY_TRIGGER"
	backupE2ECorruptionDirEnv  = "WUKONGIM_BACKUP_E2E_CORRUPTION_DIR"
	backupE2EPinTriggerEnv     = "WUKONGIM_BACKUP_E2E_PIN_PRESSURE_TRIGGER"
)

func init() {
	productionRepositoryLoader := loadAppBackupRepository
	productionRepairLoader := loadAppBackupRepairRepository
	productionGarbageLoader := loadAppBackupGarbageRepository
	productionKeyLoader := loadAppBackupKeyService
	productionClockProbe := newAppBackupClockProbe
	productionPinDecorator := decorateAppBackupSourcePinManager
	decorateAppBackupSourcePinManager = func(
		manager runtimebackup.SourcePinManager,
	) runtimebackup.SourcePinManager {
		manager = productionPinDecorator(manager)
		trigger := strings.TrimSpace(os.Getenv(backupE2EPinTriggerEnv))
		if trigger == "" {
			return manager
		}
		return &backupE2ESourcePinManager{
			SourcePinManager: manager,
			trigger:          trigger,
		}
	}
	loadAppBackupRepository = func(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int) (appBackupRepository, error) {
		root := strings.TrimSpace(os.Getenv(backupE2EFileRootEnv))
		if root == "" {
			return productionRepositoryLoader(ctx, name, endpoint, region, bucket, prefix, objectLockDays)
		}
		repository, err := backupinfra.NewFileRepository(
			name, filepath.Join(root, name),
		)
		if err != nil {
			return nil, err
		}
		delay, err := backupE2ERemoteLatency()
		if err != nil {
			return nil, err
		}
		corruptionDir := strings.TrimSpace(
			os.Getenv(backupE2ECorruptionDirEnv),
		)
		if delay == 0 && corruptionDir == "" {
			return repository, nil
		}
		return &backupE2EDelayedRepository{
			appBackupRepository: repository,
			delay:               delay,
			trigger:             strings.TrimSpace(os.Getenv(backupE2ELatencyTriggerEnv)),
			corruptionDir:       corruptionDir,
		}, nil
	}
	loadAppBackupRepairRepository = func(
		ctx context.Context,
		repository appBackupRepository,
		endpoint, region, roleARN string,
	) (backupartifact.RepairRepository, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionRepairLoader(
				ctx, repository, endpoint, region, roleARN,
			)
		}
		fileRepository, ok := backupE2EFileRepository(repository)
		if !ok {
			return nil, fmt.Errorf(
				"backup e2e: repair repository is not file-backed",
			)
		}
		if wrapped, ok := repository.(*backupE2EDelayedRepository); ok &&
			wrapped.corruptionDir != "" {
			return &backupE2ERepairRepository{
				RepairRepository: fileRepository,
				trigger: filepath.Join(
					wrapped.corruptionDir,
					fileRepository.Name()+".corrupt",
				),
				sticky: filepath.Join(
					wrapped.corruptionDir, "sticky.key",
				),
			}, nil
		}
		return fileRepository, nil
	}
	loadAppBackupGarbageRepository = func(
		ctx context.Context,
		name, endpoint, region, bucket, prefix string,
		objectLockDays int,
		roleARN string,
	) (backupinfra.GenerationGarbageRepository, error) {
		root := strings.TrimSpace(os.Getenv(backupE2EFileRootEnv))
		if root == "" {
			return productionGarbageLoader(
				ctx, name, endpoint, region, bucket, prefix,
				objectLockDays, roleARN,
			)
		}
		return backupinfra.NewFileRepository(
			name, filepath.Join(root, name),
		)
	}
	loadAppBackupKeyService = func(ctx context.Context, region, endpoint string) (appBackupKeyService, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionKeyLoader(ctx, region, endpoint)
		}
		delay, err := backupE2ERemoteLatency()
		if err != nil {
			return nil, err
		}
		service := newBackupE2EKeyService()
		if delay == 0 {
			return service, nil
		}
		return &backupE2EDelayedKeyService{
			appBackupKeyService: service,
			delay:               delay,
			trigger:             strings.TrimSpace(os.Getenv(backupE2ELatencyTriggerEnv)),
		}, nil
	}
	newAppBackupClockProbe = func(endpoint string) (backupinfra.ClockProbe, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionClockProbe(endpoint)
		}
		return backupE2EClockProbe{}, nil
	}
}

type backupE2EDelayedRepository struct {
	appBackupRepository
	delay         time.Duration
	trigger       string
	corruptionDir string
}

func (r *backupE2EDelayedRepository) PutImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(r.delay, r.trigger)); err != nil {
		return err
	}
	return r.appBackupRepository.PutImmutable(ctx, key, size, checksum, body)
}

func (r *backupE2EDelayedRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(r.delay, r.trigger)); err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	reader, object, err := r.appBackupRepository.Open(ctx, key)
	if err != nil || !r.consumeCorruptionTrigger(key) {
		return reader, object, err
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, backupartifact.RepositoryObject{}, readErr
	}
	if closeErr != nil {
		return nil, backupartifact.RepositoryObject{}, closeErr
	}
	if len(body) == 0 {
		return nil, backupartifact.RepositoryObject{}, fmt.Errorf(
			"backup e2e: selected corruption object is empty",
		)
	}
	body[len(body)/2] ^= 0xff
	return io.NopCloser(bytes.NewReader(body)), object, nil
}

func (r *backupE2EDelayedRepository) consumeCorruptionTrigger(key string) bool {
	if r == nil || r.corruptionDir == "" {
		return false
	}
	slashKey := filepath.ToSlash(key)
	if (!strings.Contains(slashKey, "/segments/") &&
		!strings.HasPrefix(slashKey, "segments/")) ||
		!strings.Contains(slashKey, "/payloads/") ||
		!strings.HasSuffix(slashKey, ".bin") {
		return false
	}
	trigger := filepath.Join(
		r.corruptionDir, r.appBackupRepository.Name()+".corrupt",
	)
	body, err := os.ReadFile(trigger)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(body)) {
	case "once":
		return os.Remove(trigger) == nil
	case "persistent":
		return true
	case "sticky":
		return consumeBackupE2EStickyKey(
			filepath.Join(r.corruptionDir, "sticky.key"), key,
		)
	default:
		return false
	}
}

func consumeBackupE2EStickyKey(path, key string) bool {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, writeErr := file.WriteString(key)
		closeErr := file.Close()
		return writeErr == nil && closeErr == nil
	}
	if !os.IsExist(err) {
		return false
	}
	selected, readErr := os.ReadFile(path)
	return readErr == nil && string(selected) == key
}

func (r *backupE2EDelayedRepository) Stat(
	ctx context.Context,
	key string,
) (backupartifact.RepositoryObject, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(r.delay, r.trigger)); err != nil {
		return backupartifact.RepositoryObject{}, err
	}
	return r.appBackupRepository.Stat(ctx, key)
}

func (r *backupE2EDelayedRepository) Check(ctx context.Context) error {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(r.delay, r.trigger)); err != nil {
		return err
	}
	return r.appBackupRepository.Check(ctx)
}

func (r *backupE2EDelayedRepository) ListErasureLedgerCommitKeys(
	ctx context.Context,
	namespace string,
) ([]string, error) {
	if err := waitBackupE2ELatency(
		ctx, backupE2EActiveDelay(r.delay, r.trigger),
	); err != nil {
		return nil, err
	}
	lister, ok := r.appBackupRepository.(backupinfra.ErasureLedgerCommitLister)
	if !ok {
		return nil, fmt.Errorf(
			"backup e2e: repository cannot list erasure ledger commits",
		)
	}
	return lister.ListErasureLedgerCommitKeys(ctx, namespace)
}

type backupE2EDelayedKeyService struct {
	appBackupKeyService
	delay   time.Duration
	trigger string
}

type backupE2ERepairRepository struct {
	backupartifact.RepairRepository
	trigger string
	sticky  string
}

func (r *backupE2ERepairRepository) RepairImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	if err := r.RepairRepository.RepairImmutable(
		ctx, key, size, checksum, body,
	); err != nil {
		return err
	}
	for _, path := range []string{r.trigger, r.sticky} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *backupE2EDelayedKeyService) GenerateDataKey(
	ctx context.Context,
	keyID string,
) (backupartifact.DataKey, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return backupartifact.DataKey{}, err
	}
	return s.appBackupKeyService.GenerateDataKey(ctx, keyID)
}

func (s *backupE2EDelayedKeyService) UnwrapDataKey(
	ctx context.Context,
	keyID string,
	wrapped []byte,
) ([]byte, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return nil, err
	}
	return s.appBackupKeyService.UnwrapDataKey(ctx, keyID, wrapped)
}

func (s *backupE2EDelayedKeyService) Sign(
	ctx context.Context,
	keyID string,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return backupartifact.ManifestSignature{}, err
	}
	return s.appBackupKeyService.Sign(ctx, keyID, message)
}

func (s *backupE2EDelayedKeyService) Verify(
	ctx context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return err
	}
	return s.appBackupKeyService.Verify(ctx, signature, message)
}

func (s *backupE2EDelayedKeyService) Check(
	ctx context.Context,
	encryptionKeyID,
	signingKeyID string,
) error {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return err
	}
	return s.appBackupKeyService.Check(ctx, encryptionKeyID, signingKeyID)
}

type backupE2ESourcePinManager struct {
	runtimebackup.SourcePinManager
	trigger string
}

func (m *backupE2ESourcePinManager) Observe(
	ctx context.Context,
	hashSlot uint16,
	lease backupcontract.SlotCaptureLease,
	frontier backupcontract.SlotFrontier,
) (runtimebackup.SourcePinObservation, error) {
	observation, err := m.SourcePinManager.Observe(
		ctx, hashSlot, lease, frontier,
	)
	if err != nil {
		return observation, err
	}
	body, readErr := os.ReadFile(m.trigger)
	if readErr != nil {
		return observation, nil
	}
	target, parseErr := strconv.ParseUint(
		strings.TrimSpace(string(body)), 10, 16,
	)
	if parseErr == nil && uint16(target) == hashSlot {
		observation.Age = 2 * time.Hour
	}
	return observation, nil
}

func backupE2ERemoteLatency() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(backupE2ERemoteLatencyEnv))
	if raw == "" {
		return 0, nil
	}
	delay, err := time.ParseDuration(raw)
	if err != nil || delay < 0 || delay > 30*time.Second {
		return 0, fmt.Errorf(
			"backup e2e: %s must be a duration between 0 and 30s",
			backupE2ERemoteLatencyEnv,
		)
	}
	return delay, nil
}

func waitBackupE2ELatency(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func backupE2EActiveDelay(delay time.Duration, trigger string) time.Duration {
	if delay <= 0 || trigger == "" {
		return delay
	}
	if _, err := os.Stat(trigger); err != nil {
		return 0
	}
	return delay
}

func backupE2EFileRepository(
	repository appBackupRepository,
) (*backupinfra.FileRepository, bool) {
	switch typed := repository.(type) {
	case *backupinfra.FileRepository:
		return typed, true
	case *backupE2EDelayedRepository:
		fileRepository, ok := typed.appBackupRepository.(*backupinfra.FileRepository)
		return fileRepository, ok
	default:
		return nil, false
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
