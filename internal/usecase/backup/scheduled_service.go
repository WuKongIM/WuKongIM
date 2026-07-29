package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

var (
	// ErrBackupJobActive reports admission while another full backup is active.
	ErrBackupJobActive = errors.New("backup usecase: full backup job is active")
	// ErrRestoreJobActive reports admission while restore owns maintenance.
	ErrRestoreJobActive = errors.New("backup usecase: restore job is active")
)

// ScheduledStateStore persists the complete bounded backup state.
type ScheduledStateStore interface {
	Load(context.Context) (backupcontract.SystemState, error)
	CompareAndSwap(context.Context, uint64, backupcontract.SystemState) error
}

// ScheduledOptions configures scheduled full-backup orchestration.
type ScheduledOptions struct {
	StateStore ScheduledStateStore
	Now        func() time.Time
	NewID      func() string
}

// ScheduledService owns plan and full-backup job admission.
type ScheduledService struct {
	store ScheduledStateStore
	now   func() time.Time
	newID func() string
}

// State returns a detached snapshot of the complete bounded subsystem state.
func (s *ScheduledService) State(
	ctx context.Context,
) (backupcontract.SystemState, error) {
	if s == nil || s.store == nil {
		return backupcontract.SystemState{}, ErrDisabled
	}
	return s.store.Load(ctx)
}

// ConfigureRequest replaces the only cluster backup plan.
type ConfigureRequest struct {
	ExpectedRevision uint64
	Enabled          bool
	Store            backupcontract.StoreConfig
	Cron             string
	TimeZone         string
	RetentionCount   int
	RateBytesPerSec  uint64
	WorkersPerNode   int
	MaxDuration      time.Duration
}

// ConfigureResult returns the published plan and optional immediate first job.
type ConfigureResult struct {
	Plan       backupcontract.Plan       `json:"plan"`
	InitialJob *backupcontract.BackupJob `json:"initial_job,omitempty"`
}

// StartBackupRequest admits an immediate or scheduled full backup.
type StartBackupRequest struct {
	Trigger     backupcontract.Trigger
	ScheduledAt time.Time
}

// ClaimSlotRequest fences one current Slot export attempt.
type ClaimSlotRequest struct {
	JobID       string
	HashSlot    uint16
	OwnerNodeID uint64
	OwnerTerm   uint64
}

// CompleteSlotRequest publishes one fenced Slot export result.
type CompleteSlotRequest struct {
	JobID          string
	HashSlot       uint16
	Attempt        uint32
	OwnerNodeID    uint64
	OwnerTerm      uint64
	ManifestKey    string
	ManifestSHA256 string
	LogicalBytes   uint64
	StoredBytes    uint64
	Records        uint64
	MaxMessageID   uint64
}

// FailSlotRequest records one fenced export failure for later retry.
type FailSlotRequest struct {
	JobID       string
	HashSlot    uint16
	Attempt     uint32
	OwnerNodeID uint64
	OwnerTerm   uint64
	ErrorCode   string
}

// FinishBackupRequest records one terminal full-backup result.
type FinishBackupRequest struct {
	JobID     string
	Status    backupcontract.JobStatus
	ErrorCode string
}

// RecordTaskRequest appends one completed auxiliary backup operation.
type RecordTaskRequest struct {
	// ID is stable across retries of the same logical operation.
	ID string
	// Kind identifies verification or retention history.
	Kind string
	// Status is the terminal succeeded or failed result.
	Status backupcontract.JobStatus
	// StartedUnixMillis is the operation start time in UTC Unix milliseconds.
	StartedUnixMillis int64
	// CompletedUnixMillis is the terminal time in UTC Unix milliseconds.
	CompletedUnixMillis int64
	// ErrorCode is a bounded stable operator-facing failure code.
	ErrorCode string
}

// AdvanceBackupPhaseRequest durably moves publication through its one-way
// phases. Keeping these phases in Controller state makes COMPLETE publication
// resumable without ever treating a published archive as cancelable work.
type AdvanceBackupPhaseRequest struct {
	JobID string
	From  backupcontract.JobStatus
	To    backupcontract.JobStatus
}

// ScheduleResult describes one due Cron occurrence evaluation.
type ScheduleResult struct {
	Occurrence time.Time
	Job        *backupcontract.BackupJob
	Skipped    bool
}

const archiveOperationLeaseDuration = 48 * time.Hour

// AcquireArchiveOperation serializes repository operations through Controller
// state. Expired leases may be replaced, so a crashed Manager cannot block
// archive administration indefinitely.
func (s *ScheduledService) AcquireArchiveOperation(
	ctx context.Context,
	kind string,
	archiveID string,
) (backupcontract.ArchiveOperation, error) {
	kind = strings.TrimSpace(kind)
	archiveID = strings.TrimSpace(archiveID)
	switch kind {
	case "verify", "hold", "delete", "retention", "restore":
	default:
		return backupcontract.ArchiveOperation{}, ErrInvalidRequest
	}
	if len(archiveID) > 128 || strings.Contains(archiveID, "/") {
		return backupcontract.ArchiveOperation{}, ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return backupcontract.ArchiveOperation{}, err
	}
	now := s.now().UTC()
	if current.ActiveArchiveOperation != nil &&
		now.Before(time.UnixMilli(
			current.ActiveArchiveOperation.ExpiresUnixMillis,
		)) {
		return backupcontract.ArchiveOperation{}, ErrArchiveOperationActive
	}
	token := strings.TrimSpace(s.newID())
	if token == "" || len(token) > 128 {
		return backupcontract.ArchiveOperation{}, ErrInvalidRequest
	}
	operation := backupcontract.ArchiveOperation{
		Token: token, Kind: kind, ArchiveID: archiveID,
		StartedUnixMillis: now.UnixMilli(),
		ExpiresUnixMillis: now.Add(archiveOperationLeaseDuration).UnixMilli(),
	}
	next := current.Clone()
	next.Revision++
	next.ActiveArchiveOperation = &operation
	if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
		return backupcontract.ArchiveOperation{}, err
	}
	return operation, nil
}

// ReleaseArchiveOperation clears only the exact operation lease held by the
// caller and never releases a newer replacement.
func (s *ScheduledService) ReleaseArchiveOperation(
	ctx context.Context,
	token string,
) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidRequest
	}
	for range 16 {
		current, err := s.store.Load(ctx)
		if err != nil {
			return err
		}
		if current.ActiveArchiveOperation == nil {
			return nil
		}
		if current.ActiveArchiveOperation.Token != token {
			return ErrStateConflict
		}
		next := current.Clone()
		next.Revision++
		next.ActiveArchiveOperation = nil
		err = s.store.CompareAndSwap(ctx, current.Revision, next)
		if !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	return ErrStateConflict
}

// RecordTask durably records a bounded verification or retention result. The
// (kind, ID) pair is idempotent so resumed cleanup does not duplicate history.
func (s *ScheduledService) RecordTask(
	ctx context.Context,
	request RecordTaskRequest,
) error {
	request.ID = strings.TrimSpace(request.ID)
	request.Kind = strings.TrimSpace(request.Kind)
	if request.ID == "" || len(request.ID) > 128 ||
		(request.Kind != "verification" && request.Kind != "retention") ||
		request.StartedUnixMillis <= 0 ||
		request.CompletedUnixMillis < request.StartedUnixMillis ||
		len(request.ErrorCode) > 128 {
		return ErrInvalidRequest
	}
	switch request.Status {
	case backupcontract.JobStatusSucceeded, backupcontract.JobStatusFailed:
	default:
		return ErrInvalidRequest
	}
	record := backupcontract.TaskRecord{
		ID: request.ID, Kind: request.Kind,
		Status:              string(request.Status),
		StartedUnixMillis:   request.StartedUnixMillis,
		CompletedUnixMillis: request.CompletedUnixMillis,
		ErrorCode:           request.ErrorCode,
	}
	for range 16 {
		current, err := s.store.Load(ctx)
		if err != nil {
			return err
		}
		for _, existing := range current.History {
			if existing.ID == record.ID && existing.Kind == record.Kind {
				return nil
			}
		}
		next := current.Clone()
		next.Revision++
		next.History = append(
			[]backupcontract.TaskRecord{record}, next.History...,
		)
		if len(next.History) > backupcontract.MaxTaskHistory {
			next.History = next.History[:backupcontract.MaxTaskHistory]
		}
		err = s.store.CompareAndSwap(ctx, current.Revision, next)
		if !errors.Is(err, ErrStateConflict) {
			return err
		}
	}
	return ErrStateConflict
}

// NewScheduledService creates the bounded scheduled-backup service.
func NewScheduledService(options ScheduledOptions) (*ScheduledService, error) {
	if options.StateStore == nil || options.Now == nil || options.NewID == nil {
		return nil, fmt.Errorf("%w: scheduled backup dependencies", ErrInvalidRequest)
	}
	return &ScheduledService{store: options.StateStore, now: options.Now, newID: options.NewID}, nil
}

// Evaluate advances the durable schedule while hiding admission details from
// the runtime supervisor.
func (s *ScheduledService) Evaluate(
	ctx context.Context,
	maxLateness time.Duration,
) error {
	_, err := s.EvaluateSchedule(ctx, maxLateness)
	return err
}

// Configure atomically publishes a plan and, on disabled-to-enabled
// transition, admits its immediate initial full backup.
func (s *ScheduledService) Configure(
	ctx context.Context,
	request ConfigureRequest,
) (ConfigureResult, error) {
	if err := validateConfigureRequest(request, s.now()); err != nil {
		return ConfigureResult{}, err
	}
	for range 16 {
		current, err := s.store.Load(ctx)
		if err != nil {
			return ConfigureResult{}, err
		}
		currentPlanRevision := uint64(0)
		wasEnabled := false
		if current.Plan != nil {
			currentPlanRevision = current.Plan.Revision
			wasEnabled = current.Plan.Enabled
		}
		if request.ExpectedRevision != currentPlanRevision {
			return ConfigureResult{}, ErrStateConflict
		}
		if current.ActiveRestore != nil {
			return ConfigureResult{}, ErrRestoreJobActive
		}
		if current.ActiveArchiveOperation != nil &&
			s.now().UTC().Before(time.UnixMilli(
				current.ActiveArchiveOperation.ExpiresUnixMillis,
			)) {
			return ConfigureResult{}, ErrArchiveOperationActive
		}
		if current.ActiveBackup != nil {
			disableOnly := current.Plan != nil && current.Plan.Enabled &&
				!request.Enabled &&
				equalPlanConfiguration(*current.Plan, request)
			if !disableOnly {
				return ConfigureResult{}, ErrBackupJobActive
			}
			now := s.now().UTC()
			next := current.Clone()
			next.Revision++
			next.Plan.Enabled = false
			next.Plan.UpdatedUnixMillis = now.UnixMilli()
			if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
				if errors.Is(err, ErrStateConflict) {
					continue
				}
				return ConfigureResult{}, err
			}
			return ConfigureResult{Plan: *next.Plan}, nil
		}

		now := s.now().UTC()
		createdAt := now.UnixMilli()
		if current.Plan != nil {
			createdAt = current.Plan.CreatedUnixMillis
		}
		plan := backupcontract.Plan{
			Revision:                 currentPlanRevision + 1,
			Enabled:                  request.Enabled,
			Store:                    cloneStoreConfig(request.Store),
			Cron:                     strings.TrimSpace(request.Cron),
			TimeZone:                 request.TimeZone,
			RetentionCount:           request.RetentionCount,
			RateBytesPerSec:          request.RateBytesPerSec,
			WorkersPerNode:           request.WorkersPerNode,
			MaxDurationMillis:        request.MaxDuration.Milliseconds(),
			ScheduleCursorUnixMillis: now.UnixMilli(),
			CreatedUnixMillis:        createdAt,
			UpdatedUnixMillis:        now.UnixMilli(),
		}
		next := current.Clone()
		next.Revision++
		next.Plan = &plan
		var initial *backupcontract.BackupJob
		if request.Enabled && !wasEnabled {
			job, err := s.newBackupJob(plan, backupcontract.TriggerInitial, time.Time{}, now)
			if err != nil {
				return ConfigureResult{}, err
			}
			next.ActiveBackup = &job
			initial = &job
		}
		if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return ConfigureResult{}, err
		}
		return ConfigureResult{Plan: plan, InitialJob: initial}, nil
	}
	return ConfigureResult{}, ErrStateConflict
}

// AdvanceBackupPhase performs a fenced, idempotent one-way publication
// transition. The transition timestamp is also the deterministic archive
// completion time used by every publication retry.
func (s *ScheduledService) AdvanceBackupPhase(
	ctx context.Context,
	request AdvanceBackupPhaseRequest,
) error {
	if strings.TrimSpace(request.JobID) == "" ||
		!validBackupPhaseTransition(request.From, request.To) {
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackup == nil || current.ActiveBackup.ID != request.JobID {
		return ErrStateConflict
	}
	if current.ActiveBackup.Status == request.To {
		return nil
	}
	if current.ActiveBackup.Status != request.From {
		return ErrStateConflict
	}
	if request.To == backupcontract.JobStatusPublishing &&
		(current.ActiveBackup.CancelRequested ||
			!s.now().UTC().Before(
				time.UnixMilli(current.ActiveBackup.DeadlineUnixMillis),
			)) {
		return ErrStateConflict
	}
	for _, slot := range current.ActiveBackup.Slots {
		if slot.Status != backupcontract.SlotStatusComplete {
			return ErrStateConflict
		}
	}
	next := current.Clone()
	next.Revision++
	next.ActiveBackup.Status = request.To
	next.ActiveBackup.UpdatedUnixMillis = s.now().UTC().UnixMilli()
	return s.store.CompareAndSwap(ctx, current.Revision, next)
}

// StartBackup admits exactly one manual or scheduled full-backup job.
func (s *ScheduledService) StartBackup(
	ctx context.Context,
	request StartBackupRequest,
) (backupcontract.BackupJob, error) {
	if request.Trigger != backupcontract.TriggerManual &&
		request.Trigger != backupcontract.TriggerScheduled {
		return backupcontract.BackupJob{}, ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return backupcontract.BackupJob{}, err
	}
	if current.Plan == nil {
		return backupcontract.BackupJob{}, ErrDisabled
	}
	if current.ActiveBackup != nil {
		return backupcontract.BackupJob{}, ErrBackupJobActive
	}
	if current.ActiveRestore != nil {
		return backupcontract.BackupJob{}, ErrRestoreJobActive
	}
	now := s.now().UTC()
	job, err := s.newBackupJob(*current.Plan, request.Trigger, request.ScheduledAt, now)
	if err != nil {
		return backupcontract.BackupJob{}, err
	}
	next := current.Clone()
	next.Revision++
	next.ActiveBackup = &job
	if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
		return backupcontract.BackupJob{}, err
	}
	return job, nil
}

// EvaluateSchedule admits one on-time Cron occurrence, records overlap as
// skipped, and advances stale cursors without catch-up jobs.
func (s *ScheduledService) EvaluateSchedule(
	ctx context.Context,
	maxLateness time.Duration,
) (ScheduleResult, error) {
	if maxLateness <= 0 || maxLateness > time.Hour {
		return ScheduleResult{}, ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return ScheduleResult{}, err
	}
	if current.Plan == nil || !current.Plan.Enabled {
		return ScheduleResult{}, nil
	}
	location, schedule, err := parsePlanSchedule(*current.Plan)
	if err != nil {
		return ScheduleResult{}, err
	}
	now := s.now().UTC()
	cursor := time.UnixMilli(current.Plan.ScheduleCursorUnixMillis).In(location)
	occurrence := schedule.Next(cursor)
	if occurrence.After(now.In(location)) {
		return ScheduleResult{}, nil
	}
	next := current.Clone()
	next.Revision++
	next.Plan.ScheduleCursorUnixMillis = occurrence.UTC().UnixMilli()
	next.Plan.UpdatedUnixMillis = now.UnixMilli()
	result := ScheduleResult{Occurrence: occurrence.UTC()}
	if now.Sub(occurrence.UTC()) > maxLateness {
		next.Plan.ScheduleCursorUnixMillis = now.UnixMilli()
		if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
			return ScheduleResult{}, err
		}
		return result, nil
	}
	if current.ActiveBackup != nil || current.ActiveRestore != nil {
		record := backupcontract.TaskRecord{
			ID: fmt.Sprintf(
				"skip-%d-%d",
				current.Plan.Revision, occurrence.UTC().UnixMilli(),
			),
			Kind: "backup", Trigger: backupcontract.TriggerScheduled,
			Status:              string(backupcontract.JobStatusSkipped),
			StartedUnixMillis:   occurrence.UTC().UnixMilli(),
			CompletedUnixMillis: now.UnixMilli(),
			ScheduledUnixMillis: occurrence.UTC().UnixMilli(),
			ErrorCode:           "overlap",
		}
		next.History = append([]backupcontract.TaskRecord{record}, next.History...)
		if len(next.History) > backupcontract.MaxTaskHistory {
			next.History = next.History[:backupcontract.MaxTaskHistory]
		}
		result.Skipped = true
	} else {
		job, err := s.newBackupJob(
			*current.Plan, backupcontract.TriggerScheduled,
			occurrence.UTC(), now,
		)
		if err != nil {
			return ScheduleResult{}, err
		}
		next.ActiveBackup = &job
		result.Job = &job
	}
	if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
		return ScheduleResult{}, err
	}
	return result, nil
}

// ClaimSlot starts or resumes one Hash Slot under a monotonically newer
// authority fence.
func (s *ScheduledService) ClaimSlot(
	ctx context.Context,
	request ClaimSlotRequest,
) (backupcontract.SlotProgress, error) {
	if request.JobID == "" || int(request.HashSlot) >= backupcontract.HashSlotCount ||
		request.OwnerNodeID == 0 || request.OwnerTerm == 0 {
		return backupcontract.SlotProgress{}, ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return backupcontract.SlotProgress{}, err
	}
	if current.ActiveBackup == nil || current.ActiveBackup.ID != request.JobID {
		return backupcontract.SlotProgress{}, ErrStateConflict
	}
	slot := current.ActiveBackup.Slots[request.HashSlot]
	if slot.Status == backupcontract.SlotStatusComplete {
		return backupcontract.SlotProgress{}, ErrStateConflict
	}
	if slot.Status == backupcontract.SlotStatusRunning {
		if slot.OwnerNodeID == request.OwnerNodeID && slot.OwnerTerm == request.OwnerTerm {
			return slot, nil
		}
		if request.OwnerTerm <= slot.OwnerTerm {
			return backupcontract.SlotProgress{}, ErrStateConflict
		}
	}
	now := s.now().UTC().UnixMilli()
	slot.Status = backupcontract.SlotStatusRunning
	slot.Attempt++
	slot.OwnerNodeID = request.OwnerNodeID
	slot.OwnerTerm = request.OwnerTerm
	slot.UpdatedUnixMillis = now
	slot.ErrorCode = ""
	next := current.Clone()
	next.Revision++
	next.ActiveBackup.Status = backupcontract.JobStatusExporting
	next.ActiveBackup.UpdatedUnixMillis = now
	next.ActiveBackup.Slots[request.HashSlot] = slot
	if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
		return backupcontract.SlotProgress{}, err
	}
	return slot, nil
}

// CompleteSlot accepts only the exact active attempt and advances bounded job
// totals once.
func (s *ScheduledService) CompleteSlot(
	ctx context.Context,
	request CompleteSlotRequest,
) error {
	if request.JobID == "" || int(request.HashSlot) >= backupcontract.HashSlotCount ||
		request.Attempt == 0 || request.OwnerNodeID == 0 || request.OwnerTerm == 0 ||
		len(request.ManifestKey) == 0 || len(request.ManifestKey) > 512 ||
		len(request.ManifestSHA256) != 64 {
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackup == nil || current.ActiveBackup.ID != request.JobID {
		return ErrStateConflict
	}
	slot := current.ActiveBackup.Slots[request.HashSlot]
	if slot.Status != backupcontract.SlotStatusRunning ||
		slot.Attempt != request.Attempt ||
		slot.OwnerNodeID != request.OwnerNodeID ||
		slot.OwnerTerm != request.OwnerTerm {
		return ErrStateConflict
	}
	now := s.now().UTC().UnixMilli()
	slot.Status = backupcontract.SlotStatusComplete
	slot.ManifestKey = request.ManifestKey
	slot.ManifestSHA256 = request.ManifestSHA256
	slot.LogicalBytes = request.LogicalBytes
	slot.StoredBytes = request.StoredBytes
	slot.Records = request.Records
	slot.MaxMessageID = request.MaxMessageID
	slot.UpdatedUnixMillis = now
	slot.ErrorCode = ""
	next := current.Clone()
	next.Revision++
	next.ActiveBackup.Slots[request.HashSlot] = slot
	next.ActiveBackup.LogicalBytes += request.LogicalBytes
	next.ActiveBackup.StoredBytes += request.StoredBytes
	next.ActiveBackup.Records += request.Records
	next.ActiveBackup.UpdatedUnixMillis = now
	if err := s.store.CompareAndSwap(ctx, current.Revision, next); err != nil {
		return err
	}
	return nil
}

// FailSlot accepts only the exact active attempt and makes it retryable.
func (s *ScheduledService) FailSlot(
	ctx context.Context,
	request FailSlotRequest,
) error {
	if request.JobID == "" || int(request.HashSlot) >= backupcontract.HashSlotCount ||
		request.Attempt == 0 || request.OwnerNodeID == 0 ||
		request.OwnerTerm == 0 || request.ErrorCode == "" ||
		len(request.ErrorCode) > 128 {
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackup == nil || current.ActiveBackup.ID != request.JobID {
		return ErrStateConflict
	}
	slot := current.ActiveBackup.Slots[request.HashSlot]
	if slot.Status != backupcontract.SlotStatusRunning ||
		slot.Attempt != request.Attempt ||
		slot.OwnerNodeID != request.OwnerNodeID ||
		slot.OwnerTerm != request.OwnerTerm {
		return ErrStateConflict
	}
	now := s.now().UTC().UnixMilli()
	slot.Status = backupcontract.SlotStatusFailed
	slot.UpdatedUnixMillis = now
	slot.ErrorCode = request.ErrorCode
	next := current.Clone()
	next.Revision++
	next.ActiveBackup.Slots[request.HashSlot] = slot
	next.ActiveBackup.UpdatedUnixMillis = now
	return s.store.CompareAndSwap(ctx, current.Revision, next)
}

// RequestBackupCancellation durably asks workers to stop before publication.
func (s *ScheduledService) RequestBackupCancellation(ctx context.Context, jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackup == nil || current.ActiveBackup.ID != jobID {
		return ErrStateConflict
	}
	if current.ActiveBackup.Status == backupcontract.JobStatusPublishing ||
		current.ActiveBackup.Status == backupcontract.JobStatusCleaning {
		return ErrStateConflict
	}
	if current.ActiveBackup.CancelRequested {
		return nil
	}
	next := current.Clone()
	next.Revision++
	next.ActiveBackup.CancelRequested = true
	next.ActiveBackup.UpdatedUnixMillis = s.now().UTC().UnixMilli()
	return s.store.CompareAndSwap(ctx, current.Revision, next)
}

// FinishBackup clears the active job and appends one bounded terminal record.
func (s *ScheduledService) FinishBackup(
	ctx context.Context,
	request FinishBackupRequest,
) error {
	if strings.TrimSpace(request.JobID) == "" {
		return ErrInvalidRequest
	}
	switch request.Status {
	case backupcontract.JobStatusSucceeded,
		backupcontract.JobStatusFailed,
		backupcontract.JobStatusCanceled:
	default:
		return ErrInvalidRequest
	}
	if len(request.ErrorCode) > 128 {
		return ErrInvalidRequest
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackup == nil || current.ActiveBackup.ID != request.JobID {
		return ErrStateConflict
	}
	if request.Status == backupcontract.JobStatusSucceeded {
		for _, slot := range current.ActiveBackup.Slots {
			if slot.Status != backupcontract.SlotStatusComplete {
				return ErrStateConflict
			}
		}
	}
	if request.Status == backupcontract.JobStatusCanceled &&
		!current.ActiveBackup.CancelRequested {
		return ErrStateConflict
	}
	now := s.now().UTC().UnixMilli()
	record := backupcontract.TaskRecord{
		ID:                  current.ActiveBackup.ID,
		Kind:                "backup",
		Trigger:             current.ActiveBackup.Trigger,
		Status:              string(request.Status),
		StartedUnixMillis:   current.ActiveBackup.StartedAtUnixMillis,
		CompletedUnixMillis: now,
		ScheduledUnixMillis: current.ActiveBackup.ScheduledAtUnixMillis,
		ErrorCode:           request.ErrorCode,
	}
	next := current.Clone()
	next.Revision++
	next.ActiveBackup = nil
	next.History = append([]backupcontract.TaskRecord{record}, next.History...)
	if len(next.History) > backupcontract.MaxTaskHistory {
		next.History = next.History[:backupcontract.MaxTaskHistory]
	}
	return s.store.CompareAndSwap(ctx, current.Revision, next)
}

func (s *ScheduledService) newBackupJob(
	plan backupcontract.Plan,
	trigger backupcontract.Trigger,
	scheduledAt time.Time,
	now time.Time,
) (backupcontract.BackupJob, error) {
	id := strings.TrimSpace(s.newID())
	if id == "" {
		return backupcontract.BackupJob{}, fmt.Errorf("%w: backup job id", ErrInvalidRequest)
	}
	slots := make([]backupcontract.SlotProgress, backupcontract.HashSlotCount)
	for slot := range slots {
		slots[slot] = backupcontract.SlotProgress{
			HashSlot: uint16(slot),
			Status:   backupcontract.SlotStatusPending,
		}
	}
	job := backupcontract.BackupJob{
		ID:                  id,
		Trigger:             trigger,
		Status:              backupcontract.JobStatusPreparing,
		PlanRevision:        plan.Revision,
		StartedAtUnixMillis: now.UnixMilli(),
		DeadlineUnixMillis:  now.Add(time.Duration(plan.MaxDurationMillis) * time.Millisecond).UnixMilli(),
		UpdatedUnixMillis:   now.UnixMilli(),
		Slots:               slots,
	}
	if !scheduledAt.IsZero() {
		job.ScheduledAtUnixMillis = scheduledAt.UTC().UnixMilli()
	}
	return job, nil
}

func validateConfigureRequest(request ConfigureRequest, now time.Time) error {
	if request.RetentionCount < 1 || request.RetentionCount > 1000 ||
		request.RateBytesPerSec == 0 ||
		request.WorkersPerNode < 1 || request.WorkersPerNode > 4 ||
		request.MaxDuration < time.Hour || request.MaxDuration > 48*time.Hour {
		return ErrInvalidRequest
	}
	switch request.Store.Kind {
	case backupcontract.StoreKindFile:
		if request.Store.Endpoint != "" || request.Store.Bucket != "" ||
			request.Store.Prefix != "" || len(request.Store.CredentialCiphertext) != 0 {
			return ErrInvalidRequest
		}
	case backupcontract.StoreKindS3:
		if request.Store.Endpoint == "" || request.Store.Bucket == "" ||
			request.Store.Prefix == "" || len(request.Store.CredentialCiphertext) == 0 {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	location, err := time.LoadLocation(request.TimeZone)
	if err != nil {
		return fmt.Errorf("%w: time zone: %v", ErrInvalidRequest, err)
	}
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
	schedule, err := parser.Parse(strings.TrimSpace(request.Cron))
	if err != nil {
		return fmt.Errorf("%w: Cron: %v", ErrInvalidRequest, err)
	}
	occurrence := schedule.Next(now.In(location))
	horizon := occurrence.AddDate(5, 0, 0)
	for range 10_000 {
		next := schedule.Next(occurrence)
		if next.Sub(occurrence) < 12*time.Hour {
			return fmt.Errorf("%w: schedule occurrences must be at least twelve hours apart", ErrInvalidRequest)
		}
		occurrence = next
		if !occurrence.Before(horizon) {
			return nil
		}
	}
	return fmt.Errorf("%w: schedule validation horizon exceeded", ErrInvalidRequest)
}

func parsePlanSchedule(
	plan backupcontract.Plan,
) (*time.Location, cron.Schedule, error) {
	location, err := time.LoadLocation(plan.TimeZone)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: time zone: %v", ErrInvalidRequest, err)
	}
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
	schedule, err := parser.Parse(strings.TrimSpace(plan.Cron))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: Cron: %v", ErrInvalidRequest, err)
	}
	return location, schedule, nil
}

func cloneStoreConfig(store backupcontract.StoreConfig) backupcontract.StoreConfig {
	clone := store
	clone.CredentialCiphertext = append([]byte(nil), store.CredentialCiphertext...)
	return clone
}

func validBackupPhaseTransition(
	from backupcontract.JobStatus,
	to backupcontract.JobStatus,
) bool {
	return from == backupcontract.JobStatusExporting &&
		to == backupcontract.JobStatusPublishing ||
		from == backupcontract.JobStatusPreparing &&
			to == backupcontract.JobStatusPublishing ||
		from == backupcontract.JobStatusPublishing &&
			to == backupcontract.JobStatusCleaning
}

func equalPlanConfiguration(
	plan backupcontract.Plan,
	request ConfigureRequest,
) bool {
	return plan.Store.Kind == request.Store.Kind &&
		plan.Store.Endpoint == request.Store.Endpoint &&
		plan.Store.Region == request.Store.Region &&
		plan.Store.Bucket == request.Store.Bucket &&
		plan.Store.Prefix == request.Store.Prefix &&
		plan.Store.PathStyle == request.Store.PathStyle &&
		string(plan.Store.CredentialCiphertext) ==
			string(request.Store.CredentialCiphertext) &&
		plan.Cron == strings.TrimSpace(request.Cron) &&
		plan.TimeZone == request.TimeZone &&
		plan.RetentionCount == request.RetentionCount &&
		plan.RateBytesPerSec == request.RateBytesPerSec &&
		plan.WorkersPerNode == request.WorkersPerNode &&
		plan.MaxDurationMillis == request.MaxDuration.Milliseconds()
}
