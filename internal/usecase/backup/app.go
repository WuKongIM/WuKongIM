package backup

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	controllerstate "github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

const maxStateRetries = 8

const defaultMaxVerificationAge = 24 * time.Hour
const defaultVerificationTimeout = 30 * time.Minute

// Options configures the entry-independent backup coordinator.
type Options struct {
	// Enabled allows backup operations when true.
	Enabled bool
	// HashSlotCount is the immutable logical partition count.
	HashSlotCount uint16
	// Store persists bounded coordination state.
	Store StateStore
	// Publisher publishes complete restore points to repositories.
	Publisher RestorePointPublisher
	// Verifier performs explicit dual-repository audits.
	Verifier RestorePointVerifier
	// Now returns the current UTC time.
	Now func() time.Time
	// NewJobID returns a globally unique backup job identity.
	NewJobID func() string
	// MaxRecoveryPointAge is the RPO health threshold. Zero defaults to five minutes.
	MaxRecoveryPointAge time.Duration
	// MaxVerificationAge is the repository audit health threshold. Zero defaults to 24 hours.
	MaxVerificationAge time.Duration
	// VerificationTimeout bounds one full dual-repository graph audit.
	VerificationTimeout time.Duration
}

// App coordinates one cluster backup job without depending on entry or infrastructure packages.
type App struct {
	enabled             bool
	hashSlotCount       uint16
	store               StateStore
	publisher           RestorePointPublisher
	verifier            RestorePointVerifier
	now                 func() time.Time
	newJobID            func() string
	maxRecoveryPointAge time.Duration
	maxVerificationAge  time.Duration
	verificationTimeout time.Duration
}

// NewApp creates a backup coordinator.
func NewApp(options Options) (*App, error) {
	if options.HashSlotCount == 0 || options.Store == nil || options.Publisher == nil || options.Now == nil || options.NewJobID == nil {
		return nil, fmt.Errorf("%w: coordinator dependencies are incomplete", ErrInvalidRequest)
	}
	maxRecoveryPointAge := options.MaxRecoveryPointAge
	if maxRecoveryPointAge == 0 {
		maxRecoveryPointAge = 5 * time.Minute
	}
	if maxRecoveryPointAge < 0 {
		return nil, fmt.Errorf("%w: max recovery point age must be positive", ErrInvalidRequest)
	}
	maxVerificationAge := options.MaxVerificationAge
	if maxVerificationAge == 0 {
		maxVerificationAge = defaultMaxVerificationAge
	}
	if maxVerificationAge < 0 {
		return nil, fmt.Errorf("%w: max verification age must be positive", ErrInvalidRequest)
	}
	verificationTimeout := options.VerificationTimeout
	if verificationTimeout == 0 {
		verificationTimeout = defaultVerificationTimeout
	}
	if verificationTimeout < 0 {
		return nil, fmt.Errorf("%w: verification timeout must be positive", ErrInvalidRequest)
	}
	return &App{
		enabled:             options.Enabled,
		hashSlotCount:       options.HashSlotCount,
		store:               options.Store,
		publisher:           options.Publisher,
		verifier:            options.Verifier,
		now:                 options.Now,
		newJobID:            options.NewJobID,
		maxRecoveryPointAge: maxRecoveryPointAge,
		maxVerificationAge:  maxVerificationAge,
		verificationTimeout: verificationTimeout,
	}, nil
}

// Verify audits one published restore point without mutating coordination state.
func (a *App) Verify(ctx context.Context, restorePointID string) (Verification, error) {
	if !a.enabled {
		return Verification{}, ErrDisabled
	}
	if a.verifier == nil || strings.TrimSpace(restorePointID) == "" {
		return Verification{}, ErrInvalidRequest
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return Verification{}, err
	}
	found := false
	for _, point := range state.RestorePoints {
		if point.ID == restorePointID {
			found = true
			break
		}
	}
	if !found {
		return Verification{}, ErrRestorePointNotFound
	}
	result, err := a.verifier.Verify(ctx, restorePointID)
	if err != nil {
		return Verification{}, err
	}
	if result.RestorePointID != restorePointID || result.VerifiedAtUnixMillis <= 0 || !result.PrimaryVerified || !result.SecondaryVerified || len(result.ManifestSHA256) != 64 {
		return Verification{}, fmt.Errorf("%w: verifier returned incomplete evidence", ErrInvalidRequest)
	}
	return result, nil
}

// StartVerification creates the one cluster-wide durable manual audit task.
func (a *App) StartVerification(ctx context.Context, restorePointID string) (VerificationTask, error) {
	if !a.enabled {
		return VerificationTask{}, ErrDisabled
	}
	if a.verifier == nil {
		return VerificationTask{}, ErrInvalidRequest
	}
	restorePointID = strings.TrimSpace(restorePointID)
	taskID := strings.TrimSpace(a.newJobID())
	if restorePointID == "" || taskID == "" {
		return VerificationTask{}, ErrInvalidRequest
	}
	var task VerificationTask
	err := a.mutate(ctx, func(state *State) error {
		if state.Active != nil {
			return ErrJobActive
		}
		if verificationTaskActive(state.Verification) {
			return ErrVerificationJobActive
		}
		point := findRestorePoint(state.RestorePoints, restorePointID)
		if point == nil {
			return ErrRestorePointNotFound
		}
		evidence := VerificationEvidence{
			Status:              VerificationTaskPending,
			StartedAtUnixMillis: a.now().UTC().UnixMilli(),
		}
		task = VerificationTask{ID: taskID, RestorePointID: restorePointID, VerificationEvidence: evidence}
		state.Verification = &task
		point.LastVerification = &evidence
		return nil
	})
	return task, err
}

// RunVerification executes or resumes one durable manual audit task.
func (a *App) RunVerification(ctx context.Context, taskID string) (VerificationTask, error) {
	if !a.enabled {
		return VerificationTask{}, ErrDisabled
	}
	if a.verifier == nil || strings.TrimSpace(taskID) == "" {
		return VerificationTask{}, ErrInvalidRequest
	}
	var task VerificationTask
	err := a.mutate(ctx, func(state *State) error {
		if state.Verification == nil || state.Verification.ID != taskID {
			return ErrRestorePointNotFound
		}
		task = *state.Verification
		if task.Status == VerificationTaskSucceeded || task.Status == VerificationTaskFailed {
			return nil
		}
		task.Status = VerificationTaskRunning
		task.CompletedAtUnixMillis = 0
		task.FailureCategory = ""
		state.Verification = &task
		if point := findRestorePoint(state.RestorePoints, task.RestorePointID); point != nil {
			evidence := task.VerificationEvidence
			point.LastVerification = &evidence
		}
		return nil
	})
	if err != nil || task.Status == VerificationTaskSucceeded || task.Status == VerificationTaskFailed {
		return task, err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, a.verificationTimeout)
	result, verifyErr := a.verifier.Verify(verifyCtx, task.RestorePointID)
	cancel()
	if verifyErr == nil && (result.RestorePointID != task.RestorePointID || result.VerifiedAtUnixMillis <= 0 ||
		!result.PrimaryVerified || !result.SecondaryVerified || len(result.ManifestSHA256) != 64) {
		verifyErr = fmt.Errorf("%w: verifier returned incomplete evidence", ErrInvalidRequest)
	}
	if (errors.Is(verifyErr, context.Canceled) || errors.Is(verifyErr, context.DeadlineExceeded)) && ctx.Err() != nil {
		_ = a.mutate(context.WithoutCancel(ctx), func(state *State) error {
			if state.Verification == nil || state.Verification.ID != taskID || state.Verification.Status != VerificationTaskRunning {
				return nil
			}
			state.Verification.Status = VerificationTaskPending
			if point := findRestorePoint(state.RestorePoints, task.RestorePointID); point != nil && point.LastVerification != nil {
				point.LastVerification.Status = VerificationTaskPending
			}
			return nil
		})
		return task, verifyErr
	}
	err = a.mutate(ctx, func(state *State) error {
		if state.Verification == nil || state.Verification.ID != taskID {
			return ErrStateConflict
		}
		completed := *state.Verification
		if verifyErr != nil {
			completed.Status = VerificationTaskFailed
			completed.CompletedAtUnixMillis = a.now().UTC().UnixMilli()
			completed.FailureCategory = "verification_failed"
			if errors.Is(verifyErr, context.DeadlineExceeded) {
				completed.FailureCategory = "verification_timeout"
			}
		} else {
			completed.Status = VerificationTaskSucceeded
			completed.CompletedAtUnixMillis = result.VerifiedAtUnixMillis
			completed.PrimaryVerified = result.PrimaryVerified
			completed.SecondaryVerified = result.SecondaryVerified
			completed.ManifestSHA256 = result.ManifestSHA256
			completed.FailureCategory = ""
		}
		state.Verification = &completed
		if point := findRestorePoint(state.RestorePoints, completed.RestorePointID); point != nil {
			evidence := completed.VerificationEvidence
			point.LastVerification = &evidence
		}
		task = completed
		return nil
	})
	if err != nil {
		return VerificationTask{}, err
	}
	return task, verifyErr
}

func verificationTaskActive(task *VerificationTask) bool {
	return task != nil && (task.Status == VerificationTaskPending || task.Status == VerificationTaskRunning)
}

func findRestorePoint(points []RestorePoint, restorePointID string) *RestorePoint {
	for index := range points {
		if points[index].ID == restorePointID {
			return &points[index]
		}
	}
	return nil
}

// Status returns a detached snapshot while preserving unknown recovery-point evidence.
func (a *App) Status(ctx context.Context) (StatusSnapshot, error) {
	if !a.enabled {
		return StatusSnapshot{Enabled: false, Health: HealthDisabled}, nil
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	snapshot := StatusSnapshot{
		Enabled: true, Health: HealthUnknown, Active: cloneJob(state.Active),
		Verification: cloneVerificationTask(state.Verification), PendingGarbageCount: len(state.PendingGarbage),
		MaxRecoveryPointAgeSeconds: int64(a.maxRecoveryPointAge / time.Second),
		MaxVerificationAgeSeconds:  int64(a.maxVerificationAge / time.Second),
	}
	snapshot.Capacity = backupCapacitySnapshot(state)
	if len(state.RestorePoints) > 0 {
		latest := state.RestorePoints[0]
		for _, candidate := range state.RestorePoints[1:] {
			if candidate.EffectiveAtUnixMillis > latest.EffectiveAtUnixMillis ||
				(candidate.EffectiveAtUnixMillis == latest.EffectiveAtUnixMillis && candidate.CreatedAtUnixMillis > latest.CreatedAtUnixMillis) {
				latest = candidate
			}
		}
		latest = cloneRestorePoints([]RestorePoint{latest})[0]
		snapshot.Latest = &latest
		age := a.now().UTC().Unix() - time.UnixMilli(latest.EffectiveAtUnixMillis).UTC().Unix()
		if age < 0 {
			age = 0
		}
		snapshot.RecoveryPointAgeSeconds = &age
		if time.Duration(age)*time.Second > a.maxRecoveryPointAge {
			snapshot.Health = HealthDegraded
		} else {
			snapshot.Health = HealthHealthy
		}
	}
	if state.Active != nil {
		switch state.Active.Status {
		case JobStatusDegraded:
			snapshot.Health = HealthDegraded
		case JobStatusFailed:
			snapshot.Health = HealthFailed
		}
	}
	var latestVerification *VerificationEvidence
	if state.Verification != nil && state.Verification.CompletedAtUnixMillis > 0 {
		evidence := state.Verification.VerificationEvidence
		latestVerification = &evidence
	}
	for _, point := range state.RestorePoints {
		if point.LastVerification != nil &&
			point.LastVerification.CompletedAtUnixMillis > 0 &&
			(latestVerification == nil || point.LastVerification.CompletedAtUnixMillis > latestVerification.CompletedAtUnixMillis) {
			evidence := *point.LastVerification
			latestVerification = &evidence
		}
	}
	if latestVerification != nil && latestVerification.Status == VerificationTaskFailed {
		snapshot.Health = HealthFailed
		snapshot.FailureCategory = latestVerification.FailureCategory
	}
	if latestVerification != nil && latestVerification.Status == VerificationTaskSucceeded {
		age := a.now().UTC().Unix() - time.UnixMilli(latestVerification.CompletedAtUnixMillis).UTC().Unix()
		if age < 0 {
			age = 0
		}
		snapshot.VerificationAgeSeconds = &age
		if time.Duration(age)*time.Second > a.maxVerificationAge && snapshot.Health == HealthHealthy {
			snapshot.Health = HealthDegraded
		}
	}
	return snapshot, nil
}

func backupCapacitySnapshot(state State) CapacitySnapshot {
	const warningPercent = 80
	const criticalPercent = 95
	maximum := controllerstate.MaxBackupRestorePoints
	warningAt := maximum * warningPercent / 100
	criticalAt := maximum * criticalPercent / 100
	held := 0
	for _, point := range state.RestorePoints {
		if point.Held {
			held++
		}
	}
	total := len(state.RestorePoints) + len(state.PendingGarbage)
	level := "normal"
	if total >= criticalAt {
		level = "critical"
	} else if total >= warningAt {
		level = "warning"
	}
	return CapacitySnapshot{
		Total: total, Held: held, Pending: len(state.PendingGarbage), Max: maximum,
		WarningAt: warningAt, CriticalAt: criticalAt, Level: level,
	}
}

func cloneVerificationTask(task *VerificationTask) *VerificationTask {
	if task == nil {
		return nil
	}
	copy := *task
	return &copy
}

// Trigger creates the only active backup job.
func (a *App) Trigger(ctx context.Context, request TriggerRequest) (Job, error) {
	if !a.enabled {
		return Job{}, ErrDisabled
	}
	if !validKind(request.Kind) || strings.TrimSpace(request.ConfigFingerprint) == "" {
		return Job{}, fmt.Errorf("%w: trigger kind and config fingerprint are required", ErrInvalidRequest)
	}
	if request.Kind == backupartifact.RestorePointSyntheticFull {
		return Job{}, fmt.Errorf("%w: synthetic-full publication is unavailable until independent object-reuse flattening is qualified", ErrInvalidRequest)
	}
	jobID := strings.TrimSpace(a.newJobID())
	if jobID == "" {
		return Job{}, fmt.Errorf("%w: generated job id is empty", ErrInvalidRequest)
	}
	var created Job
	err := a.mutate(ctx, func(state *State) error {
		if state.Active != nil {
			return ErrJobActive
		}
		if verificationTaskActive(state.Verification) {
			return ErrVerificationJobActive
		}
		now := a.now().UTC().UnixMilli()
		created = Job{
			ID:                  jobID,
			Epoch:               state.LastEpoch + 1,
			Kind:                request.Kind,
			Status:              JobStatusCapturing,
			HashSlotCount:       a.hashSlotCount,
			ConfigFingerprint:   request.ConfigFingerprint,
			RestorePointID:      "restore-" + jobID,
			StartedAtUnixMillis: now,
			UpdatedAtUnixMillis: now,
			Partitions:          []PartitionReport{},
		}
		if request.Kind == backupartifact.RestorePointIncremental {
			if latest, ok := latestRestorePoint(state.RestorePoints); ok {
				created.BaseRestorePointID = latest.ID
			}
		}
		state.LastEpoch = created.Epoch
		state.Active = cloneJob(&created)
		return nil
	})
	return created, err
}

// CoordinationState returns a detached state snapshot for the runtime scheduler.
func (a *App) CoordinationState(ctx context.Context) (State, error) {
	if !a.enabled {
		return State{}, ErrDisabled
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	return state.Clone(), nil
}

// ReserveErasureLedgerCommit allocates the next contiguous ledger sequence
// while keeping Controller state bounded to one pending immutable record.
func (a *App) ReserveErasureLedgerCommit(ctx context.Context, reference ErasureLedgerRecordReference) (ErasureLedgerRecordReference, error) {
	if !a.enabled {
		return ErasureLedgerRecordReference{}, ErrDisabled
	}
	reference.EventID = strings.TrimSpace(reference.EventID)
	reference.RecordKey = strings.TrimSpace(reference.RecordKey)
	reference.RecordSHA256 = strings.TrimSpace(reference.RecordSHA256)
	if reference.Sequence != 0 || !validErasureLedgerRecordReference(reference) {
		return ErasureLedgerRecordReference{}, fmt.Errorf("%w: invalid erasure ledger record reference", ErrInvalidRequest)
	}
	var reserved ErasureLedgerRecordReference
	err := a.mutate(ctx, func(state *State) error {
		if state.PendingErasureLedger != nil {
			pending := *state.PendingErasureLedger
			candidate := reference
			candidate.Sequence = pending.Sequence
			if pending == candidate {
				reserved = pending
				return nil
			}
			return ErrErasureLedgerPending
		}
		if state.LastCommittedErasureLedger != nil {
			committed := *state.LastCommittedErasureLedger
			candidate := reference
			candidate.Sequence = committed.Sequence
			if committed == candidate {
				reserved = committed
				return nil
			}
		}
		if state.ErasureLedgerBoundary == math.MaxUint64 {
			return fmt.Errorf("%w: erasure ledger sequence exhausted", ErrStateConflict)
		}
		reserved = reference
		reserved.Sequence = state.ErasureLedgerBoundary + 1
		state.PendingErasureLedger = &reserved
		return nil
	})
	return reserved, err
}

// CommitErasureLedgerCommit advances the contiguous boundary after both
// repositories durably contain the signed commit marker.
func (a *App) CommitErasureLedgerCommit(ctx context.Context, sequence uint64, eventID string) error {
	if !a.enabled {
		return ErrDisabled
	}
	eventID = strings.TrimSpace(eventID)
	if sequence == 0 || !validLowerSHA256(eventID) {
		return fmt.Errorf("%w: invalid erasure ledger commit identity", ErrInvalidRequest)
	}
	return a.mutate(ctx, func(state *State) error {
		if state.PendingErasureLedger == nil {
			if state.ErasureLedgerBoundary == sequence {
				return nil
			}
			return fmt.Errorf("%w: erasure ledger commit is not pending", ErrStateConflict)
		}
		pending := state.PendingErasureLedger
		if pending.Sequence != sequence || pending.EventID != eventID || sequence != state.ErasureLedgerBoundary+1 {
			return fmt.Errorf("%w: erasure ledger commit fence mismatch", ErrStateConflict)
		}
		committed := *pending
		state.ErasureLedgerBoundary = sequence
		state.PendingErasureLedger = nil
		state.LastCommittedErasureLedger = &committed
		return nil
	})
}

func validErasureLedgerRecordReference(reference ErasureLedgerRecordReference) bool {
	return validLowerSHA256(reference.EventID) && validLowerSHA256(reference.RecordSHA256) &&
		backupartifact.ValidateErasureLedgerRecordKey(reference.RecordKey, reference.EventID) == nil
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func latestRestorePoint(points []RestorePoint) (RestorePoint, bool) {
	if len(points) == 0 {
		return RestorePoint{}, false
	}
	latest := points[0]
	for _, point := range points[1:] {
		if point.EffectiveAtUnixMillis > latest.EffectiveAtUnixMillis || (point.EffectiveAtUnixMillis == latest.EffectiveAtUnixMillis && point.CreatedAtUnixMillis > latest.CreatedAtUnixMillis) {
			latest = point
		}
	}
	return latest, true
}

// Cancel cancels a capture before top-level publication begins.
func (a *App) Cancel(ctx context.Context, jobID string, backupEpoch uint64) (Job, error) {
	if !a.enabled {
		return Job{}, ErrDisabled
	}
	var canceled Job
	err := a.mutate(ctx, func(state *State) error {
		job, err := activeJob(state, jobID, backupEpoch)
		if err != nil {
			return err
		}
		if job.Status == JobStatusPublishing {
			return fmt.Errorf("%w: publishing job cannot be canceled", ErrStateConflict)
		}
		job.Status = JobStatusCanceled
		job.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		canceled = *cloneJob(job)
		state.Active = nil
		return nil
	})
	return canceled, err
}

// ListRestorePoints returns detached restore points newest first.
func (a *App) ListRestorePoints(ctx context.Context) ([]RestorePoint, error) {
	if !a.enabled {
		return nil, ErrDisabled
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	points := cloneRestorePoints(state.RestorePoints)
	sortRestorePoints(points)
	return points, nil
}

type restorePointCursor struct {
	EffectiveAtUnixMillis int64  `json:"effective_at_unix_millis"`
	CreatedAtUnixMillis   int64  `json:"created_at_unix_millis"`
	ID                    string `json:"id"`
}

// ListRestorePointsPage returns one bounded, stable keyset page newest first.
func (a *App) ListRestorePointsPage(ctx context.Context, request RestorePointListRequest) (RestorePointPage, error) {
	if !a.enabled {
		return RestorePointPage{}, ErrDisabled
	}
	limit := request.Limit
	if limit == 0 {
		limit = DefaultRestorePointPageSize
	}
	if limit < 0 {
		return RestorePointPage{}, fmt.Errorf("%w: restore-point page limit must not be negative", ErrInvalidRequest)
	}
	if limit > MaxRestorePointPageSize {
		limit = MaxRestorePointPageSize
	}
	var cursor *restorePointCursor
	if strings.TrimSpace(request.Cursor) != "" {
		decoded, err := decodeRestorePointCursor(request.Cursor)
		if err != nil {
			return RestorePointPage{}, fmt.Errorf("%w: invalid restore-point cursor", ErrInvalidRequest)
		}
		cursor = &decoded
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return RestorePointPage{}, err
	}
	query := strings.ToLower(strings.TrimSpace(request.IDQuery))
	points := make([]RestorePoint, 0, len(state.RestorePoints))
	for _, point := range state.RestorePoints {
		if request.HeldOnly && !point.Held {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(point.ID), query) {
			continue
		}
		points = append(points, point)
	}
	points = cloneRestorePoints(points)
	sortRestorePoints(points)
	page := RestorePointPage{Items: []RestorePoint{}, Total: len(points)}
	for _, point := range points {
		if cursor != nil && !restorePointAfterCursor(point, *cursor) {
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = encodeRestorePointCursor(page.Items[len(page.Items)-1])
			break
		}
		page.Items = append(page.Items, point)
	}
	return page, nil
}

func sortRestorePoints(points []RestorePoint) {
	sort.Slice(points, func(i, j int) bool {
		if points[i].EffectiveAtUnixMillis != points[j].EffectiveAtUnixMillis {
			return points[i].EffectiveAtUnixMillis > points[j].EffectiveAtUnixMillis
		}
		if points[i].CreatedAtUnixMillis != points[j].CreatedAtUnixMillis {
			return points[i].CreatedAtUnixMillis > points[j].CreatedAtUnixMillis
		}
		return points[i].ID > points[j].ID
	})
}

func restorePointAfterCursor(point RestorePoint, cursor restorePointCursor) bool {
	if point.EffectiveAtUnixMillis != cursor.EffectiveAtUnixMillis {
		return point.EffectiveAtUnixMillis < cursor.EffectiveAtUnixMillis
	}
	if point.CreatedAtUnixMillis != cursor.CreatedAtUnixMillis {
		return point.CreatedAtUnixMillis < cursor.CreatedAtUnixMillis
	}
	return point.ID < cursor.ID
}

func encodeRestorePointCursor(point RestorePoint) string {
	payload, _ := json.Marshal(restorePointCursor{
		EffectiveAtUnixMillis: point.EffectiveAtUnixMillis,
		CreatedAtUnixMillis:   point.CreatedAtUnixMillis,
		ID:                    point.ID,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeRestorePointCursor(value string) (restorePointCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return restorePointCursor{}, err
	}
	var cursor restorePointCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || strings.TrimSpace(cursor.ID) == "" {
		return restorePointCursor{}, ErrInvalidRequest
	}
	return cursor, nil
}

// Hold prevents retention collection for one published restore point.
func (a *App) Hold(ctx context.Context, restorePointID string) (RestorePoint, error) {
	return a.setHold(ctx, restorePointID, true)
}

// Release removes an operator hold without bypassing repository object lock.
func (a *App) Release(ctx context.Context, restorePointID string) (RestorePoint, error) {
	return a.setHold(ctx, restorePointID, false)
}

func (a *App) setHold(ctx context.Context, restorePointID string, held bool) (RestorePoint, error) {
	if !a.enabled {
		return RestorePoint{}, ErrDisabled
	}
	restorePointID = strings.TrimSpace(restorePointID)
	if restorePointID == "" {
		return RestorePoint{}, ErrInvalidRequest
	}
	var updated RestorePoint
	err := a.mutate(ctx, func(state *State) error {
		for index := range state.RestorePoints {
			if state.RestorePoints[index].ID != restorePointID {
				continue
			}
			state.RestorePoints[index].Held = held
			updated = state.RestorePoints[index]
			return nil
		}
		return ErrRestorePointNotFound
	})
	return updated, err
}

// DecideSchedule chooses the next automatic job without mutating coordination state.
func DecideSchedule(now time.Time, state State, policy SchedulePolicy) (ScheduleDecision, error) {
	if policy.RestorePointInterval <= 0 || policy.SyntheticFullInterval <= 0 || policy.MaterializedFullInterval <= 0 {
		return ScheduleDecision{}, fmt.Errorf("%w: schedule intervals must be positive", ErrInvalidRequest)
	}
	if state.Active != nil {
		return ScheduleDecision{Reason: "job_active"}, nil
	}
	if verificationTaskActive(state.Verification) {
		return ScheduleDecision{Reason: "verification_active"}, nil
	}
	nowMillis := now.UTC().UnixMilli()
	if len(state.RestorePoints) == 0 {
		return ScheduleDecision{Due: true, Kind: backupartifact.RestorePointMaterializedFull, Reason: "baseline_missing"}, nil
	}
	latestMillis := int64(0)
	latestSyntheticMillis := int64(0)
	latestMaterializedMillis := int64(0)
	for _, point := range state.RestorePoints {
		if point.CreatedAtUnixMillis > latestMillis {
			latestMillis = point.CreatedAtUnixMillis
		}
		if (point.Kind == backupartifact.RestorePointSyntheticFull || point.Kind == backupartifact.RestorePointMaterializedFull) && point.CreatedAtUnixMillis > latestSyntheticMillis {
			latestSyntheticMillis = point.CreatedAtUnixMillis
		}
		if point.Kind == backupartifact.RestorePointMaterializedFull && point.CreatedAtUnixMillis > latestMaterializedMillis {
			latestMaterializedMillis = point.CreatedAtUnixMillis
		}
	}
	if latestMaterializedMillis == 0 || nowMillis-latestMaterializedMillis >= policy.MaterializedFullInterval.Milliseconds() {
		return ScheduleDecision{Due: true, Kind: backupartifact.RestorePointMaterializedFull, Reason: "materialized_full_due"}, nil
	}
	if latestSyntheticMillis == 0 || nowMillis-latestSyntheticMillis >= policy.SyntheticFullInterval.Milliseconds() {
		return ScheduleDecision{Due: true, Kind: backupartifact.RestorePointMaterializedFull, Reason: "independent_full_fallback_due"}, nil
	}
	if nowMillis-latestMillis >= policy.RestorePointInterval.Milliseconds() {
		return ScheduleDecision{Due: true, Kind: backupartifact.RestorePointIncremental, Reason: "restore_point_due"}, nil
	}
	return ScheduleDecision{Reason: "not_due"}, nil
}

// ReportPartition records one fenced logical hash-slot completion summary.
func (a *App) ReportPartition(ctx context.Context, report PartitionReport) (Job, error) {
	if !a.enabled {
		return Job{}, ErrDisabled
	}
	if err := validatePartitionReport(report, a.hashSlotCount); err != nil {
		return Job{}, err
	}
	var updated Job
	err := a.mutate(ctx, func(state *State) error {
		job, err := activeJob(state, report.JobID, report.BackupEpoch)
		if err != nil {
			return err
		}
		if job.Status != JobStatusCapturing && job.Status != JobStatusDegraded {
			return fmt.Errorf("%w: job status %q does not accept reports", ErrStateConflict, job.Status)
		}
		index := sort.Search(len(job.Partitions), func(index int) bool {
			return job.Partitions[index].HashSlot >= report.HashSlot
		})
		if index < len(job.Partitions) && job.Partitions[index].HashSlot == report.HashSlot {
			if job.Partitions[index] != report {
				return fmt.Errorf("%w: conflicting report for hash slot %d", ErrStateConflict, report.HashSlot)
			}
			updated = *cloneJob(job)
			return nil
		}
		job.Partitions = append(job.Partitions, PartitionReport{})
		copy(job.Partitions[index+1:], job.Partitions[index:])
		job.Partitions[index] = report
		job.Status = JobStatusCapturing
		job.FailureCategory = ""
		job.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		updated = *cloneJob(job)
		return nil
	})
	return updated, err
}

// Degrade records a bounded retryable failure without discarding completed partitions.
func (a *App) Degrade(ctx context.Context, jobID string, backupEpoch uint64, category string) error {
	if !a.enabled {
		return ErrDisabled
	}
	category = strings.TrimSpace(category)
	if category == "" || len(category) > 128 {
		return ErrInvalidRequest
	}
	return a.markDegraded(ctx, jobID, backupEpoch, category)
}

// Publish publishes one all-partitions-complete job and records its restore point.
func (a *App) Publish(ctx context.Context, jobID string, backupEpoch uint64) (RestorePoint, error) {
	if !a.enabled {
		return RestorePoint{}, ErrDisabled
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return RestorePoint{}, err
	}
	job, err := activeJob(&state, jobID, backupEpoch)
	if err != nil {
		return RestorePoint{}, err
	}
	if !partitionsComplete(*job) {
		return RestorePoint{}, ErrPartitionsIncomplete
	}
	jobSnapshot := *cloneJob(job)
	if err := a.transitionToPublishing(ctx, jobID, backupEpoch); err != nil {
		return RestorePoint{}, err
	}
	jobSnapshot.Status = JobStatusPublishing
	jobSnapshot.FailureCategory = ""
	restorePoint, err := a.publisher.Publish(ctx, jobSnapshot)
	if err != nil {
		_ = a.markDegraded(ctx, jobID, backupEpoch, "repository_publish")
		return RestorePoint{}, err
	}
	if err := validatePublishedRestorePoint(restorePoint, jobSnapshot); err != nil {
		_ = a.markDegraded(ctx, jobID, backupEpoch, "invalid_restore_point")
		return RestorePoint{}, err
	}
	if err := a.complete(ctx, jobID, backupEpoch, restorePoint); err != nil {
		return RestorePoint{}, err
	}
	return restorePoint, nil
}

func (a *App) mutate(ctx context.Context, update func(*State) error) error {
	for attempt := 0; attempt < maxStateRetries; attempt++ {
		state, err := a.store.Load(ctx)
		if err != nil {
			return err
		}
		next := state.Clone()
		if err := update(&next); err != nil {
			return err
		}
		if err := a.store.CompareAndSwap(ctx, state.Revision, next); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrStateConflict
}

func (a *App) transitionToPublishing(ctx context.Context, jobID string, epoch uint64) error {
	return a.mutate(ctx, func(state *State) error {
		job, err := activeJob(state, jobID, epoch)
		if err != nil {
			return err
		}
		switch job.Status {
		case JobStatusCapturing, JobStatusDegraded, JobStatusPublishing:
		default:
			return fmt.Errorf("%w: cannot publish job in status %q", ErrStateConflict, job.Status)
		}
		if !partitionsComplete(*job) {
			return ErrPartitionsIncomplete
		}
		job.Status = JobStatusPublishing
		job.FailureCategory = ""
		job.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		return nil
	})
}

func (a *App) markDegraded(ctx context.Context, jobID string, epoch uint64, category string) error {
	return a.mutate(ctx, func(state *State) error {
		job, err := activeJob(state, jobID, epoch)
		if err != nil {
			return err
		}
		job.Status = JobStatusDegraded
		job.FailureCategory = category
		job.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		return nil
	})
}

func (a *App) complete(ctx context.Context, jobID string, epoch uint64, restorePoint RestorePoint) error {
	return a.mutate(ctx, func(state *State) error {
		job, err := activeJob(state, jobID, epoch)
		if err != nil {
			return err
		}
		if job.Status != JobStatusPublishing {
			return fmt.Errorf("%w: cannot complete job in status %q", ErrStateConflict, job.Status)
		}
		job.Status = JobStatusCompleted
		job.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		state.RestorePoints = append(state.RestorePoints, restorePoint)
		state.Active = nil
		return nil
	})
}

func activeJob(state *State, jobID string, epoch uint64) (*Job, error) {
	if state.Active == nil || state.Active.ID != jobID || state.Active.Epoch != epoch {
		return nil, ErrJobNotFound
	}
	return state.Active, nil
}

func partitionsComplete(job Job) bool {
	if job.HashSlotCount == 0 || len(job.Partitions) != int(job.HashSlotCount) {
		return false
	}
	for index, report := range job.Partitions {
		if report.HashSlot != uint16(index) {
			return false
		}
	}
	return true
}

func validatePartitionReport(report PartitionReport, hashSlotCount uint16) error {
	if strings.TrimSpace(report.JobID) == "" || report.BackupEpoch == 0 || report.HashSlot >= hashSlotCount || report.CommittedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: partition identity or cut is invalid", ErrInvalidRequest)
	}
	if strings.TrimSpace(report.ManifestKey) == "" || report.ObjectCount == 0 || report.CiphertextBytes == 0 {
		return fmt.Errorf("%w: partition manifest summary is incomplete", ErrInvalidRequest)
	}
	if len(report.ManifestSHA256) != 64 {
		return fmt.Errorf("%w: partition manifest checksum is invalid", ErrInvalidRequest)
	}
	if _, err := hex.DecodeString(report.ManifestSHA256); err != nil || strings.ToLower(report.ManifestSHA256) != report.ManifestSHA256 {
		return fmt.Errorf("%w: partition manifest checksum is invalid", ErrInvalidRequest)
	}
	return nil
}

func validatePublishedRestorePoint(restorePoint RestorePoint, job Job) error {
	if strings.TrimSpace(restorePoint.ID) == "" || restorePoint.JobID != job.ID || restorePoint.BackupEpoch != job.Epoch || restorePoint.Kind != job.Kind {
		return fmt.Errorf("%w: publisher returned a mismatched restore point", ErrInvalidRequest)
	}
	if restorePoint.EffectiveAtUnixMillis <= 0 || restorePoint.CreatedAtUnixMillis < restorePoint.EffectiveAtUnixMillis || !restorePoint.PrimaryVerified || !restorePoint.SecondaryVerified {
		return fmt.Errorf("%w: publisher returned an unverified restore point", ErrInvalidRequest)
	}
	oldest := oldestPartitionWatermark(job.Partitions)
	if restorePoint.EffectiveAtUnixMillis != oldest {
		return fmt.Errorf("%w: restore point effective time %d does not match oldest partition watermark %d", ErrInvalidRequest, restorePoint.EffectiveAtUnixMillis, oldest)
	}
	if len(restorePoint.ManifestSHA256) != 64 {
		return fmt.Errorf("%w: restore point manifest checksum is invalid", ErrInvalidRequest)
	}
	if _, err := hex.DecodeString(restorePoint.ManifestSHA256); err != nil {
		return fmt.Errorf("%w: restore point manifest checksum is invalid", ErrInvalidRequest)
	}
	return nil
}

func oldestPartitionWatermark(partitions []PartitionReport) int64 {
	oldest := int64(0)
	for _, partition := range partitions {
		if oldest == 0 || partition.CommittedAtUnixMillis < oldest {
			oldest = partition.CommittedAtUnixMillis
		}
	}
	return oldest
}

func validKind(kind backupartifact.RestorePointKind) bool {
	switch kind {
	case backupartifact.RestorePointIncremental, backupartifact.RestorePointSyntheticFull, backupartifact.RestorePointMaterializedFull:
		return true
	default:
		return false
	}
}

func cloneJob(job *Job) *Job {
	if job == nil {
		return nil
	}
	out := *job
	out.Partitions = append([]PartitionReport(nil), job.Partitions...)
	return &out
}
