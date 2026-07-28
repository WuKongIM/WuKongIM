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
	"encoding/json"
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

// backupLocalE2ERevision is stamped only into local e2e backup smoke binaries.
// Production qualification never reads or accepts this value.
var backupLocalE2ERevision string

// ValidateBackupLocalE2EBuildQualification binds the file-backed e2e substitute
// to the exact clean source revision used to build the local smoke binary.
func ValidateBackupLocalE2EBuildQualification(
	qualification BackupBuildQualification,
	localRevision string,
) error {
	buildRevision := strings.ToLower(
		strings.TrimSpace(qualification.BuildRevision),
	)
	localRevision = strings.ToLower(strings.TrimSpace(localRevision))
	if !validGitRevision(buildRevision) ||
		!validGitRevision(localRevision) ||
		buildRevision != localRevision ||
		qualification.BuildModified {
		return fmt.Errorf(
			"%w: local e2e automatic backup requires an exact clean build revision",
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateCurrentBackupBuildQualification() error {
	return validateBackupBuildQualificationForE2EMode(
		CurrentBackupBuildQualification(),
		backupLocalE2ERevision,
		strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) != "",
	)
}

func validateBackupBuildQualificationForE2EMode(
	qualification BackupBuildQualification,
	localRevision string,
	fileRepositoryEnabled bool,
) error {
	productionErr := ValidateBackupBuildQualification(qualification)
	if productionErr == nil || !fileRepositoryEnabled {
		return productionErr
	}
	return ValidateBackupLocalE2EBuildQualification(
		qualification,
		localRevision,
	)
}

func init() {
	productionRepositoryLoader := loadAppBackupRepository
	productionRepairLoader := loadAppBackupRepairRepository
	productionGarbageLoader := loadAppBackupGarbageRepository
	productionKeyLoader := loadAppBackupKeyService
	productionKeyBinder := bindAppBackupKeyService
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
	loadAppBackupRepository = func(
		ctx context.Context,
		name, endpoint, region, bucket, prefix string,
		objectLockDays int,
		accessRoleARN string,
	) (appBackupRepository, error) {
		root := strings.TrimSpace(os.Getenv(backupE2EFileRootEnv))
		var repository appBackupRepository
		var err error
		if root == "" {
			repository, err = productionRepositoryLoader(
				ctx, name, endpoint, region, bucket, prefix,
				objectLockDays, accessRoleARN,
			)
		} else {
			repository, err = backupinfra.NewFileRepository(
				name, filepath.Join(root, name),
			)
		}
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
			productionRepository := repository
			wrapped, wrappedRepository := repository.(*backupE2EDelayedRepository)
			if wrappedRepository {
				productionRepository = wrapped.appBackupRepository
			}
			repair, err := productionRepairLoader(
				ctx, productionRepository, endpoint, region, roleARN,
			)
			if err != nil || !wrappedRepository || wrapped.corruptionDir == "" {
				return repair, err
			}
			return &backupE2ERepairRepository{
				RepairRepository: repair,
				trigger: filepath.Join(
					wrapped.corruptionDir,
					productionRepository.Name()+".corrupt",
				),
				sticky: filepath.Join(
					wrapped.corruptionDir, "sticky.key",
				),
			}, nil
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
		probeSlot uint64,
	) (backupinfra.GenerationGarbageRepository, error) {
		root := strings.TrimSpace(os.Getenv(backupE2EFileRootEnv))
		if root == "" {
			return productionGarbageLoader(
				ctx, name, endpoint, region, bucket, prefix,
				objectLockDays, roleARN, probeSlot,
			)
		}
		return backupinfra.NewFileRepository(
			name, filepath.Join(root, name),
		)
	}
	loadAppBackupKeyService = func(
		ctx context.Context,
		repositoryID string,
	) (appBackupKeyService, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionKeyLoader(ctx, repositoryID)
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
	bindAppBackupKeyService = func(
		service appBackupKeyService,
		primary backupartifact.Repository,
		secondary backupartifact.Repository,
		canPublish func() bool,
	) (appBackupKeyService, error) {
		if strings.TrimSpace(os.Getenv(backupE2EFileRootEnv)) == "" {
			return productionKeyBinder(
				service, primary, secondary, canPublish,
			)
		}
		return service, nil
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
	if err != nil || !r.consumeCorruptionTrigger(ctx, key) {
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

func (r *backupE2EDelayedRepository) consumeCorruptionTrigger(
	ctx context.Context,
	key string,
) bool {
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
	var body []byte
	var selectedTrigger string
	for _, trigger := range []string{
		filepath.Join(
			r.corruptionDir,
			r.appBackupRepository.Name()+".corrupt",
		),
		filepath.Join(r.corruptionDir, "all.corrupt"),
	} {
		var err error
		body, err = os.ReadFile(trigger)
		if err == nil {
			selectedTrigger = trigger
			break
		}
		body = nil
	}
	if body == nil {
		return false
	}
	mode := strings.TrimSpace(string(body))
	switch {
	case mode == "once":
		return os.Remove(selectedTrigger) == nil
	case mode == "persistent":
		return true
	case mode == "sticky":
		return consumeBackupE2EStickyKey(
			filepath.Join(r.corruptionDir, "sticky.key"), key,
		)
	case strings.HasPrefix(mode, "sticky-segment:"):
		target, ok := parseBackupE2EStickySegmentTarget(mode)
		if !ok || !r.segmentPayloadMatchesTarget(ctx, key, target) {
			return false
		}
		selected := consumeBackupE2EStickyKey(
			backupE2EStickySegmentKey(r.corruptionDir, target), key,
		)
		if !selected {
			return false
		}
		// Publish one immutable marker per repository before returning corrupt
		// bytes. The qualification test can therefore distinguish one-copy
		// reads from an actual dual-copy integrity inspection.
		return consumeBackupE2EStickyKey(
			backupE2EStickySegmentHitKey(
				r.corruptionDir, target, r.appBackupRepository.Name(),
			),
			key,
		)
	default:
		return false
	}
}

type backupE2EStickySegmentTarget struct {
	hashSlot            uint16
	stream              backupartifact.SegmentStream
	sourceHighWatermark uint64
}

func parseBackupE2EStickySegmentTarget(
	mode string,
) (backupE2EStickySegmentTarget, bool) {
	parts := strings.Split(mode, ":")
	if len(parts) != 4 || parts[0] != "sticky-segment" {
		return backupE2EStickySegmentTarget{}, false
	}
	hashSlot, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return backupE2EStickySegmentTarget{}, false
	}
	stream := backupartifact.SegmentStream(parts[2])
	if stream != backupartifact.SegmentStreamMetadata &&
		stream != backupartifact.SegmentStreamMessages {
		return backupE2EStickySegmentTarget{}, false
	}
	sourceHighWatermark, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || sourceHighWatermark == 0 {
		return backupE2EStickySegmentTarget{}, false
	}
	return backupE2EStickySegmentTarget{
		hashSlot: uint16(hashSlot), stream: stream,
		sourceHighWatermark: sourceHighWatermark,
	}, true
}

// backupE2EStickySegmentKey isolates one exact immutable segment selection
// from generic sticky markers used by preceding single-copy repair drills.
func backupE2EStickySegmentKey(
	corruptionDir string,
	target backupE2EStickySegmentTarget,
) string {
	return filepath.Join(
		corruptionDir,
		fmt.Sprintf(
			"sticky-segment-%d-%s-%d.key",
			target.hashSlot, target.stream, target.sourceHighWatermark,
		),
	)
}

func backupE2EStickySegmentHitKey(
	corruptionDir string,
	target backupE2EStickySegmentTarget,
	repository string,
) string {
	return filepath.Join(
		corruptionDir,
		fmt.Sprintf(
			"sticky-segment-%d-%s-%d.%s.hit",
			target.hashSlot, target.stream, target.sourceHighWatermark,
			repository,
		),
	)
}

func backupE2ECorruptionSelectionKey(
	corruptionDir string,
	mode string,
) string {
	if mode == "sticky" {
		return filepath.Join(corruptionDir, "sticky.key")
	}
	target, ok := parseBackupE2EStickySegmentTarget(mode)
	if !ok {
		return ""
	}
	return backupE2EStickySegmentKey(corruptionDir, target)
}

// segmentPayloadMatchesTarget binds an e2e corruption marker to the exact
// logical segment authenticated by the payload's immutable commit record.
func (r *backupE2EDelayedRepository) segmentPayloadMatchesTarget(
	ctx context.Context,
	key string,
	target backupE2EStickySegmentTarget,
) bool {
	parts := strings.Split(filepath.ToSlash(key), "/")
	if len(parts) != 4 || parts[0] != "segments" ||
		parts[1] == "" || parts[2] != "payloads" {
		return false
	}
	commitKey := "segments/" + parts[1] + "/commit.json"
	reader, _, err := r.appBackupRepository.Open(ctx, commitKey)
	if err != nil {
		return false
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, 64<<10+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil ||
		len(body) == 0 || len(body) > 64<<10 {
		return false
	}
	var commit backupartifact.SegmentCommit
	if json.Unmarshal(body, &commit) != nil ||
		commit.SegmentID != parts[1] {
		return false
	}
	return commit.Header.Logical.HashSlot == target.hashSlot &&
		commit.Header.Logical.Stream == target.stream &&
		commit.Header.SourceHighWatermark == target.sourceHighWatermark
}

func consumeBackupE2EStickyKey(path, key string) bool {
	for attempt := 0; attempt < 3; attempt++ {
		selected, err := os.ReadFile(path)
		switch {
		case err == nil && len(selected) > 0:
			return string(selected) == key
		case err == nil:
			// An older process may have stopped after creating the file but
			// before writing the selection. Remove that incomplete state so a
			// complete selection can be published atomically below.
			if removeErr := os.Remove(path); removeErr != nil &&
				!os.IsNotExist(removeErr) {
				return false
			}
			continue
		case !os.IsNotExist(err):
			return false
		}
		published, publishErr := publishBackupE2EStickyKey(path, key)
		if publishErr != nil {
			return false
		}
		if published {
			return true
		}
	}
	return false
}

// publishBackupE2EStickyKey makes a complete immutable selection visible with
// one same-directory hard-link operation.
func publishBackupE2EStickyKey(path, key string) (bool, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".sticky-key-*")
	if err != nil {
		return false, err
	}
	tempPath := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return false, err
	}
	if _, err := file.WriteString(key); err != nil {
		cleanup()
		return false, err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return false, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return false, err
	}
	if err := os.Link(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(tempPath); err != nil {
		return false, err
	}
	return true, nil
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
	selectionKey := r.sticky
	if triggerBody, err := os.ReadFile(r.trigger); err == nil {
		if exactKey := backupE2ECorruptionSelectionKey(
			filepath.Dir(r.trigger),
			strings.TrimSpace(string(triggerBody)),
		); exactKey != "" {
			selectionKey = exactKey
		}
	}
	if err := r.RepairRepository.RepairImmutable(
		ctx, key, size, checksum, body,
	); err != nil {
		return err
	}
	if err := os.Remove(r.trigger); err != nil && !os.IsNotExist(err) {
		return err
	}
	allTrigger := filepath.Join(filepath.Dir(r.sticky), "all.corrupt")
	if _, err := os.Stat(allTrigger); err == nil {
		// Keep both repositories pinned to the same object until the test
		// explicitly releases the dual-corruption fault.
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(selectionKey); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *backupE2EDelayedKeyService) NewDataKey(
	ctx context.Context,
) (backupartifact.DataKey, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return backupartifact.DataKey{}, err
	}
	return s.appBackupKeyService.NewDataKey(ctx)
}

func (s *backupE2EDelayedKeyService) OpenDataKey(
	ctx context.Context,
	envelope backupartifact.DataKeyEnvelope,
) ([]byte, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return nil, err
	}
	return s.appBackupKeyService.OpenDataKey(ctx, envelope)
}

func (s *backupE2EDelayedKeyService) Sign(
	ctx context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return backupartifact.ManifestSignature{}, err
	}
	return s.appBackupKeyService.Sign(ctx, message)
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
) error {
	if err := waitBackupE2ELatency(ctx, backupE2EActiveDelay(s.delay, s.trigger)); err != nil {
		return err
	}
	return s.appBackupKeyService.Check(ctx)
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

func (s *backupE2EKeyService) NewDataKey(
	_ context.Context,
) (backupartifact.DataKey, error) {
	if s == nil {
		return backupartifact.DataKey{}, fmt.Errorf("backup e2e keys: unavailable")
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
	return backupartifact.DataKey{
		Plaintext: plaintext,
		Envelope: backupartifact.DataKeyEnvelope{
			Version: 1, Algorithm: "AES_256_GCM_E2E", KeyID: "e2e",
			Nonce: nonce,
			Value: aead.Seal(nil, nonce, plaintext, []byte("e2e")),
		},
	}, nil
}

func (s *backupE2EKeyService) OpenDataKey(
	_ context.Context,
	envelope backupartifact.DataKeyEnvelope,
) ([]byte, error) {
	if s == nil || envelope.Version != 1 ||
		envelope.Algorithm != "AES_256_GCM_E2E" ||
		envelope.KeyID != "e2e" {
		return nil, fmt.Errorf("backup e2e keys: envelope is invalid")
	}
	block, err := aes.NewCipher(s.wrappingKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(envelope.Nonce) != aead.NonceSize() ||
		len(envelope.Value) == 0 {
		return nil, fmt.Errorf("backup e2e keys: wrapped key is truncated")
	}
	return aead.Open(
		nil, envelope.Nonce, envelope.Value, []byte("e2e"),
	)
}

func (s *backupE2EKeyService) Sign(
	_ context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	if s == nil {
		return backupartifact.ManifestSignature{}, fmt.Errorf("backup e2e keys: unavailable")
	}
	return backupartifact.ManifestSignature{
		Algorithm: "ED25519_E2E", KeyID: "ed25519:e2e",
		Value: ed25519.Sign(s.signingKey, message),
	}, nil
}

func (s *backupE2EKeyService) Verify(_ context.Context, signature backupartifact.ManifestSignature, message []byte) error {
	if s == nil || signature.Algorithm != "ED25519_E2E" || signature.KeyID == "" || !ed25519.Verify(s.signingKey.Public().(ed25519.PublicKey), message, signature.Value) {
		return fmt.Errorf("backup e2e keys: signature verification failed")
	}
	return nil
}

func (s *backupE2EKeyService) Check(ctx context.Context) error {
	dataKey, err := s.NewDataKey(ctx)
	if err != nil {
		return err
	}
	unwrapped, err := s.OpenDataKey(ctx, dataKey.Envelope)
	if err != nil || !bytes.Equal(unwrapped, dataKey.Plaintext) {
		return fmt.Errorf("backup e2e keys: envelope round trip failed: %w", err)
	}
	probe := []byte("wukongim-backup-e2e-key-doctor-v1")
	signature, err := s.Sign(ctx, probe)
	if err != nil {
		return err
	}
	return s.Verify(ctx, signature, probe)
}

type backupE2EClockProbe struct{}

func (backupE2EClockProbe) UTC(context.Context) (time.Time, error) {
	return time.Now().UTC(), nil
}
