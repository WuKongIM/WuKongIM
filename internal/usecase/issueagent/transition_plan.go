package issueagent

import (
	"errors"
	"slices"
	"strconv"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// CIRepairDisposition is the usecase-owned outcome of a failed validation.
type CIRepairDisposition struct {
	Repair  bool
	Summary string
}

// PlanCIRepairDisposition applies rollout and complete per-Issue budgets.
func PlanCIRepairDisposition(
	previous issueagentcontract.Checkpoint,
	policy Policy,
) CIRepairDisposition {
	switch {
	case int(previous.Budget.CIRepairAttempts) >= policy.IssueBudget.MaxCIRepairAttempts:
		return CIRepairDisposition{Summary: "Validation failed after the configured bounded CI repair attempts; human review is required."}
	case int(previous.Budget.RemediationAttempts) >= policy.IssueBudget.MaxRemediationAttempts ||
		previous.Budget.WorkerSeconds+uint64((95*time.Minute).Seconds()) >
			uint64(policy.IssueBudget.MaxWorkerTime.Seconds()):
		return CIRepairDisposition{Summary: "Validation failed after the per-Issue remediation budget was exhausted; human review is required."}
	case !policy.Enabled ||
		policy.RolloutMode != RolloutGeneral &&
			!(policy.RolloutMode == RolloutRemediation &&
				slices.Contains(policy.RemediationIssueAllowlist, previous.IssueNumber)):
		return CIRepairDisposition{Summary: "Validation failed while automated remediation was outside the current rollout policy; human review is required."}
	default:
		return CIRepairDisposition{
			Repair:  true,
			Summary: "Recorded an exact failed validation generation and returned it to bounded remediation.",
		}
	}
}

// PlanValidationFailureTransition returns the complete human or repair
// successor. A repair disposition requires the already validated exact task.
func PlanValidationFailureTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	validation issueagentcontract.Validation,
	disposition CIRepairDisposition,
	task *issueagentcontract.TaskEnvelope,
	now time.Time,
) (PlannedTransition, error) {
	next, err := successorBase(previous, anchor)
	if err != nil || now.IsZero() {
		return PlannedTransition{}, errors.New("validation-failure transition input is invalid")
	}
	next.Lease = nil
	next.Validation = &validation
	plan := PlannedTransition{Checkpoint: next, Summary: disposition.Summary}
	if !disposition.Repair {
		if task != nil {
			return PlannedTransition{}, errors.New("human validation failure cannot dispatch a task")
		}
		plan.Checkpoint.State = issueagentcontract.StateReadyForHuman
		plan.Checkpoint.NextAction = issueagentcontract.ActionWaitForHuman
		plan.RequireReadyHumanLabel = true
		return validatePlannedTransition(previous, plan)
	}
	if task == nil || task.Phase != issueagentcontract.PhaseFix ||
		task.Sequence != next.Sequence || task.Generation != next.Generation {
		return PlannedTransition{}, errors.New("CI repair task is not transition-bound")
	}
	taskDigest, err := issueagentcontract.TaskDigest(*task)
	if err != nil {
		return PlannedTransition{}, err
	}
	plan.Checkpoint.State = issueagentcontract.StateFixing
	plan.Checkpoint.Lease = &issueagentcontract.Lease{
		OperationID: task.OperationID, Workflow: "issue-agent-run.yml",
		DispatchRequestID: task.OperationID, Phase: issueagentcontract.PhaseFix,
		IssuedAt: now, ExpiresAt: now.Add(95 * time.Minute),
		TaskSHA256: taskDigest, Task: *task,
		ReservedSeconds: uint64((95 * time.Minute).Seconds()), Heavy: true,
	}
	plan.Checkpoint.Budget.CIRepairAttempts++
	plan.Checkpoint.Budget.RemediationAttempts++
	plan.Checkpoint.NextAction = issueagentcontract.ActionImplementFix
	return validatePlannedTransition(previous, plan)
}

// FinalizeCommandTransition turns verified command effects into one complete
// successor. GitHub facts and side-effect results are inputs, never decisions.
func FinalizeCommandTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	intent CommandIntent,
	commandPlan CommandPlan,
	eventID string,
	actor string,
	commentID int64,
	task *issueagentcontract.TaskEnvelope,
	childIssueNumber int64,
	now time.Time,
) (PlannedTransition, error) {
	next, err := successorBase(previous, anchor)
	if err != nil || commandPlan.Kind != intent.Kind ||
		commandPlan.NewGeneration != previous.Generation+1 ||
		eventID == "" || actor == "" || commentID <= 0 {
		return PlannedTransition{}, errors.New("command transition input is invalid")
	}
	next.Generation = commandPlan.NewGeneration
	next.Lease = nil
	control := &issueagentcontract.ControlAudit{
		Kind: string(intent.Kind), EventID: eventID, Actor: actor,
		CommentID: commentID,
	}
	next.Control = control
	transition := PlannedTransition{
		Checkpoint: next,
		Summary: "Applied freshly authorized maintainer command /agent " +
			string(intent.Kind) + ".",
	}
	switch intent.Kind {
	case CommandRevise:
		if commandPlan.RevisedCheckpoint == nil {
			return PlannedTransition{}, errors.New("revise transition is incomplete")
		}
		transition.Checkpoint = *commandPlan.RevisedCheckpoint
		transition.Checkpoint.Control = control
	case CommandCancel:
		transition.Checkpoint.State = issueagentcontract.StateCancelled
		transition.Checkpoint.NextAction = issueagentcontract.ActionNone
	case CommandAdoptHead:
		if previous.Work == nil {
			return PlannedTransition{}, errors.New("adopt-head transition lacks work")
		}
		control.AdoptedHeadSHA = commandPlan.AdoptedHeadSHA
		transition.Checkpoint.Work = &issueagentcontract.Work{
			Branch: previous.Work.Branch, HeadSHA: commandPlan.AdoptedHeadSHA,
			PRNumber:                 previous.Work.PRNumber,
			MechanicalRebaseAttempts: previous.Work.MechanicalRebaseAttempts,
		}
		transition.Checkpoint.Validation = nil
		switch {
		case previous.Work.PRNumber == 0:
			transition.Checkpoint.State = issueagentcontract.StateReproduced
			transition.Checkpoint.NextAction =
				issueagentcontract.ActionOpenDraftPR
		case previous.Diagnosis == nil:
			transition.Checkpoint.State = issueagentcontract.StateDraftPROpen
			transition.Checkpoint.NextAction = issueagentcontract.ActionDiagnose
		default:
			transition.Checkpoint.State = issueagentcontract.StateValidating
			transition.Checkpoint.NextAction = issueagentcontract.ActionValidate
		}
	case CommandAddressReview:
		if task == nil || now.IsZero() ||
			task.Phase != issueagentcontract.PhaseAddressReview ||
			task.Sequence != transition.Checkpoint.Sequence ||
			task.Generation != transition.Checkpoint.Generation {
			return PlannedTransition{}, errors.New("address-review task is not command-bound")
		}
		taskDigest, digestErr := issueagentcontract.TaskDigest(*task)
		if digestErr != nil {
			return PlannedTransition{}, digestErr
		}
		control.ReviewThreadIDs = append(
			[]string(nil), commandPlan.ReviewThreadIDs...,
		)
		transition.Checkpoint.State = issueagentcontract.StateFixing
		transition.Checkpoint.Validation = nil
		transition.Checkpoint.Lease = &issueagentcontract.Lease{
			OperationID: task.OperationID, Workflow: "issue-agent-run.yml",
			DispatchRequestID: task.OperationID,
			Phase:             issueagentcontract.PhaseAddressReview,
			IssuedAt:          now, ExpiresAt: now.Add(95 * time.Minute),
			TaskSHA256: taskDigest, Task: *task,
			ReservedSeconds: uint64((95 * time.Minute).Seconds()), Heavy: true,
		}
		transition.Checkpoint.Budget.RemediationAttempts++
		transition.Checkpoint.NextAction = issueagentcontract.ActionImplementFix
	case CommandBackport:
		if commandPlan.Backport == nil || childIssueNumber <= 0 {
			return PlannedTransition{}, errors.New("backport transition lacks child Issue")
		}
		control.BackportBranch = commandPlan.Backport.TargetBranch
		control.ChildIssueNumber = childIssueNumber
		transition.Checkpoint.State = issueagentcontract.StateMerged
		transition.Checkpoint.NextAction = issueagentcontract.ActionNone
		transition.Summary += " Created independent backport Issue."
	default:
		return PlannedTransition{}, errors.New("unsupported finalized command")
	}
	return validatePlannedTransition(previous, transition)
}

// TransitionAnchor is the verified predecessor identity for one signed
// successor.
type TransitionAnchor struct {
	CommentID int64
	Digest    string
}

// PlannedTransition is a complete checkpoint and its human-facing projection
// metadata. The composition root must not reinterpret its state or action.
type PlannedTransition struct {
	Checkpoint             issueagentcontract.Checkpoint
	Summary                string
	RequireReadyHumanLabel bool
}

// WorkerAttemptFacts are trusted, provider-neutral usage facts extracted from
// one lease-bound Worker Artifact. Accounting is applied by the state machine.
type WorkerAttemptFacts struct {
	Provider            issueagentcontract.Provider
	Model               string
	InputTokens         uint64
	OutputTokens        uint64
	ElapsedMilliseconds uint64
	TerminalResult      string
}

// PlanChainRecoveryTransition moves an exact verified recovery anchor to its
// last durable boundary and records the complete administrator audit.
func PlanChainRecoveryTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	control issueagentcontract.ControlAudit,
) (PlannedTransition, error) {
	if control.Kind != string(CommandRecoverChain) ||
		control.EventID == "" || control.Actor == "" || control.CommentID <= 0 ||
		control.RecoveryAnchorCommentID != anchor.CommentID ||
		control.RecoveryAnchorDigest != anchor.Digest ||
		len(control.QuarantinedCommentIDs) == 0 ||
		!scheduleDigestPattern.MatchString(control.QuarantineDigest) {
		return PlannedTransition{}, errors.New("chain-recovery transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.Generation++
	next.Lease = nil
	next.Model = nil
	control.QuarantinedCommentIDs = append(
		[]int64(nil), control.QuarantinedCommentIDs...,
	)
	next.Control = &control
	switch previous.State {
	case issueagentcontract.StateReproducing:
		next.State = issueagentcontract.StateVersionPinned
		next.NextAction = issueagentcontract.ActionReproduce
	case issueagentcontract.StateDiagnosing:
		next.State = issueagentcontract.StateDraftPROpen
		next.NextAction = issueagentcontract.ActionDiagnose
	case issueagentcontract.StateFixing:
		next.State = issueagentcontract.StateDiagnosed
		next.NextAction = issueagentcontract.ActionImplementFix
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Admin recovered the signed chain from an exact anchor and " +
			"quarantined later invalid App checkpoints.",
	})
}

// PlanMergeObservedTransition records an exact human merge as terminal state.
func PlanMergeObservedTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateReadyForReview ||
		previous.Work == nil || previous.Validation == nil {
		return PlannedTransition{}, errors.New("merge transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateMerged
	next.Lease = nil
	next.NextAction = issueagentcontract.ActionNone
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Observed the exact human-merged Agent PR and recorded " +
			"terminal merged state.",
	})
}

// PlanVersionPinnedTransition binds immutable resolved versions to the
// authorized generation.
func PlanVersionPinnedTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	versions issueagentcontract.Versions,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateAuthorized ||
		previous.NextAction != issueagentcontract.ActionPinVersions {
		return PlannedTransition{}, errors.New("version-pin transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateVersionPinned
	next.Versions = versions
	next.NextAction = issueagentcontract.ActionReproduce
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Pinned the reported version and authorization-time diagnosis " +
			"baseline to immutable commits.",
	})
}

// PlanWorkerLeaseTransition creates a complete phase-specific signed lease and
// owns attempt accounting.
func PlanWorkerLeaseTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	task issueagentcontract.TaskEnvelope,
	now time.Time,
) (PlannedTransition, error) {
	reservation, err := WorkerReservationForPhase(task.Phase)
	if now.IsZero() || err != nil ||
		task.Generation != previous.Generation ||
		task.Sequence != previous.Sequence+1 {
		return PlannedTransition{}, errors.New("Worker lease transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	taskDigest, err := issueagentcontract.TaskDigest(task)
	if err != nil {
		return PlannedTransition{}, err
	}
	switch task.Phase {
	case issueagentcontract.PhaseReproduce:
		if previous.State != issueagentcontract.StateVersionPinned ||
			previous.NextAction != issueagentcontract.ActionReproduce {
			return PlannedTransition{}, errors.New("reproduction lease boundary is invalid")
		}
		next.State = issueagentcontract.StateReproducing
		next.NextAction = issueagentcontract.ActionReproduce
		next.Budget.ReproductionAttempts++
	case issueagentcontract.PhaseDiagnose:
		if previous.State != issueagentcontract.StateDraftPROpen ||
			previous.NextAction != issueagentcontract.ActionDiagnose {
			return PlannedTransition{}, errors.New("diagnosis lease boundary is invalid")
		}
		next.State = issueagentcontract.StateDiagnosing
		next.NextAction = issueagentcontract.ActionDiagnose
	case issueagentcontract.PhaseFix:
		if previous.State != issueagentcontract.StateDiagnosed ||
			previous.NextAction != issueagentcontract.ActionImplementFix ||
			previous.Diagnosis == nil || previous.Reproduction == nil {
			return PlannedTransition{}, errors.New("fix lease boundary is invalid")
		}
		next.State = issueagentcontract.StateFixing
		next.NextAction = issueagentcontract.ActionImplementFix
		next.Budget.RemediationAttempts++
	default:
		return PlannedTransition{}, errors.New("unsupported Worker lease phase")
	}
	next.Lease = &issueagentcontract.Lease{
		OperationID: task.OperationID, Workflow: "issue-agent-run.yml",
		DispatchRequestID: task.OperationID, Phase: task.Phase,
		IssuedAt: now, ExpiresAt: now.Add(reservation.Duration),
		TaskSHA256: taskDigest, Task: task,
		ReservedSeconds: uint64(reservation.Duration / time.Second),
		Heavy:           reservation.Heavy,
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Leased one bounded, credential-free " +
			string(task.Phase) + " Worker.",
	})
}

// PlanReproductionResultTransition binds a validated reproduction evaluation
// and, when confirmed, the exact commit produced by the Publisher.
func PlanReproductionResultTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	evaluation ReproductionEvaluation,
	work *issueagentcontract.Work,
	attempt WorkerAttemptFacts,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateReproducing ||
		previous.Lease == nil ||
		previous.Lease.Phase != issueagentcontract.PhaseReproduce ||
		evaluation.Evidence == nil {
		return PlannedTransition{}, errors.New("reproduction result boundary is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.Lease = nil
	next.Reproduction = evaluation.Evidence
	if err := applyWorkerAttempt(&next, attempt); err != nil {
		return PlannedTransition{}, err
	}
	switch evaluation.Decision {
	case ReproductionConfirmed:
		if work == nil || work.PRNumber != 0 {
			return PlannedTransition{}, errors.New("confirmed reproduction lacks exact work")
		}
		next.State = issueagentcontract.StateReproduced
		next.Work = cloneWork(work)
		next.NextAction = issueagentcontract.ActionOpenDraftPR
	case ReproductionAlreadyFixed:
		if work != nil {
			return PlannedTransition{}, errors.New("already-fixed reproduction cannot bind work")
		}
		next.State = issueagentcontract.StateAlreadyFixed
		next.Work = nil
		next.NextAction = issueagentcontract.ActionNone
	default:
		return PlannedTransition{}, errors.New("reproduction result is not publishable")
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Published an exact two-baseline, three-run E2E " +
			"reproduction decision.",
	})
}

// PlanWorkerFailureTransition records a classified Worker/provider failure and
// returns the Issue to humans without consuming infrastructure retry budget.
func PlanWorkerFailureTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	attempt WorkerAttemptFacts,
) (PlannedTransition, error) {
	if previous.Lease == nil {
		return PlannedTransition{}, errors.New("Worker failure lacks a lease")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateReadyForHuman
	next.Lease = nil
	next.NextAction = issueagentcontract.ActionWaitForHuman
	if err := applyWorkerAttempt(&next, attempt); err != nil {
		return PlannedTransition{}, err
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Recorded a classified " + attempt.TerminalResult +
			" Worker failure without treating it as an infrastructure lease expiry.",
		RequireReadyHumanLabel: true,
	})
}

// PlanWorkerPublicationCollisionTransition records a valid Worker result that
// could not be published because the deterministic Agent ref is occupied by
// an untrusted commit. No external head is adopted.
func PlanWorkerPublicationCollisionTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	attempt WorkerAttemptFacts,
) (PlannedTransition, error) {
	next, err := successorBase(previous, anchor)
	if err != nil || previous.Lease == nil {
		return PlannedTransition{},
			errors.New("publication collision transition input is invalid")
	}
	next.State = issueagentcontract.StateReadyForHuman
	next.NextAction = issueagentcontract.ActionWaitForHuman
	next.Lease = nil
	if err := applyWorkerAttempt(&next, attempt); err != nil {
		return PlannedTransition{}, err
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "The deterministic Agent branch is occupied by a commit that " +
			"does not match the configured App identity and exact Worker result.",
		RequireReadyHumanLabel: true,
	})
}

// PlanDiagnosisResultTransition binds the mandatory causal diagnosis.
func PlanDiagnosisResultTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	diagnosis issueagentcontract.Diagnosis,
	attempt WorkerAttemptFacts,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateDiagnosing ||
		previous.Lease == nil ||
		previous.Lease.Phase != issueagentcontract.PhaseDiagnose ||
		diagnosis.AuthorizationEvent != "" {
		return PlannedTransition{}, errors.New("diagnosis result boundary is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateDiagnosed
	next.Lease = nil
	next.Diagnosis = &diagnosis
	next.NextAction = issueagentcontract.ActionImplementFix
	if err := applyWorkerAttempt(&next, attempt); err != nil {
		return PlannedTransition{}, err
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Published the mandatory causal diagnosis and deterministic " +
			"risk classification.",
	})
}

// PlanFixResultTransition binds the exact immutable commit produced for a
// successful fix or review-repair Worker result.
func PlanFixResultTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	newHeadSHA string,
	attempt WorkerAttemptFacts,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateFixing ||
		previous.Lease == nil ||
		(previous.Lease.Phase != issueagentcontract.PhaseFix &&
			previous.Lease.Phase != issueagentcontract.PhaseAddressReview) ||
		previous.Work == nil || !fullCommitPattern.MatchString(newHeadSHA) {
		return PlannedTransition{}, errors.New("fix result boundary is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateValidating
	next.Lease = nil
	next.Work = cloneWork(previous.Work)
	next.Work.HeadSHA = newHeadSHA
	next.Validation = nil
	next.NextAction = issueagentcontract.ActionValidate
	if err := applyWorkerAttempt(&next, attempt); err != nil {
		return PlannedTransition{}, err
	}
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Published the bounded fix candidate after exact local build, " +
			"related tests, and three E2E passes.",
	})
}

// PlanDraftPROpenTransition binds the deterministic Draft PR returned by
// GitHub to already published reproduction work.
func PlanDraftPROpenTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	prNumber int64,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateReproduced ||
		previous.NextAction != issueagentcontract.ActionOpenDraftPR ||
		previous.Work == nil || previous.Work.PRNumber != 0 || prNumber <= 0 {
		return PlannedTransition{}, errors.New("Draft-PR transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateDraftPROpen
	next.Work = cloneWork(previous.Work)
	next.Work.PRNumber = prNumber
	next.NextAction = issueagentcontract.ActionDiagnose
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Opened or recovered the deterministic Draft PR for the frozen " +
			"E2E reproduction.",
	})
}

// PlanRiskAuthorizationTransition records a fresh generation-bound approval
// for the exact signed diagnosis scope.
func PlanRiskAuthorizationTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	eventID string,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateDiagnosed ||
		previous.Diagnosis == nil || eventID == "" ||
		previous.Diagnosis.AuthorizationEvent != "" {
		return PlannedTransition{}, errors.New("risk-authorization transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.Generation++
	next.Lease = nil
	next.Model = nil
	diagnosis := *previous.Diagnosis
	diagnosis.AuthorizationEvent = eventID
	next.Diagnosis = &diagnosis
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Recorded a fresh maintainer authorization for the exact " +
			"high-risk diagnosis scope.",
	})
}

// PlanValidationSuccessTransition binds the exact successful validation
// generation after GitHub has converted the Draft PR to Ready.
func PlanValidationSuccessTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	validation issueagentcontract.Validation,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateValidating ||
		previous.Work == nil || validation.Conclusion != "success" ||
		validation.LocalPasses != 3 ||
		validation.HeadSHA != previous.Work.HeadSHA {
		return PlannedTransition{}, errors.New("validation-success transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateReadyForReview
	next.Lease = nil
	next.Validation = &validation
	next.NextAction = issueagentcontract.ActionRequestReview
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Verified the exact Validation Gate generation and converted " +
			"the Draft PR to Ready for human review.",
	})
}

// PlanAlreadyFixedOnMainTransition records exact three-pass moving-main
// evidence after the Publisher closed only the unmerged Draft PR.
func PlanAlreadyFixedOnMainTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	validation issueagentcontract.Validation,
) (PlannedTransition, error) {
	if previous.State != issueagentcontract.StateValidating ||
		previous.Work == nil || validation.Conclusion != "success" ||
		validation.LocalPasses != 3 ||
		validation.HeadSHA != previous.Work.HeadSHA {
		return PlannedTransition{}, errors.New("already-fixed transition input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateAlreadyFixed
	next.Lease = nil
	next.Validation = &validation
	next.NextAction = issueagentcontract.ActionNone
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Current main passed the exact frozen E2E three consecutive " +
			"times; closed only the unmerged Agent Draft PR and left the Issue open.",
	})
}

// PlanWorkerBudgetStopTransition moves any active automated phase to the human
// queue without changing its cumulative accounting.
func PlanWorkerBudgetStopTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
) (PlannedTransition, error) {
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateReadyForHuman
	next.Lease = nil
	next.NextAction = issueagentcontract.ActionWaitForHuman
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint:             next,
		Summary:                "Stopped automatic work at the configured per-Issue Worker budget.",
		RequireReadyHumanLabel: true,
	})
}

// GitHubEffectKind names one bounded side effect that must happen before a
// planned transition can be published.
type GitHubEffectKind string

const (
	GitHubEffectNone       GitHubEffectKind = "none"
	GitHubEffectRebaseMain GitHubEffectKind = "rebase_agent_branch_onto_main"
	GitHubEffectCloseDraft GitHubEffectKind = "close_agent_draft"
)

// GitHubEffect is an exact, expected-head-fenced side effect proposal.
type GitHubEffect struct {
	Kind         GitHubEffectKind
	Branch       string
	ExpectedHead string
	MainSHA      string
	ExpectedTree string
	Message      string
}

// DriftTransitionPlan contains either one immediate transition or one
// mechanical effect with typed success and failure outcomes.
type DriftTransitionPlan struct {
	Effect    GitHubEffect
	Immediate *PlannedTransition
	Success   PlannedTransition
	Failure   PlannedTransition
}

// PlanExpiredLeaseTransition owns retry accounting and the recovery boundary
// selected after a signed Worker lease expires.
func PlanExpiredLeaseTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	maxInfrastructureRetries int,
) (PlannedTransition, error) {
	next, err := successorBase(previous, anchor)
	if err != nil || previous.Lease == nil || maxInfrastructureRetries <= 0 {
		return PlannedTransition{}, errors.New("expired-lease transition input is invalid")
	}
	next.Lease = nil
	next.Model = nil
	plan := PlannedTransition{
		Checkpoint: next,
		Summary:    "Recovered an expired Worker lease without accepting any untrusted output.",
	}
	if int(next.Budget.InfrastructureRetries) >= maxInfrastructureRetries {
		plan.Checkpoint.State = issueagentcontract.StateReadyForHuman
		plan.Checkpoint.NextAction = issueagentcontract.ActionWaitForHuman
		plan.RequireReadyHumanLabel = true
		plan.Summary = "Stopped automatic recovery after the bounded infrastructure retry budget."
		return validatePlannedTransition(previous, plan)
	}
	plan.Checkpoint.Budget.InfrastructureRetries++
	switch previous.State {
	case issueagentcontract.StateReproducing:
		plan.Checkpoint.State = issueagentcontract.StateVersionPinned
		plan.Checkpoint.NextAction = issueagentcontract.ActionReproduce
	case issueagentcontract.StateDiagnosing:
		plan.Checkpoint.State = issueagentcontract.StateDraftPROpen
		plan.Checkpoint.NextAction = issueagentcontract.ActionDiagnose
	case issueagentcontract.StateFixing:
		plan.Checkpoint.State = issueagentcontract.StateDiagnosed
		plan.Checkpoint.NextAction = issueagentcontract.ActionImplementFix
	default:
		return PlannedTransition{}, errors.New("expired lease is outside a recoverable state")
	}
	return validatePlannedTransition(previous, plan)
}

// PlanValidationDriftTransition returns the full bounded effect and both legal
// outcomes for a moving-main conflict.
func PlanValidationDriftTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	facts DriftFacts,
) (DriftTransitionPlan, error) {
	decision, err := PlanDriftRecovery(facts)
	if err != nil || previous.Work == nil ||
		previous.State != issueagentcontract.StateValidating {
		return DriftTransitionPlan{}, errors.New("validation drift transition input is invalid")
	}
	base, err := successorBase(previous, anchor)
	if err != nil {
		return DriftTransitionPlan{}, err
	}
	base.Lease = nil
	base.Validation = nil
	base.Work = cloneWork(previous.Work)
	human := PlannedTransition{
		Checkpoint:             base,
		Summary:                "Moving main produced a semantic conflict; human resolution is required.",
		RequireReadyHumanLabel: true,
	}
	human.Checkpoint.State = issueagentcontract.StateReadyForHuman
	human.Checkpoint.NextAction = issueagentcontract.ActionWaitForHuman
	switch decision {
	case DriftReadyForHuman, DriftAwaitHeadAdoption:
		if decision == DriftAwaitHeadAdoption {
			human.Summary = "The Agent branch has an external head; a fresh " +
				"maintainer /agent adopt-head command is required."
		}
		validated, err := validatePlannedTransition(previous, human)
		if err != nil {
			return DriftTransitionPlan{}, err
		}
		return DriftTransitionPlan{Immediate: &validated}, nil
	case DriftMechanicalRebase:
		human.Checkpoint.Work.MechanicalRebaseAttempts++
		success := PlannedTransition{
			Checkpoint: human.Checkpoint,
			Summary:    "Applied the single allowed expected-head-fenced mechanical main rebase; full validation is required again.",
		}
		success.Checkpoint.State = issueagentcontract.StateValidating
		success.Checkpoint.NextAction = issueagentcontract.ActionValidate
		human.Checkpoint.State = issueagentcontract.StateReadyForHuman
		human.Checkpoint.NextAction = issueagentcontract.ActionWaitForHuman
		return DriftTransitionPlan{
			Effect: GitHubEffect{
				Kind:         GitHubEffectRebaseMain,
				Branch:       previous.Work.Branch,
				ExpectedHead: previous.Work.HeadSHA,
				MainSHA:      facts.CurrentMainSHA,
				ExpectedTree: facts.MechanicalTreeSHA,
				Message:      MechanicalRebaseMessage(previous.IssueNumber),
			},
			Success: success,
			Failure: human,
		}, nil
	default:
		return DriftTransitionPlan{}, errors.New("moving-main conflict decision is not publishable")
	}
}

// PlanExternalBranchUpdateTransition records an unexpected active Agent branch
// head without adopting or overwriting it.
func PlanExternalBranchUpdateTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
	currentHeadSHA string,
) (PlannedTransition, error) {
	if !issueagentcontract.IsActiveWorkState(previous.State) ||
		previous.Work == nil ||
		previous.Work.ExternalHeadSHA != nil ||
		!fullCommitPattern.MatchString(currentHeadSHA) ||
		currentHeadSHA == previous.Work.HeadSHA {
		return PlannedTransition{},
			errors.New("external branch update input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateReadyForHuman
	next.NextAction = issueagentcontract.ActionWaitForHuman
	next.Lease = nil
	next.Validation = nil
	next.Work = cloneWork(previous.Work)
	next.Work.ExternalHeadSHA = &currentHeadSHA
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Recorded external_branch_update at " + currentHeadSHA +
			"; a fresh maintainer /agent adopt-head command is required.",
		RequireReadyHumanLabel: true,
	})
}

// PlanWorkObjectDriftTransition hands a missing, closed, or retargeted active
// Agent work object to humans without adopting a new head or overwriting it.
func PlanWorkObjectDriftTransition(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
) (PlannedTransition, error) {
	if !issueagentcontract.IsActiveWorkState(previous.State) ||
		previous.Work == nil || previous.Work.ExternalHeadSHA != nil {
		return PlannedTransition{},
			errors.New("work object drift input is invalid")
	}
	next, err := successorBase(previous, anchor)
	if err != nil {
		return PlannedTransition{}, err
	}
	next.State = issueagentcontract.StateReadyForHuman
	next.NextAction = issueagentcontract.ActionWaitForHuman
	next.Lease = nil
	next.Validation = nil
	next.Work = cloneWork(previous.Work)
	return validatePlannedTransition(previous, PlannedTransition{
		Checkpoint: next,
		Summary: "Recorded missing_or_changed_work_object; the Agent branch or " +
			"pull request state/target requires maintainer repair.",
		RequireReadyHumanLabel: true,
	})
}

// MechanicalRebaseMessage is part of the exact effect identity.
func MechanicalRebaseMessage(issueNumber int64) string {
	return "chore(agent): rebase issue #" +
		strconv.FormatInt(issueNumber, 10)
}

// BindMechanicalRebaseSuccess binds the immutable commit returned by the
// planned rebase effect and validates the complete successor.
func BindMechanicalRebaseSuccess(
	previous issueagentcontract.Checkpoint,
	plan DriftTransitionPlan,
	newHeadSHA string,
) (PlannedTransition, error) {
	if plan.Effect.Kind != GitHubEffectRebaseMain ||
		!fullCommitPattern.MatchString(newHeadSHA) ||
		plan.Success.Checkpoint.Work == nil {
		return PlannedTransition{}, errors.New("mechanical merge result is invalid")
	}
	result := plan.Success
	result.Checkpoint.Work.HeadSHA = newHeadSHA
	return validatePlannedTransition(previous, result)
}

// MechanicalRebaseFailure returns the already planned human transition.
func MechanicalRebaseFailure(
	previous issueagentcontract.Checkpoint,
	plan DriftTransitionPlan,
) (PlannedTransition, error) {
	if plan.Effect.Kind != GitHubEffectRebaseMain {
		return PlannedTransition{}, errors.New("mechanical merge failure plan is invalid")
	}
	return validatePlannedTransition(previous, plan.Failure)
}

func successorBase(
	previous issueagentcontract.Checkpoint,
	anchor TransitionAnchor,
) (issueagentcontract.Checkpoint, error) {
	if anchor.CommentID <= 0 || !scheduleDigestPattern.MatchString(anchor.Digest) {
		return issueagentcontract.Checkpoint{}, errors.New("transition predecessor is invalid")
	}
	next := previous
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &anchor.CommentID
	next.PreviousCheckpointSHA256 = &anchor.Digest
	return next, nil
}

func cloneWork(work *issueagentcontract.Work) *issueagentcontract.Work {
	if work == nil {
		return nil
	}
	result := *work
	return &result
}

func applyWorkerAttempt(
	checkpoint *issueagentcontract.Checkpoint,
	facts WorkerAttemptFacts,
) error {
	if checkpoint == nil || facts.TerminalResult == "" {
		return errors.New("Worker attempt facts are invalid")
	}
	elapsed := facts.ElapsedMilliseconds
	if elapsed == 0 {
		elapsed = 1
	}
	seconds := (elapsed + 999) / 1000
	if ^uint64(0)-checkpoint.Budget.WorkerSeconds < seconds {
		return errors.New("Worker time accounting overflow")
	}
	checkpoint.Budget.WorkerSeconds += seconds
	checkpoint.Model = &issueagentcontract.ModelAttempt{
		Provider: facts.Provider, Model: facts.Model,
		AdapterVersion: "v1", PromptPolicyVersion: "v1",
		InputTokens: facts.InputTokens, OutputTokens: facts.OutputTokens,
		ElapsedMilliseconds: elapsed, TerminalResult: facts.TerminalResult,
	}
	return nil
}

// ProjectLifecycleLabels retires scheduler labels for terminal checkpoints and
// ensures human-queue transitions are visible. It returns a sorted unique set.
func ProjectLifecycleLabels(
	state issueagentcontract.State,
	labels []string,
) []string {
	projected := make([]string, 0, len(labels)+1)
	for _, label := range labels {
		if IsTerminalLifecycleState(state) &&
			(label == "ready-for-agent" || label == "ready-for-human") {
			continue
		}
		projected = append(projected, label)
	}
	if state == issueagentcontract.StateReadyForHuman {
		projected = append(projected, "ready-for-human")
	}
	slices.Sort(projected)
	return slices.Compact(projected)
}

// IsTerminalLifecycleState reports states that must retire scheduler labels.
func IsTerminalLifecycleState(state issueagentcontract.State) bool {
	switch state {
	case issueagentcontract.StateAlreadyFixed,
		issueagentcontract.StateMerged,
		issueagentcontract.StateCancelled,
		issueagentcontract.StateSuperseded,
		issueagentcontract.StateWontFix:
		return true
	default:
		return false
	}
}

func validatePlannedTransition(
	previous issueagentcontract.Checkpoint,
	plan PlannedTransition,
) (PlannedTransition, error) {
	if plan.Summary == "" ||
		issueagentcontract.ValidateCheckpointSuccessor(
			previous, plan.Checkpoint,
		) != nil {
		return PlannedTransition{}, errors.New("planned checkpoint transition is invalid")
	}
	return plan, nil
}
