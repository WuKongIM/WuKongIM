package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// ArchiveRepositoryProvider resolves one durable plan repository.
type ArchiveRepositoryProvider interface {
	Open(context.Context, backupcontract.StoreConfig) (backupartifact.ArchiveStore, error)
}

// ObjectStoreCredentialSealer protects credentials before Controller
// publication.
type ObjectStoreCredentialSealer interface {
	SealObjectStoreCredentials(accessKey string, secretKey string) ([]byte, error)
}

// SharedRepositoryProbe lets app wiring prove file visibility across every
// active data node before plan enablement.
type SharedRepositoryProbe interface {
	ProbeRepository(
		context.Context,
		backupcontract.StoreConfig,
		backupartifact.ArchiveStore,
	) error
}

// ManagementOptions configures the Manager-facing backup application service.
type ManagementOptions struct {
	Scheduled  *ScheduledService
	Repository ArchiveRepositoryProvider
	Sealer     ObjectStoreCredentialSealer
	Probe      SharedRepositoryProbe
	ClusterID  string
	Now        func() time.Time
}

// ManagementService owns Manager backup configuration and archive operations.
type ManagementService struct {
	scheduled  *ScheduledService
	repository ArchiveRepositoryProvider
	sealer     ObjectStoreCredentialSealer
	probe      SharedRepositoryProbe
	clusterID  string
	now        func() time.Time
}

// Dashboard is the single Manager read model for plan, task, and archive state.
type Dashboard struct {
	State                      backupcontract.SystemState `json:"state"`
	Archives                   []ArchiveSummary           `json:"archives"`
	CredentialsConfigured      bool                       `json:"credentials_configured"`
	NextScheduledUnixMS        int64                      `json:"next_scheduled_unix_ms,omitempty"`
	BackupHealth               BackupHealth               `json:"backup_health,omitempty"`
	BackupHealthReason         string                     `json:"backup_health_reason,omitempty"`
	LastSuccessfulBackupUnixMS int64                      `json:"last_successful_backup_unix_ms,omitempty"`
	RepositoryError            string                     `json:"repository_error,omitempty"`
}

// BackupHealth summarizes whether the enabled plan is producing recoverable
// archives at its expected cadence.
type BackupHealth string

const (
	// BackupHealthHealthy means no expected backup coverage is missing.
	BackupHealthHealthy BackupHealth = "healthy"
	// BackupHealthWarning means the latest backup attempt failed.
	BackupHealthWarning BackupHealth = "warning"
	// BackupHealthCritical means two expected runs passed without a success.
	BackupHealthCritical BackupHealth = "critical"
)

// ConfigureManagementRequest contains a plan plus an optional replacement
// object storage credential.
type ConfigureManagementRequest struct {
	ConfigureRequest
	AccessKey string
	SecretKey string
}

// TestRepositoryRequest identifies the exact saved plan to verify.
type TestRepositoryRequest struct {
	ExpectedPlanRevision uint64
}

// NewManagementService creates the Manager-facing backup application service.
func NewManagementService(options ManagementOptions) (*ManagementService, error) {
	if options.Scheduled == nil || options.Repository == nil ||
		options.Sealer == nil || options.Probe == nil ||
		strings.TrimSpace(options.ClusterID) == "" || options.Now == nil {
		return nil, fmt.Errorf("%w: backup management dependencies", ErrInvalidRequest)
	}
	return &ManagementService{
		scheduled: options.Scheduled, repository: options.Repository,
		sealer: options.Sealer, probe: options.Probe,
		clusterID: strings.TrimSpace(options.ClusterID), now: options.Now,
	}, nil
}

// Dashboard returns Controller state and the current repository inventory.
func (s *ManagementService) Dashboard(ctx context.Context) (Dashboard, error) {
	state, err := s.scheduled.State(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	result := Dashboard{
		State: state.Clone(), Archives: []ArchiveSummary{},
	}
	if state.Plan == nil {
		return result, nil
	}
	if state.Plan.Enabled {
		result.BackupHealth, result.BackupHealthReason,
			result.LastSuccessfulBackupUnixMS = planBackupHealth(
			*state.Plan, state.History, s.now(),
		)
		location, schedule, scheduleErr := parsePlanSchedule(*state.Plan)
		if scheduleErr == nil {
			now := s.now().In(location)
			cursor := state.Plan.ScheduleCursorUnixMillis
			if cursor <= 0 {
				cursor = state.Plan.CreatedUnixMillis
			}
			next := schedule.Next(time.UnixMilli(cursor).In(location))
			for range 10_000 {
				if next.After(now) {
					result.NextScheduledUnixMS = next.UTC().UnixMilli()
					break
				}
				next = schedule.Next(next)
			}
		}
	}
	result.CredentialsConfigured =
		len(state.Plan.Store.CredentialCiphertext) > 0
	result.State.Plan.Store.CredentialCiphertext = nil
	store, err := s.repository.Open(ctx, state.Plan.Store)
	if err != nil {
		result.RepositoryError = "repository_unavailable"
		return result, nil
	}
	result.Archives, err = ListArchives(ctx, store)
	if err != nil {
		result.Archives = []ArchiveSummary{}
		result.RepositoryError = "repository_unavailable"
	}
	return result, nil
}

func planBackupHealth(
	plan backupcontract.Plan,
	history []backupcontract.TaskRecord,
	now time.Time,
) (BackupHealth, string, int64) {
	health := BackupHealthHealthy
	var latestBackup backupcontract.TaskRecord
	var latestSuccess int64
	for _, record := range history {
		if record.Kind != "backup" {
			continue
		}
		if record.CompletedUnixMillis > latestBackup.CompletedUnixMillis {
			latestBackup = record
		}
		if record.Status == string(backupcontract.JobStatusSucceeded) &&
			record.CompletedUnixMillis > latestSuccess {
			latestSuccess = record.CompletedUnixMillis
		}
	}
	if latestBackup.Status == string(backupcontract.JobStatusFailed) {
		health = BackupHealthWarning
	}
	location, schedule, err := parsePlanSchedule(plan)
	if err != nil {
		return health, backupHealthReason(health), latestSuccess
	}
	baseline := plan.CreatedUnixMillis
	if latestSuccess > baseline {
		baseline = latestSuccess
	}
	occurrence := schedule.Next(time.UnixMilli(baseline).In(location))
	for range 2 {
		if occurrence.After(now.In(location)) {
			return health, backupHealthReason(health), latestSuccess
		}
		occurrence = schedule.Next(occurrence)
	}
	return BackupHealthCritical, "successful_backup_stale", latestSuccess
}

func backupHealthReason(health BackupHealth) string {
	if health == BackupHealthWarning {
		return "latest_backup_failed"
	}
	return ""
}

// Configure encrypts credentials and atomically publishes the plan without
// contacting the selected repository.
func (s *ManagementService) Configure(
	ctx context.Context,
	request ConfigureManagementRequest,
) (ConfigureResult, error) {
	current, err := s.scheduled.State(ctx)
	if err != nil {
		return ConfigureResult{}, err
	}
	storeConfig, err := normalizeManagementStoreConfig(request.Store)
	if err != nil {
		return ConfigureResult{}, err
	}
	switch storeConfig.Kind {
	case backupcontract.StoreKindFile:
	case backupcontract.StoreKindOSS,
		backupcontract.StoreKindCOS,
		backupcontract.StoreKindS3:
		accessKey := strings.TrimSpace(request.AccessKey)
		if accessKey != "" || request.SecretKey != "" {
			if accessKey == "" || request.SecretKey == "" {
				return ConfigureResult{}, ErrInvalidRequest
			}
			ciphertext, err := s.sealer.SealObjectStoreCredentials(
				accessKey, request.SecretKey,
			)
			if err != nil {
				return ConfigureResult{}, err
			}
			storeConfig.CredentialCiphertext = ciphertext
			storeConfig.CredentialRevision++
			if current.Plan != nil {
				storeConfig.CredentialRevision =
					current.Plan.Store.CredentialRevision + 1
			}
		} else if current.Plan != nil &&
			current.Plan.Store.Kind == storeConfig.Kind {
			storeConfig.CredentialCiphertext = append(
				[]byte(nil), current.Plan.Store.CredentialCiphertext...,
			)
			storeConfig.CredentialRevision =
				current.Plan.Store.CredentialRevision
		}
		if len(storeConfig.CredentialCiphertext) == 0 {
			return ConfigureResult{}, ErrInvalidRequest
		}
	default:
		return ConfigureResult{}, ErrInvalidRequest
	}
	request.ConfigureRequest.Store = storeConfig
	request.ConfigureRequest.RepositoryVerification =
		&backupcontract.RepositoryVerification{
			Status: backupcontract.RepositoryVerificationUnverified,
		}
	if current.Plan != nil &&
		equalEffectiveRepository(current.Plan.Store, storeConfig) {
		request.ConfigureRequest.RepositoryVerification =
			cloneRepositoryVerification(
				current.Plan.RepositoryVerification,
			)
	}
	return s.scheduled.Configure(ctx, request.ConfigureRequest)
}

// TestRepository verifies read/write/delete access for one exact saved plan.
func (s *ManagementService) TestRepository(
	ctx context.Context,
	request TestRepositoryRequest,
) (backupcontract.Plan, error) {
	current, err := s.scheduled.State(ctx)
	if err != nil {
		return backupcontract.Plan{}, err
	}
	if current.Plan == nil {
		return backupcontract.Plan{}, ErrDisabled
	}
	if current.Plan.Revision != request.ExpectedPlanRevision {
		return backupcontract.Plan{}, ErrStateConflict
	}
	config := cloneStoreConfig(current.Plan.Store)
	store, err := s.repository.Open(ctx, config)
	if err != nil {
		return backupcontract.Plan{}, normalizeStoreAccessError(err)
	}
	if err := s.probe.ProbeRepository(ctx, config, store); err != nil {
		return backupcontract.Plan{}, normalizeStoreAccessError(err)
	}
	if _, err := backupartifact.EnsureRepository(
		ctx, store, s.clusterID, s.now().UTC().UnixMilli(),
	); err != nil {
		return backupcontract.Plan{}, normalizeArtifactError(err)
	}
	return s.scheduled.MarkRepositoryVerified(
		ctx, request.ExpectedPlanRevision,
	)
}

func equalEffectiveRepository(
	current backupcontract.StoreConfig,
	next backupcontract.StoreConfig,
) bool {
	return current.Kind == next.Kind &&
		current.Endpoint == next.Endpoint &&
		current.Region == next.Region &&
		current.Bucket == next.Bucket &&
		current.Prefix == next.Prefix &&
		current.PathStyle == next.PathStyle &&
		current.CredentialRevision == next.CredentialRevision
}

func normalizeManagementStoreConfig(
	input backupcontract.StoreConfig,
) (backupcontract.StoreConfig, error) {
	config := cloneStoreConfig(input)
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.Region = strings.TrimSpace(config.Region)
	config.Bucket = strings.TrimSpace(config.Bucket)
	config.Prefix = strings.Trim(strings.TrimSpace(config.Prefix), "/")
	// Access callers never choose durable ciphertext or its revision. Those
	// values come only from the credential sealer or the same-provider plan.
	config.CredentialCiphertext = nil
	config.CredentialRevision = 0
	switch config.Kind {
	case backupcontract.StoreKindFile:
		return backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile}, nil
	case backupcontract.StoreKindS3:
		if config.Endpoint == "" || config.Bucket == "" || config.Prefix == "" {
			return backupcontract.StoreConfig{}, ErrInvalidRequest
		}
	case backupcontract.StoreKindOSS, backupcontract.StoreKindCOS:
		if config.Region == "" || config.Bucket == "" || config.Prefix == "" ||
			config.PathStyle || !ValidCloudRegion(config.Region) {
			return backupcontract.StoreConfig{}, ErrInvalidRequest
		}
		if config.Kind == backupcontract.StoreKindCOS &&
			!COSBucketHasAPPID(config.Bucket) {
			return backupcontract.StoreConfig{}, ErrInvalidRequest
		}
	default:
		return backupcontract.StoreConfig{}, ErrInvalidRequest
	}
	return config, nil
}

// StartBackup admits one immediate manual full backup.
func (s *ManagementService) StartBackup(
	ctx context.Context,
) (backupcontract.BackupJob, error) {
	return s.scheduled.StartBackup(ctx, StartBackupRequest{
		Trigger: backupcontract.TriggerManual,
	})
}

// CancelBackup requests cancellation of the current job.
func (s *ManagementService) CancelBackup(ctx context.Context, jobID string) error {
	return s.scheduled.RequestBackupCancellation(ctx, jobID)
}

// Archive returns one published archive detail.
func (s *ManagementService) Archive(
	ctx context.Context,
	archiveID string,
) (ArchiveDetail, error) {
	store, err := s.currentStore(ctx)
	if err != nil {
		return ArchiveDetail{}, err
	}
	detail, err := ArchiveByID(ctx, store, archiveID)
	return detail, normalizeArtifactError(err)
}

// VerifyArchive fully verifies one published archive.
func (s *ManagementService) VerifyArchive(
	ctx context.Context,
	archiveID string,
) (detail ArchiveDetail, resultErr error) {
	operation, err := s.scheduled.AcquireArchiveOperation(
		ctx, "verify", archiveID,
	)
	if err != nil {
		return ArchiveDetail{}, err
	}
	defer func() {
		recordContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), 10*time.Second,
		)
		defer cancel()
		status := backupcontract.JobStatusSucceeded
		errorCode := ""
		if resultErr != nil {
			status = backupcontract.JobStatusFailed
			errorCode = verificationErrorCode(resultErr)
		}
		resultErr = errors.Join(
			resultErr,
			s.scheduled.RecordTask(recordContext, RecordTaskRequest{
				ID: operation.Token, Kind: "verification", Status: status,
				StartedUnixMillis:   operation.StartedUnixMillis,
				CompletedUnixMillis: s.now().UTC().UnixMilli(),
				ErrorCode:           errorCode,
			}),
			s.releaseArchiveOperation(operation.Token),
		)
	}()
	store, err := s.currentStore(ctx)
	if err != nil {
		return ArchiveDetail{}, err
	}
	detail, err = VerifyArchive(ctx, store, archiveID)
	if err == nil || !IsArchiveIntegrityFailure(err) {
		return detail, normalizeArtifactError(err)
	}
	markErr := MarkArchiveCorrupt(ctx, store, archiveID, s.now())
	return ArchiveDetail{}, errors.Join(ErrArchiveCorrupt, err, markErr)
}

func verificationErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrArchiveCorrupt):
		return "archive_corrupt"
	case errors.Is(err, ErrArchiveNotFound):
		return "archive_not_found"
	case errors.Is(err, ErrStoreUnreachable):
		return "store_unreachable"
	default:
		return "verification_failed"
	}
}

// HoldArchive creates or removes one retention hold marker.
func (s *ManagementService) HoldArchive(
	ctx context.Context,
	archiveID string,
	held bool,
	note string,
) (summary ArchiveSummary, resultErr error) {
	operation, err := s.scheduled.AcquireArchiveOperation(
		ctx, "hold", archiveID,
	)
	if err != nil {
		return ArchiveSummary{}, err
	}
	defer func() {
		resultErr = errors.Join(
			resultErr,
			s.releaseArchiveOperation(operation.Token),
		)
	}()
	store, err := s.currentStore(ctx)
	if err != nil {
		return ArchiveSummary{}, err
	}
	summary, err = SetArchiveHold(ctx, store, archiveID, held, note, s.now())
	return summary, normalizeArtifactError(err)
}

// DeleteArchive removes one unheld archive.
func (s *ManagementService) DeleteArchive(
	ctx context.Context,
	archiveID string,
) (resultErr error) {
	operation, err := s.scheduled.AcquireArchiveOperation(
		ctx, "delete", archiveID,
	)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(
			resultErr,
			s.releaseArchiveOperation(operation.Token),
		)
	}()
	state, err := s.scheduled.State(ctx)
	if err != nil {
		return err
	}
	if state.ActiveRestore != nil && state.ActiveRestore.BackupID == archiveID {
		return ErrArchiveInUse
	}
	store, err := s.currentStore(ctx)
	if err != nil {
		return err
	}
	archives, err := ListArchives(ctx, store)
	if err != nil {
		return err
	}
	healthyCount := 0
	targetHealthy := false
	for _, archive := range archives {
		if archive.Health == ArchiveHealthHealthy {
			healthyCount++
			if archive.ID == archiveID {
				targetHealthy = true
			}
		}
	}
	if targetHealthy && healthyCount <= 1 {
		return ErrLastUsableArchive
	}
	return normalizeArtifactError(DeleteArchive(ctx, store, archiveID))
}

func (s *ManagementService) releaseArchiveOperation(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.scheduled.ReleaseArchiveOperation(ctx, token)
}

func (s *ManagementService) currentStore(
	ctx context.Context,
) (backupartifact.ArchiveStore, error) {
	state, err := s.scheduled.State(ctx)
	if err != nil {
		return nil, err
	}
	if state.Plan == nil {
		return nil, ErrDisabled
	}
	return s.repository.Open(ctx, state.Plan.Store)
}

// DirectRepositoryProbe verifies bounded read/write/delete behavior. App
// wiring may wrap it with the cross-node shared-file visibility protocol.
type DirectRepositoryProbe struct {
	NewID func() string
}

// ProbeRepository performs a bounded nonce round trip.
func (p DirectRepositoryProbe) ProbeRepository(
	ctx context.Context,
	_ backupcontract.StoreConfig,
	store backupartifact.ArchiveStore,
) error {
	if p.NewID == nil || store == nil {
		return ErrInvalidRequest
	}
	key := "probes/" + p.NewID()
	body := []byte("wukongim-backup-probe")
	if err := store.Put(ctx, backupartifact.PutObject{
		Key: key, Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	}); err != nil {
		return err
	}
	defer store.Delete(ctx, key)
	reader, object, err := store.Open(ctx, key)
	if err != nil {
		return err
	}
	loaded, readErr := io.ReadAll(io.LimitReader(reader, int64(len(body))+1))
	closeErr := reader.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if object.Bytes != uint64(len(body)) || !bytes.Equal(loaded, body) {
		return fmt.Errorf("backup usecase: repository probe mismatch")
	}
	return store.Delete(ctx, key)
}

var _ SharedRepositoryProbe = DirectRepositoryProbe{}
