package issueagent

import (
	"errors"
	"fmt"
	"slices"
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
	OperationRecordMerge         Operation = "record_merge"
	OperationRecordBranchDrift   Operation = "record_branch_drift"
	OperationRecordWorkDrift     Operation = "record_work_drift"
	OperationRepairProjection    Operation = "repair_projection"
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

// WorkHeadFacts bind the fresh Agent ref (and PR when present) to the durable
// Work identity before any lease or publication can advance.
type WorkHeadFacts struct {
	PRNumber int64  `json:"pr_number"`
	HeadSHA  string `json:"head_sha"`
	PRState  string `json:"pr_state"`
	Draft    bool   `json:"draft"`
	BaseRef  string `json:"base_ref"`
	HeadRef  string `json:"head_ref"`
}

// MergeFacts are a fresh exact PR projection used only to recover a missed
// pull_request.closed wake-up.
type MergeFacts struct {
	PRNumber int64
	HeadSHA  string
	Merged   bool
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
	WorkHead            *WorkHeadFacts
	WorkObjectMissing   bool
	Merge               *MergeFacts
	IssueLabels         []string
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
	ExternalHeadSHA             string                   `json:"external_head_sha"`
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
	if input.ChainStatus == ChainMissing {
		return Plan{
			Operation:    OperationIntakeIssue,
			WriteAllowed: true,
			Reason:       "Issue has no Agent checkpoint and is eligible for deterministic intake",
		}, nil
	}
	if policy.RolloutMode == RolloutIntake {
		return Plan{
			Operation: OperationWait,
			Reason:    "intake mode admits only missing-chain intake and authorization",
		}, nil
	}
	if input.ChainStatus == ChainInvalid {
		return Plan{
			Operation: OperationAlertAuditFailure,
			Reason:    "signed checkpoint chain is invalid; automatic writes are fenced",
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
	var matchingArtifact *WorkerArtifact
	if input.Lease != nil {
		if input.Lease.Generation != input.Checkpoint.Generation ||
			!scheduleDigestPattern.MatchString(input.Lease.OperationID) ||
			!scheduleDigestPattern.MatchString(input.Lease.TaskDigest) ||
			input.Lease.ExpiresAt.IsZero() {
			return Plan{}, errors.New("current lease facts are invalid")
		}
		plan.OperationID = input.Lease.OperationID
		for index := range input.Artifacts {
			artifact := &input.Artifacts[index]
			if artifact.RunID <= 0 {
				return Plan{}, errors.New("Worker Artifact has invalid run identity")
			}
			if artifact.OperationID == input.Lease.OperationID &&
				artifact.TaskDigest == input.Lease.TaskDigest &&
				artifact.Generation == input.Lease.Generation {
				if matchingArtifact != nil {
					return Plan{}, errors.New("multiple Artifacts match the current lease")
				}
				matchingArtifact = artifact
			}
		}
	}
	if issueagentcontract.IsActiveWorkState(input.Checkpoint.State) &&
		input.Checkpoint.Work != nil {
		if input.WorkObjectMissing {
			plan.Operation = OperationRecordWorkDrift
			plan.WriteAllowed = true
			plan.Reason = "signed Agent work references a missing GitHub object"
			return plan, nil
		}
		work := input.Checkpoint.Work
		if input.WorkHead == nil ||
			input.WorkHead.PRNumber != work.PRNumber ||
			!fullCommitPattern.MatchString(input.WorkHead.HeadSHA) {
			return Plan{}, errors.New("current Agent branch facts are stale")
		}
		mergedReview := work.PRNumber > 0 &&
			input.Checkpoint.State == issueagentcontract.StateReadyForReview &&
			input.WorkHead.PRState == "closed" &&
			input.Merge != nil && input.Merge.Merged
		if work.PRNumber > 0 &&
			(input.WorkHead.BaseRef != "main" ||
				input.WorkHead.HeadRef != work.Branch ||
				input.WorkHead.PRState != "open" && !mergedReview) {
			plan.Operation = OperationRecordWorkDrift
			plan.WriteAllowed = true
			plan.Reason = "Agent pull request target or state differs from signed work"
			return plan, nil
		}
		if input.WorkHead.HeadSHA != work.HeadSHA {
			if input.Checkpoint.State == issueagentcontract.StateValidating {
				plan.Operation = OperationRequestValidation
				plan.WriteAllowed = true
				plan.Reason = "validation Publisher must classify a pending signed rebase or external head"
				return plan, nil
			}
			if matchingArtifact != nil && input.Lease != nil &&
				input.Lease.ExpiresAt.After(input.Now) {
				plan.Operation = OperationPublishWorkerResult
				plan.ArtifactRunID = matchingArtifact.RunID
				plan.WriteAllowed = true
				plan.Reason = "Artifact Publisher must recover an exact pending commit or record external drift"
				return plan, nil
			}
			plan.Operation = OperationRecordBranchDrift
			plan.ExternalHeadSHA = input.WorkHead.HeadSHA
			plan.WriteAllowed = true
			plan.Reason = "fresh GitHub facts report an external Agent branch update"
			return plan, nil
		}
		if work.PRNumber > 0 {
			expectedDraft := input.Checkpoint.State !=
				issueagentcontract.StateReadyForReview
			if input.WorkHead.PRState == "open" &&
				input.WorkHead.Draft != expectedDraft {
				plan.Operation = OperationRepairProjection
				plan.WriteAllowed = true
				plan.Reason = "Agent pull request Draft projection is incomplete"
				return plan, nil
			}
		}
	}
	currentLabels := append([]string(nil), input.IssueLabels...)
	slices.Sort(currentLabels)
	expectedLabels := ProjectLifecycleLabels(
		input.Checkpoint.State, currentLabels,
	)
	if !slices.Equal(expectedLabels, currentLabels) {
		plan.Operation = OperationRepairProjection
		plan.WriteAllowed = true
		plan.Reason = "durable checkpoint label projection is incomplete"
		return plan, nil
	}
	if input.Lease != nil {
		if !input.Lease.ExpiresAt.After(input.Now) {
			plan.Operation = OperationExpireLease
			plan.WriteAllowed = true
			plan.Reason = "current Worker lease expired"
			return plan, nil
		}
		if matchingArtifact == nil {
			plan.Operation = OperationWait
			plan.Reason = "current Worker lease has no publishable Artifact"
			return plan, nil
		}
		plan.Operation = OperationPublishWorkerResult
		plan.ArtifactRunID = matchingArtifact.RunID
		plan.WriteAllowed = true
		plan.Reason = "current unexpired lease has one exact Artifact"
		return plan, nil
	}
	if input.Checkpoint.State == issueagentcontract.StateReadyForReview &&
		input.Checkpoint.Work != nil && input.Merge != nil {
		if input.Merge.PRNumber != input.Checkpoint.Work.PRNumber ||
			!fullCommitPattern.MatchString(input.Merge.HeadSHA) ||
			input.WorkHead == nil ||
			input.Merge.HeadSHA != input.WorkHead.HeadSHA {
			return Plan{}, errors.New("current pull request merge facts are stale")
		}
		if input.Merge.Merged {
			plan.Operation = OperationRecordMerge
			plan.WriteAllowed = true
			plan.Reason = "fresh GitHub facts report the exact validated PR as merged"
			return plan, nil
		}
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
