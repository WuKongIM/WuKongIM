package issueagent

import (
	"errors"
	"fmt"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ChainStatus is the verifier's conclusion about signed Issue state.
type ChainStatus string

const (
	ChainMissing ChainStatus = "missing"
	ChainValid   ChainStatus = "valid"
	ChainInvalid ChainStatus = "invalid"
)

// Operation is one typed side effect proposal for the trusted Publisher.
type Operation string

const (
	OperationWait                Operation = "wait"
	OperationReportOnly          Operation = "report_only"
	OperationAlertAuditFailure   Operation = "alert_audit_failure"
	OperationIntakeIssue         Operation = "intake_issue"
	OperationResolveVersions     Operation = "resolve_versions"
	OperationDispatchWorker      Operation = "dispatch_worker"
	OperationPublishWorkerResult Operation = "publish_worker_result"
	OperationExpireLease         Operation = "expire_lease"
	OperationCreateDraftPR       Operation = "create_draft_pr"
	OperationRequestValidation   Operation = "request_validation"
)

// LeaseFacts are the exact signed lease fields needed by reconciliation.
type LeaseFacts struct {
	OperationID string
	TaskDigest  string
	Generation  uint64
	ExpiresAt   time.Time
}

// WorkerArtifact is bounded current GitHub Actions Artifact identity.
type WorkerArtifact struct {
	RunID       int64
	OperationID string
	TaskDigest  string
	Generation  uint64
}

// ReconcileInput is one current GitHub snapshot; event payload is deliberately absent.
type ReconcileInput struct {
	Now                 time.Time
	ChainStatus         ChainStatus
	Checkpoint          *issueagentcontract.Checkpoint
	CheckpointCommentID int64
	CheckpointDigest    string
	Lease               *LeaseFacts
	Artifacts           []WorkerArtifact
}

// ReconcilePolicy is the currently enabled capability ceiling.
type ReconcilePolicy struct {
	Enabled     bool
	RolloutMode RolloutMode
}

// Plan is an immutable proposal bound to the exact checkpoint predecessor.
type Plan struct {
	Operation                   Operation                `json:"operation"`
	Repository                  string                   `json:"repository"`
	IssueNumber                 int64                    `json:"issue_number"`
	Generation                  uint64                   `json:"generation"`
	ExpectedSequence            uint64                   `json:"expected_sequence"`
	ExpectedCheckpointCommentID int64                    `json:"expected_checkpoint_comment_id"`
	ExpectedCheckpointDigest    string                   `json:"expected_checkpoint_digest"`
	OperationID                 string                   `json:"operation_id"`
	ArtifactRunID               int64                    `json:"artifact_run_id"`
	Phase                       issueagentcontract.Phase `json:"phase"`
	WriteAllowed                bool                     `json:"write_allowed"`
	Reason                      string                   `json:"reason"`
}

// Reconcile derives one next operation solely from current verified facts.
func Reconcile(input ReconcileInput, policy ReconcilePolicy) (Plan, error) {
	if input.Now.IsZero() || !validRolloutMode(policy.RolloutMode) {
		return Plan{}, errors.New("reconcile input or rollout policy is invalid")
	}
	if !policy.Enabled || policy.RolloutMode == RolloutDisabled {
		return Plan{Operation: OperationWait, Reason: "Issue Agent is disabled"}, nil
	}
	if policy.RolloutMode == RolloutShadow {
		return Plan{
			Operation: OperationReportOnly,
			Reason:    "shadow mode records no GitHub write",
		}, nil
	}
	if input.ChainStatus == ChainInvalid {
		return Plan{
			Operation: OperationAlertAuditFailure,
			Reason:    "signed checkpoint chain is invalid; automatic writes are fenced",
		}, nil
	}
	if input.ChainStatus == ChainMissing {
		return Plan{
			Operation:    OperationIntakeIssue,
			WriteAllowed: true,
			Reason:       "Issue has no Agent checkpoint and is eligible for deterministic intake",
		}, nil
	}
	if input.ChainStatus != ChainValid || input.Checkpoint == nil {
		return Plan{}, errors.New("valid chain status requires a checkpoint")
	}
	if input.CheckpointCommentID <= 0 ||
		!scheduleDigestPattern.MatchString(input.CheckpointDigest) {
		return Plan{}, errors.New("verified checkpoint reference is invalid")
	}

	plan := Plan{
		Repository:                  input.Checkpoint.Repository,
		IssueNumber:                 input.Checkpoint.IssueNumber,
		Generation:                  input.Checkpoint.Generation,
		ExpectedSequence:            input.Checkpoint.Sequence,
		ExpectedCheckpointCommentID: input.CheckpointCommentID,
		ExpectedCheckpointDigest:    input.CheckpointDigest,
	}
	if input.Lease != nil {
		if input.Lease.Generation != input.Checkpoint.Generation ||
			!scheduleDigestPattern.MatchString(input.Lease.OperationID) ||
			!scheduleDigestPattern.MatchString(input.Lease.TaskDigest) ||
			input.Lease.ExpiresAt.IsZero() {
			return Plan{}, errors.New("current lease facts are invalid")
		}
		plan.OperationID = input.Lease.OperationID
		if !input.Lease.ExpiresAt.After(input.Now) {
			plan.Operation = OperationExpireLease
			plan.WriteAllowed = true
			plan.Reason = "current Worker lease expired"
			return plan, nil
		}
		var match *WorkerArtifact
		for index := range input.Artifacts {
			artifact := &input.Artifacts[index]
			if artifact.RunID <= 0 {
				return Plan{}, errors.New("Worker Artifact has invalid run identity")
			}
			if artifact.OperationID == input.Lease.OperationID &&
				artifact.TaskDigest == input.Lease.TaskDigest &&
				artifact.Generation == input.Lease.Generation {
				if match != nil {
					return Plan{}, errors.New("multiple Artifacts match the current lease")
				}
				match = artifact
			}
		}
		if match == nil {
			plan.Operation = OperationWait
			plan.Reason = "current Worker lease has no publishable Artifact"
			return plan, nil
		}
		plan.Operation = OperationPublishWorkerResult
		plan.ArtifactRunID = match.RunID
		plan.WriteAllowed = true
		plan.Reason = "current unexpired lease has one exact Artifact"
		return plan, nil
	}

	operation, phase, allowed := nextStateOperation(
		input.Checkpoint.State,
		policy.RolloutMode,
	)
	plan.Operation = operation
	plan.Phase = phase
	plan.WriteAllowed = allowed
	if operation == OperationWait {
		plan.Reason = "rollout mode or lifecycle state admits no automatic work"
	} else {
		plan.Reason = fmt.Sprintf("checkpoint state %s selects %s", input.Checkpoint.State, operation)
	}
	return plan, nil
}

func nextStateOperation(
	state issueagentcontract.State,
	mode RolloutMode,
) (Operation, issueagentcontract.Phase, bool) {
	if mode == RolloutIntake {
		return OperationWait, "", false
	}
	switch state {
	case issueagentcontract.StateAuthorized:
		return OperationResolveVersions, "", true
	case issueagentcontract.StateVersionPinned, issueagentcontract.StateReproducing:
		return OperationDispatchWorker, issueagentcontract.PhaseReproduce, true
	case issueagentcontract.StateReproduced:
		return OperationCreateDraftPR, "", true
	}
	if mode == RolloutReproduction {
		return OperationWait, "", false
	}
	switch state {
	case issueagentcontract.StateDraftPROpen, issueagentcontract.StateDiagnosing:
		return OperationDispatchWorker, issueagentcontract.PhaseDiagnose, true
	case issueagentcontract.StateDiagnosed, issueagentcontract.StateFixing:
		return OperationDispatchWorker, issueagentcontract.PhaseFix, true
	case issueagentcontract.StateValidating:
		return OperationRequestValidation, "", true
	default:
		return OperationWait, "", false
	}
}
