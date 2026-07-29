package backup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const maximumSlotExportAttempts uint32 = 3

// SlotAuthority identifies the current distributed owner of one Hash Slot export.
type SlotAuthority struct {
	// NodeID is the current physical Hash Slot leader.
	NodeID uint64
	// Term fences output to that exact leader tenure.
	Term uint64
}

// SlotExportResult summarizes one fully stored and verified Slot artifact.
type SlotExportResult struct {
	// ManifestKey selects the immutable attempt-scoped Slot manifest.
	ManifestKey string
	// ManifestSHA256 binds the completed Slot result to its stored manifest.
	ManifestSHA256 string
	// LogicalBytes is the decoded size represented by the Slot artifact.
	LogicalBytes uint64
	// StoredBytes is the compressed repository size.
	StoredBytes uint64
	// Records is the exact logical record count in the Slot artifact.
	Records uint64
	// MaxMessageID is the Slot's durable allocator evidence.
	MaxMessageID uint64
}

// ScheduledSlotExecutor resolves and executes one distributed Slot export.
type ScheduledSlotExecutor interface {
	Authority(context.Context, uint16) (SlotAuthority, error)
	ExportSlot(
		context.Context,
		backupcontract.Plan,
		string,
		uint16,
		uint32,
		SlotAuthority,
	) (SlotExportResult, error)
}

// ScheduledArchiveFinalizer verifies publication and applies retention.
type ScheduledArchiveFinalizer interface {
	Publish(
		context.Context,
		backupartifact.ArchiveStore,
		backupcontract.BackupJob,
	) error
	ApplyRetention(
		context.Context,
		backupartifact.ArchiveStore,
		int,
	) error
}

// JobRunnerOptions configures resumable scheduled full-backup execution.
type JobRunnerOptions struct {
	// Scheduled owns durable admission, progress, and terminal transitions.
	Scheduled *ScheduledService
	// Repository resolves the current plan store for every resumable step.
	Repository ArchiveRepositoryProvider
	// Slots executes authority-fenced exports with bounded node concurrency.
	Slots ScheduledSlotExecutor
	// Finalizer verifies publication and applies serialized retention.
	Finalizer ScheduledArchiveFinalizer
	// Now supplies deterministic deadline checks.
	Now func() time.Time
}

// JobRunner advances one per-node-bounded Slot batch or terminal transition.
type JobRunner struct {
	scheduled  *ScheduledService
	repository ArchiveRepositoryProvider
	slots      ScheduledSlotExecutor
	finalizer  ScheduledArchiveFinalizer
	now        func() time.Time
}

// NewJobRunner validates execution dependencies.
func NewJobRunner(options JobRunnerOptions) (*JobRunner, error) {
	if options.Scheduled == nil || options.Repository == nil ||
		options.Slots == nil || options.Finalizer == nil || options.Now == nil {
		return nil, fmt.Errorf("%w: backup runner dependencies", ErrInvalidRequest)
	}
	return &JobRunner{
		scheduled: options.Scheduled, repository: options.Repository,
		slots: options.Slots, finalizer: options.Finalizer, now: options.Now,
	}, nil
}

// RunOnce resumes the active job after failover without creating another job.
func (r *JobRunner) RunOnce(ctx context.Context) (bool, error) {
	state, err := r.scheduled.State(ctx)
	if err != nil {
		return false, err
	}
	if state.ActiveBackup == nil || state.Plan == nil {
		return false, nil
	}
	job := *state.ActiveBackup
	if job.PlanRevision != state.Plan.Revision {
		return true, r.abort(ctx, *state.Plan, job, backupcontract.JobStatusFailed, "plan_revision_changed")
	}
	if !repositoryIsVerified(*state.Plan) {
		return true, r.scheduled.FinishBackup(
			ctx,
			FinishBackupRequest{
				JobID:     job.ID,
				Status:    backupcontract.JobStatusFailed,
				ErrorCode: "repository_unverified",
			},
		)
	}
	switch job.Status {
	case backupcontract.JobStatusPublishing:
		return true, r.publish(ctx, *state.Plan, job)
	case backupcontract.JobStatusCleaning:
		return true, r.clean(ctx, *state.Plan, job)
	}
	if err := r.ensurePendingMarker(ctx, *state.Plan, job); err != nil {
		return true, err
	}
	if job.CancelRequested {
		return true, r.abort(ctx, *state.Plan, job, backupcontract.JobStatusCanceled, "")
	}
	if !r.now().UTC().Before(time.UnixMilli(job.DeadlineUnixMillis)) {
		return true, r.abort(ctx, *state.Plan, job, backupcontract.JobStatusFailed, "deadline_exceeded")
	}
	for _, slot := range job.Slots {
		if slot.Status == backupcontract.SlotStatusFailed &&
			slot.Attempt >= maximumSlotExportAttempts {
			return true, r.abort(
				ctx, *state.Plan, job,
				backupcontract.JobStatusFailed, "slot_retry_exhausted",
			)
		}
	}
	candidates, err := r.claimBatch(ctx, *state.Plan, job)
	if err != nil {
		return true, err
	}
	if len(candidates) > 0 {
		return true, r.runBatch(ctx, *state.Plan, job, candidates)
	}
	return true, r.scheduled.AdvanceBackupPhase(
		ctx,
		AdvanceBackupPhaseRequest{
			JobID: job.ID, From: job.Status,
			To: backupcontract.JobStatusPublishing,
		},
	)
}

func (r *JobRunner) publish(
	ctx context.Context,
	plan backupcontract.Plan,
	job backupcontract.BackupJob,
) error {
	store, err := r.repository.Open(ctx, plan.Store)
	if err != nil {
		return err
	}
	published, err := archiveCompleteExists(ctx, store, job.ID)
	if err != nil {
		return err
	}
	if !published &&
		(job.CancelRequested ||
			!r.now().UTC().Before(time.UnixMilli(job.DeadlineUnixMillis))) {
		status := backupcontract.JobStatusFailed
		errorCode := "deadline_exceeded"
		if job.CancelRequested {
			status = backupcontract.JobStatusCanceled
			errorCode = ""
		}
		return r.abort(ctx, plan, job, status, errorCode)
	}
	if err := r.finalizer.Publish(ctx, store, job); err != nil {
		published, publishedErr := archiveCompleteExists(ctx, store, job.ID)
		if publishedErr != nil {
			return errors.Join(err, publishedErr)
		}
		if isPermanentPublicationError(err) {
			if published {
				return r.scheduled.FinishBackup(ctx, FinishBackupRequest{
					JobID: job.ID, Status: backupcontract.JobStatusFailed,
					ErrorCode: "publication_failed",
				})
			}
			return r.abort(
				ctx, plan, job, backupcontract.JobStatusFailed,
				"publication_failed",
			)
		}
		return err
	}
	return r.scheduled.AdvanceBackupPhase(
		ctx,
		AdvanceBackupPhaseRequest{
			JobID: job.ID, From: backupcontract.JobStatusPublishing,
			To: backupcontract.JobStatusCleaning,
		},
	)
}

func archiveCompleteExists(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	jobID string,
) (bool, error) {
	reader, _, err := store.Open(ctx, "backups/"+jobID+"/COMPLETE")
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, reader.Close()
}

func isPermanentPublicationError(err error) bool {
	return errors.Is(err, backupartifact.ErrInvalidObject) ||
		errors.Is(err, backupartifact.ErrInvalidManifest) ||
		errors.Is(err, backupartifact.ErrUnsupportedVersion) ||
		errors.Is(err, backupartifact.ErrObjectCorrupt) ||
		errors.Is(err, backupartifact.ErrObjectNotFound) ||
		errors.Is(err, backupartifact.ErrRepositoryIncomplete)
}

func (r *JobRunner) clean(
	ctx context.Context,
	plan backupcontract.Plan,
	job backupcontract.BackupJob,
) error {
	cleanupCode := ""
	startedUnixMillis := r.now().UTC().UnixMilli()
	operation, err := r.scheduled.resumeArchiveOperation(
		ctx, "retention", job.ID,
	)
	if err != nil {
		return err
	}
	store, err := r.repository.Open(ctx, plan.Store)
	if err == nil {
		err = errors.Join(
			r.finalizer.ApplyRetention(ctx, store, plan.RetentionCount),
			store.Delete(ctx, "pending/"+job.ID),
		)
	}
	// Lease release is the previous coordinator's completion acknowledgement.
	// It is token-fenced rather than term-fenced so a successor never starts
	// retention before the previous worker has stopped its repository I/O.
	releaseContext, releaseCancel := context.WithTimeout(
		context.Background(), 10*time.Second,
	)
	releaseErr := r.scheduled.ReleaseArchiveOperation(
		releaseContext, operation.Token,
	)
	releaseCancel()
	if releaseErr != nil {
		return errors.Join(err, releaseErr)
	}
	if err != nil {
		cleanupCode = "cleanup_deferred"
	}
	if recordErr := r.recordRetention(
		ctx, job.ID, startedUnixMillis, cleanupCode,
	); recordErr != nil {
		return recordErr
	}
	return r.scheduled.FinishBackup(ctx, FinishBackupRequest{
		JobID: job.ID, Status: backupcontract.JobStatusSucceeded,
		ErrorCode: cleanupCode,
	})
}

func (r *JobRunner) recordRetention(
	ctx context.Context,
	jobID string,
	startedUnixMillis int64,
	errorCode string,
) error {
	status := backupcontract.JobStatusSucceeded
	if errorCode != "" {
		status = backupcontract.JobStatusFailed
	}
	return r.scheduled.RecordTask(ctx, RecordTaskRequest{
		ID: jobID, Kind: "retention", Status: status,
		StartedUnixMillis:   startedUnixMillis,
		CompletedUnixMillis: r.now().UTC().UnixMilli(),
		ErrorCode:           errorCode,
	})
}

func (r *JobRunner) ensurePendingMarker(
	ctx context.Context,
	plan backupcontract.Plan,
	job backupcontract.BackupJob,
) error {
	store, err := r.repository.Open(ctx, plan.Store)
	if err != nil {
		return err
	}
	body := strconv.FormatInt(job.StartedAtUnixMillis, 10)
	err = store.Put(ctx, backupartifact.PutObject{
		Key: "pending/" + job.ID, Body: strings.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	})
	if errors.Is(err, backupartifact.ErrObjectExists) {
		return nil
	}
	return err
}

func (r *JobRunner) abort(
	ctx context.Context,
	plan backupcontract.Plan,
	job backupcontract.BackupJob,
	status backupcontract.JobStatus,
	errorCode string,
) error {
	finishErr := r.scheduled.FinishBackup(ctx, FinishBackupRequest{
		JobID: job.ID, Status: status, ErrorCode: errorCode,
	})
	if finishErr != nil {
		return finishErr
	}
	store, openErr := r.repository.Open(ctx, plan.Store)
	if openErr != nil {
		return openErr
	}
	return errors.Join(
		store.DeletePrefix(ctx, "backups/"+strings.TrimSpace(job.ID)),
		store.Delete(ctx, "pending/"+job.ID),
	)
}

type claimedSlot struct {
	hashSlot  uint16
	authority SlotAuthority
	attempt   uint32
}

type slotOutcome struct {
	claim  claimedSlot
	result SlotExportResult
	err    error
}

func (r *JobRunner) claimBatch(
	ctx context.Context,
	plan backupcontract.Plan,
	job backupcontract.BackupJob,
) ([]claimedSlot, error) {
	claimed := make([]claimedSlot, 0, plan.WorkersPerNode)
	perNode := make(map[uint64]int)
	for _, slot := range job.Slots {
		if slot.Status == backupcontract.SlotStatusComplete {
			continue
		}
		authority, err := r.slots.Authority(ctx, slot.HashSlot)
		if err != nil {
			return nil, err
		}
		if perNode[authority.NodeID] >= plan.WorkersPerNode {
			continue
		}
		claim, err := r.scheduled.ClaimSlot(ctx, ClaimSlotRequest{
			JobID: job.ID, HashSlot: slot.HashSlot,
			OwnerNodeID: authority.NodeID, OwnerTerm: authority.Term,
		})
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, claimedSlot{
			hashSlot: slot.HashSlot, authority: authority, attempt: claim.Attempt,
		})
		perNode[authority.NodeID]++
	}
	return claimed, nil
}

func (r *JobRunner) runBatch(
	ctx context.Context,
	plan backupcontract.Plan,
	job backupcontract.BackupJob,
	claims []claimedSlot,
) error {
	remaining := time.UnixMilli(job.DeadlineUnixMillis).Sub(r.now().UTC())
	runContext, cancel := context.WithTimeout(
		ctx, remaining,
	)
	defer cancel()
	outcomes := make([]slotOutcome, len(claims))
	var wait sync.WaitGroup
	wait.Add(len(claims))
	for index, claim := range claims {
		index, claim := index, claim
		goruntimeregistry.SafeGo(
			nil, goruntimeregistry.TaskBackupScheduledSlotExport,
			func() {
				defer wait.Done()
				result, err := r.slots.ExportSlot(
					runContext, plan, job.ID, claim.hashSlot,
					claim.attempt, claim.authority,
				)
				outcomes[index] = slotOutcome{
					claim: claim, result: result, err: err,
				}
			},
		)
	}
	wait.Wait()
	if err := runContext.Err(); err != nil {
		// User cancellation, deadline, shutdown, and Controller leadership loss
		// are durable coordinator events. Leave claimed Slots resumable instead
		// of misclassifying them as failed export attempts.
		return err
	}
	var resultErr error
	for _, outcome := range outcomes {
		if outcome.err != nil {
			failErr := r.failSlot(ctx, FailSlotRequest{
				JobID: job.ID, HashSlot: outcome.claim.hashSlot,
				Attempt:     outcome.claim.attempt,
				OwnerNodeID: outcome.claim.authority.NodeID,
				OwnerTerm:   outcome.claim.authority.Term,
				ErrorCode:   "slot_export_failed",
			})
			resultErr = errors.Join(resultErr, outcome.err, failErr)
			continue
		}
		completeErr := r.completeSlot(ctx, CompleteSlotRequest{
			JobID: job.ID, HashSlot: outcome.claim.hashSlot,
			Attempt:        outcome.claim.attempt,
			OwnerNodeID:    outcome.claim.authority.NodeID,
			OwnerTerm:      outcome.claim.authority.Term,
			ManifestKey:    outcome.result.ManifestKey,
			ManifestSHA256: outcome.result.ManifestSHA256,
			LogicalBytes:   outcome.result.LogicalBytes,
			StoredBytes:    outcome.result.StoredBytes,
			Records:        outcome.result.Records,
			MaxMessageID:   outcome.result.MaxMessageID,
		})
		resultErr = errors.Join(resultErr, completeErr)
	}
	return resultErr
}

func (r *JobRunner) completeSlot(
	ctx context.Context,
	request CompleteSlotRequest,
) error {
	for range 16 {
		err := r.scheduled.CompleteSlot(ctx, request)
		if !errors.Is(err, ErrStateConflict) {
			return err
		}
		state, loadErr := r.scheduled.State(ctx)
		if loadErr != nil {
			return loadErr
		}
		if state.ActiveBackup == nil || state.ActiveBackup.ID != request.JobID {
			return ErrStateConflict
		}
		slot := state.ActiveBackup.Slots[request.HashSlot]
		if slot.Status == backupcontract.SlotStatusComplete {
			if slot.Attempt == request.Attempt &&
				slot.OwnerNodeID == request.OwnerNodeID &&
				slot.OwnerTerm == request.OwnerTerm &&
				slot.ManifestKey == request.ManifestKey &&
				slot.ManifestSHA256 == request.ManifestSHA256 {
				return nil
			}
			return ErrStateConflict
		}
		if slot.Status != backupcontract.SlotStatusRunning ||
			slot.Attempt != request.Attempt ||
			slot.OwnerNodeID != request.OwnerNodeID ||
			slot.OwnerTerm != request.OwnerTerm {
			return ErrStateConflict
		}
	}
	return ErrStateConflict
}

func (r *JobRunner) failSlot(
	ctx context.Context,
	request FailSlotRequest,
) error {
	for range 16 {
		err := r.scheduled.FailSlot(ctx, request)
		if !errors.Is(err, ErrStateConflict) {
			return err
		}
		state, loadErr := r.scheduled.State(ctx)
		if loadErr != nil {
			return loadErr
		}
		if state.ActiveBackup == nil || state.ActiveBackup.ID != request.JobID {
			return ErrStateConflict
		}
		slot := state.ActiveBackup.Slots[request.HashSlot]
		if slot.Status == backupcontract.SlotStatusFailed &&
			slot.Attempt == request.Attempt {
			return nil
		}
	}
	return ErrStateConflict
}
