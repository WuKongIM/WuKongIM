package presence

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// RecoveryProviderReason is a closed, identity-free provider failure vocabulary.
type RecoveryProviderReason uint8

const (
	RecoveryProviderInvalidInput RecoveryProviderReason = iota + 1
	RecoveryProviderResultBudget
	RecoveryProviderAdmission
	RecoveryProviderTargetBefore
	RecoveryProviderSnapshotBefore
	RecoveryProviderOwnerRoster
	RecoveryProviderOwnerUnavailable
	RecoveryProviderOwnerPage
	RecoveryProviderOwnerResult
	RecoveryProviderCallLimit
	RecoveryProviderSnapshotAfter
	RecoveryProviderMembersChanged
	RecoveryProviderTargetAfter
	RecoveryProviderContext
)

func (r RecoveryProviderReason) String() string {
	switch r {
	case RecoveryProviderInvalidInput:
		return "invalid_input"
	case RecoveryProviderResultBudget:
		return "result_budget"
	case RecoveryProviderAdmission:
		return "admission"
	case RecoveryProviderTargetBefore:
		return "target_before"
	case RecoveryProviderSnapshotBefore:
		return "snapshot_before"
	case RecoveryProviderOwnerRoster:
		return "owner_roster"
	case RecoveryProviderOwnerUnavailable:
		return "owner_unavailable"
	case RecoveryProviderOwnerPage:
		return "owner_page"
	case RecoveryProviderOwnerResult:
		return "owner_result"
	case RecoveryProviderCallLimit:
		return "call_limit"
	case RecoveryProviderSnapshotAfter:
		return "snapshot_after"
	case RecoveryProviderMembersChanged:
		return "members_changed"
	case RecoveryProviderTargetAfter:
		return "target_after"
	case RecoveryProviderContext:
		return "context"
	default:
		return ""
	}
}

// RecoveryProviderStage indexes fixed stage durations; a missing mask bit is unknown.
type RecoveryProviderStage uint8

const (
	RecoveryProviderReserveStage RecoveryProviderStage = iota
	RecoveryProviderAdmissionStage
	RecoveryProviderTargetBeforeStage
	RecoveryProviderSnapshotBeforeStage
	RecoveryProviderOwnerReadStage
	RecoveryProviderSnapshotAfterStage
	RecoveryProviderTargetAfterStage
	RecoveryProviderStageCount
)

func (s RecoveryProviderStage) Mask() uint16 {
	if s >= RecoveryProviderStageCount {
		return 0
	}
	return 1 << s
}

const (
	RecoveryProviderObservedOwnerBudget   uint16 = 1 << RecoveryProviderStageCount
	RecoveryProviderObservedOwnerElapsed  uint16 = 1 << (RecoveryProviderStageCount + 1)
	RecoveryProviderObservedPageNext      uint16 = 1 << (RecoveryProviderStageCount + 2)
	RecoveryProviderObservedOwnerError    uint16 = 1 << (RecoveryProviderStageCount + 3)
	RecoveryProviderObservedOwnerDispatch uint16 = 1 << (RecoveryProviderStageCount + 4)
	recoveryProviderObservedMask          uint16 = (1 << RecoveryProviderStageCount) - 1 | RecoveryProviderObservedOwnerBudget | RecoveryProviderObservedOwnerElapsed | RecoveryProviderObservedPageNext | RecoveryProviderObservedOwnerError | RecoveryProviderObservedOwnerDispatch
)

// RecoveryProviderOwnerMode distinguishes local projection from wire operations.
type RecoveryProviderOwnerMode uint8

const (
	RecoveryProviderOwnerNone RecoveryProviderOwnerMode = iota
	RecoveryProviderOwnerLocal
	RecoveryProviderOwnerRemoteCalls
	RecoveryProviderOwnerRemoteSingle
	RecoveryProviderOwnerRemoteTargets
)

func (m RecoveryProviderOwnerMode) String() string {
	switch m {
	case RecoveryProviderOwnerLocal:
		return "local"
	case RecoveryProviderOwnerRemoteCalls:
		return "remote_calls"
	case RecoveryProviderOwnerRemoteSingle:
		return "remote_single"
	case RecoveryProviderOwnerRemoteTargets:
		return "remote_targets"
	default:
		return ""
	}
}

// RecoveryProviderOwnerError retains only a closed kind before cause folding.
type RecoveryProviderOwnerError uint8

const (
	RecoveryProviderOwnerNoError RecoveryProviderOwnerError = iota
	RecoveryProviderOwnerTimeout
	RecoveryProviderOwnerCanceled
	RecoveryProviderOwnerBusy
	RecoveryProviderOwnerDial
	RecoveryProviderOwnerMissing
	RecoveryProviderOwnerOther
)

func (e RecoveryProviderOwnerError) String() string {
	switch e {
	case RecoveryProviderOwnerNoError:
		return "none"
	case RecoveryProviderOwnerTimeout:
		return "timeout"
	case RecoveryProviderOwnerCanceled:
		return "canceled"
	case RecoveryProviderOwnerBusy:
		return "busy"
	case RecoveryProviderOwnerDial:
		return "dial"
	case RecoveryProviderOwnerMissing:
		return "node_missing"
	case RecoveryProviderOwnerOther:
		return "error"
	default:
		return ""
	}
}

// RecoveryProviderFailureEvidence freezes one physical-round failure observation.
// FailureElapsed ends at capture, not at eventual round return. Calls and owner
// progress describe that shared round; they are never a fabricated caller trace.
// Remote owner scope includes encoding, RPC, decoding and merge; these are not
// separate transport or receiver spans.
type RecoveryProviderFailureEvidence struct {
	Reason RecoveryProviderReason
	// Observed distinguishes an executed zero-duration stage from missing data.
	Observed uint16
	Stages   [RecoveryProviderStageCount]time.Duration
	// FailureElapsed uses one provider-entry monotonic origin and ends at capture.
	FailureElapsed time.Duration
	// ContextOverride marks later physical-execution cancellation selecting the
	// returned error. All provider measurements still end at the original capture.
	ContextOverride bool
	// Calls is the provider input count, including calls rejected before owner reads.
	Calls uint64
	// Owner counts are the plan and completed scans before capture; a completed
	// scan may still contain failed individual groups and is not all-UID proof.
	// Skipped unavailable members do not increment completed scans; subtracting
	// these counters does not measure pending work or queue occupancy.
	OwnersPlanned, OwnersCompleted uint64
	// OwnerNodeID selects the latest owner scope. Dispatch/OwnerMode is known
	// only after the current page's port entry. A new page clears this knowledge
	// even when an earlier page used the port. The latest
	// owner is context, not attribution for a later membership/fence failure.
	OwnerNodeID uint64
	OwnerMode   RecoveryProviderOwnerMode
	OwnerError  RecoveryProviderOwnerError
	// PageNext is present only after the current page's validation succeeded.
	PageStart, PageNext uint64
	// Configured is the owner-scope policy; effective is remaining at this page's
	// actual port entry, not proof of wire dispatch or remote execution.
	// OwnerElapsed spans the entire current owner scope, including all pages,
	// decoding and merge, even when the current page has not entered its port.
	OwnerConfiguredBudget, OwnerEffectiveBudget, OwnerElapsed time.Duration
	// OwnerRPCTrace is terminal metadata for this exact remote owner page only.
	// Shared physical-round evidence can be referenced by multiple logical calls.
	OwnerRPCTrace rpctrace.TerminalIdentity
}

func (e RecoveryProviderFailureEvidence) valid() bool {
	if !e.OwnerRPCTrace.Valid() {
		return false
	}
	if e.OwnerRPCTrace.State != rpctrace.TerminalTraceUnknown && (e.Observed&RecoveryProviderObservedOwnerDispatch == 0 || e.OwnerMode == RecoveryProviderOwnerLocal || e.OwnerMode == RecoveryProviderOwnerNone) {
		return false
	}
	if e.Reason.String() == "" || e.Observed & ^recoveryProviderObservedMask != 0 || e.FailureElapsed < 0 || e.OwnersCompleted > e.OwnersPlanned || e.OwnerMode > RecoveryProviderOwnerRemoteTargets || e.OwnerError.String() == "" {
		return false
	}
	for i, duration := range e.Stages {
		if duration < 0 || (e.Observed&(1<<i) == 0 && duration != 0) {
			return false
		}
	}
	if e.OwnerConfiguredBudget < 0 || e.OwnerEffectiveBudget < 0 || e.OwnerElapsed < 0 {
		return false
	}
	if e.Observed&RecoveryProviderObservedOwnerBudget == 0 && e.OwnerConfiguredBudget != 0 {
		return false
	}
	if e.Observed&RecoveryProviderObservedOwnerDispatch == 0 && (e.OwnerEffectiveBudget != 0 || e.OwnerMode != RecoveryProviderOwnerNone) {
		return false
	}
	if e.Observed&RecoveryProviderObservedOwnerElapsed == 0 && e.OwnerElapsed != 0 {
		return false
	}
	if e.Observed&RecoveryProviderObservedOwnerError == 0 && e.OwnerError != RecoveryProviderOwnerNoError {
		return false
	}
	return e.Observed&RecoveryProviderObservedPageNext != 0 || e.PageNext == 0
}

type recoveryProviderFailureError struct {
	err      error
	evidence RecoveryProviderFailureEvidence
}

func (e *recoveryProviderFailureError) Error() string { return e.err.Error() }
func (e *recoveryProviderFailureError) Unwrap() error { return e.err }

// WithRecoveryProviderFailure attaches only validated scalar evidence on errors.
// It retains original text and cause families and allocates nothing on success.
func WithRecoveryProviderFailure(err error, evidence RecoveryProviderFailureEvidence) error {
	if err == nil || !evidence.valid() {
		return err
	}
	if _, exists := RecoveryProviderFailure(err); exists {
		return err
	}
	return &recoveryProviderFailureError{err: err, evidence: evidence}
}

// RecoveryProviderFailure returns an immutable value copy of observed metadata.
func RecoveryProviderFailure(err error) (RecoveryProviderFailureEvidence, bool) {
	if err == nil {
		return RecoveryProviderFailureEvidence{}, false
	}
	var found *recoveryProviderFailureError
	if !errors.As(err, &found) || found == nil {
		return RecoveryProviderFailureEvidence{}, false
	}
	return found.evidence, true
}
