package reviewagent

import (
	"errors"
	"regexp"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

var lifecycleDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// SignalKind describes only why reconciliation woke up.
type SignalKind string

const (
	SignalOpened         SignalKind = "opened"
	SignalReopened       SignalKind = "reopened"
	SignalSynchronize    SignalKind = "synchronize"
	SignalReadyForReview SignalKind = "ready_for_review"
	SignalConvertedDraft SignalKind = "converted_to_draft"
	SignalEdited         SignalKind = "edited"
	SignalClosed         SignalKind = "closed"
	SignalCommand        SignalKind = "command"
	SignalCompletion     SignalKind = "completion"
	SignalManual         SignalKind = "manual"
	SignalObserved       SignalKind = "observed"
	SignalGovernance     SignalKind = "governance"
	SignalWorkerFailure  SignalKind = "worker_failure"
)

// Completion is a trusted worker handoff fenced to one generation.
type Completion struct {
	Generation            contract.GenerationIdentity `json:"generation"`
	Decision              contract.Decision           `json:"decision"`
	EvidenceDigest        string                      `json:"evidence_digest"`
	ResultDigest          string                      `json:"result_digest"`
	ExplanationDigest     string                      `json:"explanation_digest"`
	ExplanationReply      string                      `json:"explanation_reply"`
	ResponseBytes         uint64                      `json:"response_bytes"`
	InfrastructureFailure bool                        `json:"infrastructure_failure"`
	Findings              []contract.Finding          `json:"findings"`
}

// Signal identifies a candidate event and the exact Actions run authority.
type Signal struct {
	Kind          SignalKind
	RunID         int64
	WorkerAttempt uint32
	Command       *Command
	Completion    *Completion
}

// PlanAction is one bounded orchestration effect.
type PlanAction string

const (
	ActionNoop                  PlanAction = "noop"
	ActionAppendState           PlanAction = "append_state"
	ActionEnqueue               PlanAction = "enqueue"
	ActionAcquireAndDispatch    PlanAction = "acquire_and_dispatch"
	ActionSupersedeAndDispatch  PlanAction = "supersede_and_dispatch"
	ActionSupersedeAndEnqueue   PlanAction = "supersede_and_enqueue"
	ActionRecordInconclusive    PlanAction = "record_inconclusive"
	ActionRecordChangesRequired PlanAction = "record_changes_required"
	ActionComplete              PlanAction = "complete"
	ActionCompleteExplanation   PlanAction = "complete_explanation"
	ActionRepairProjection      PlanAction = "repair_projection"
	ActionRespondStatus         PlanAction = "respond_status"
	ActionExplain               PlanAction = "explain"
	ActionReconsiderAndDispatch PlanAction = "reconsider_and_dispatch"
	ActionReconsiderAndEnqueue  PlanAction = "reconsider_and_enqueue"
	ActionRetryAndDispatch      PlanAction = "retry_and_dispatch"
	ActionRetryAndEnqueue       PlanAction = "retry_and_enqueue"
	ActionCancel                PlanAction = "cancel"
)

// ReconcileInput contains every pure input to one lifecycle decision.
type ReconcileInput struct {
	Facts     PullRequestFacts
	State     *contract.ReviewState
	Scheduler SchedulerState
	Signal    Signal
	Policy    Policy
	Now       time.Time
}

// ReconcilePlan is the only output adapters may execute.
type ReconcilePlan struct {
	Action              PlanAction                  `json:"action"`
	Reason              string                      `json:"reason"`
	Generation          contract.GenerationIdentity `json:"generation"`
	DesiredPhase        contract.Phase              `json:"desired_phase"`
	NextScheduler       SchedulerState              `json:"next_scheduler"`
	Dispatch            bool                        `json:"dispatch"`
	LeaseRunID          int64                       `json:"lease_run_id"`
	NextPullRequest     int64                       `json:"next_pull_request"`
	CancelRunID         int64                       `json:"cancel_run_id"`
	ReuseEvidenceDigest string                      `json:"reuse_evidence_digest"`
	EvidenceDigest      string                      `json:"evidence_digest"`
	ResultDigest        string                      `json:"result_digest"`
	ExplanationDigest   string                      `json:"explanation_digest"`
	ExplanationReply    string                      `json:"explanation_reply"`
	InteractionRequest  string                      `json:"interaction_request"`
	StatusBody          string                      `json:"status_body"`
	DispatchExplanation bool                        `json:"dispatch_explanation"`
	NextBudget          contract.InteractionBudget  `json:"next_budget"`
	DecisionSource      contract.DecisionSource     `json:"decision_source"`
	PriorFindings       []contract.Finding          `json:"prior_findings"`
	DeadlineAt          time.Time                   `json:"deadline_at"`
}

// ReconcilePullRequest deterministically plans one transition without side
// effects.
func ReconcilePullRequest(input ReconcileInput) (ReconcilePlan, error) {
	if err := validatePolicy(input.Policy); err != nil {
		return ReconcilePlan{}, err
	}
	if input.Now.IsZero() || input.Now.Location() != time.UTC {
		return ReconcilePlan{}, errors.New("Review reconciliation time must use UTC")
	}
	if input.Facts.Repository == "" || input.Facts.PullRequest <= 0 ||
		input.Facts.StateParentSHA == "" {
		return ReconcilePlan{}, errors.New("incomplete pull-request identity")
	}
	if err := ValidateSchedulerState(
		input.Scheduler,
		input.Policy.Scheduler,
	); err != nil {
		return ReconcilePlan{}, err
	}
	if input.State != nil {
		if err := contract.ValidateReviewState(*input.State); err != nil {
			return ReconcilePlan{}, err
		}
	}

	sameGeneration := input.State != nil &&
		sameGenerationFacts(input.State.Generation, input.Facts)
	if input.Signal.Kind == SignalCompletion {
		if input.State != nil && !sameGeneration {
			return currentStateNoop(input, "stale completion"), nil
		}
		return reconcileCompletion(input)
	}
	if input.Signal.Kind == SignalWorkerFailure {
		if input.State == nil || sameGeneration {
			return reconcileWorkerFailure(input)
		}
		input.Signal.Kind = SignalObserved
	}
	if input.Signal.Kind == SignalCommand {
		readOnlyStatus := input.Signal.Command != nil &&
			input.Signal.Command.Kind == CommandStatus
		if sameGeneration &&
			(readOnlyStatus || input.Facts.Open && !input.Facts.Draft) {
			return reconcileCommand(input)
		}
		input.Signal.Kind = SignalObserved
	}
	if sameGeneration &&
		input.State.Phase == contract.PhaseClosed &&
		input.Facts.Open {
		sameGeneration = false
	}

	nextNumber := uint64(1)
	if input.State != nil {
		nextNumber = input.State.Generation.Generation + 1
	}
	generation := generationFromFacts(input.Facts, nextNumber)
	if sameGeneration {
		generation = input.State.Generation
	}

	if !input.Facts.Open {
		return reconcileWithoutReviewSession(
			input,
			generation,
			sameGeneration,
			ActionAppendState,
			contract.PhaseClosed,
			"",
			"pull request closed",
		)
	}
	if input.Facts.Draft {
		return reconcileWithoutReviewSession(
			input,
			generation,
			sameGeneration,
			ActionAppendState,
			contract.PhaseAwaitingReady,
			"",
			"pull request is draft",
		)
	}
	if input.Facts.Mergeability == MergeabilityConflicting {
		return reconcileWithoutReviewSession(
			input,
			generation,
			sameGeneration,
			ActionRecordChangesRequired,
			contract.PhaseChangesRequired,
			contract.DecisionSourceMergeConflict,
			"pull request has merge conflicts",
		)
	}
	if reason := ineligibleReason(input.Facts, input.Policy); reason != "" {
		return reconcileWithoutReviewSession(
			input,
			generation,
			sameGeneration,
			ActionRecordInconclusive,
			contract.PhaseInconclusive,
			contract.DecisionSourcePolicy,
			reason,
		)
	}
	if err := contract.ValidateGenerationIdentity(generation); err != nil {
		return ReconcilePlan{}, err
	}

	if sameGeneration {
		switch input.State.Phase {
		case contract.PhaseReviewing:
			if lease := activeLeaseForGeneration(
				input.Scheduler,
				input.State.Generation,
			); lease != nil {
				deadline := generationDeadline(
					input,
					input.State.Generation,
					lease.AcquiredAt,
				)
				if !input.Now.Before(deadline) {
					expired := input
					expired.Signal.RunID = lease.RunID
					return completeInfrastructureFailure(
						expired,
						Completion{Generation: input.State.Generation},
						"Review generation exceeded its wall-time limit",
					)
				}
				dispatch := input.Signal.Kind == SignalManual
				return ReconcilePlan{
					Action:        ActionNoop,
					Reason:        "generation already active",
					Generation:    input.State.Generation,
					DesiredPhase:  input.State.Phase,
					NextScheduler: input.Scheduler,
					Dispatch:      dispatch,
					LeaseRunID:    lease.RunID,
					DeadlineAt:    deadline,
					NextPullRequest: nextEligiblePullRequest(
						input.Scheduler,
						input.Signal.RunID,
						input.Now,
						input.Policy.Scheduler,
					),
				}, nil
			}
			if input.Signal.Kind != SignalManual {
				return ReconcilePlan{}, errors.New(
					"reviewing state lacks its scheduler lease",
				)
			}
		case contract.PhaseQueued, contract.PhaseAwaitingReady:
			// Continue below. A queued generation may acquire capacity, and a
			// ready PR reuses its awaiting-ready generation.
		case contract.PhaseCanceled:
			scheduler, cancelRunID, nextPullRequest, err :=
				removePullRequestWork(
					input.Scheduler,
					input.Facts.PullRequest,
					input.Now,
					input.Policy.Scheduler,
				)
			if err != nil {
				return ReconcilePlan{}, err
			}
			action := ActionNoop
			reason := "generation already canceled"
			if cancelRunID != 0 ||
				input.Signal.Kind == SignalGovernance ||
				input.Signal.Kind == SignalManual {
				action = ActionRepairProjection
				reason = "repair canceled Review projection"
			}
			return ReconcilePlan{
				Action:          action,
				Reason:          reason,
				Generation:      input.State.Generation,
				DesiredPhase:    contract.PhaseCanceled,
				NextScheduler:   scheduler,
				NextPullRequest: nextPullRequest,
				CancelRunID:     cancelRunID,
			}, nil
		case contract.PhaseApproved,
			contract.PhaseChangesRequired,
			contract.PhaseInconclusive:
			if lease := activeLeaseForGeneration(
				input.Scheduler,
				input.State.Generation,
			); lease != nil {
				if input.State.InteractionRequest == "" {
					scheduler, err := ReleaseLease(
						input.Scheduler,
						input.State.Generation,
						lease.RunID,
						input.Now,
						input.Policy.Scheduler,
					)
					if err != nil {
						return ReconcilePlan{}, err
					}
					return ReconcilePlan{
						Action:        ActionRepairProjection,
						Reason:        "repair terminal Review projection",
						Generation:    input.State.Generation,
						DesiredPhase:  input.State.Phase,
						NextScheduler: scheduler,
						NextPullRequest: nextEligiblePullRequest(
							scheduler,
							input.Signal.RunID,
							input.Now,
							input.Policy.Scheduler,
						),
					}, nil
				}
				deadline := generationDeadline(
					input,
					input.State.Generation,
					lease.AcquiredAt,
				)
				if !input.Now.Before(deadline) {
					scheduler, err := ReleaseLease(
						input.Scheduler,
						input.State.Generation,
						lease.RunID,
						input.Now,
						input.Policy.Scheduler,
					)
					if err != nil {
						return ReconcilePlan{}, err
					}
					return ReconcilePlan{
						Action:        ActionNoop,
						Reason:        "generation interaction exceeded its wall-time limit",
						Generation:    input.State.Generation,
						DesiredPhase:  input.State.Phase,
						NextScheduler: scheduler,
					}, nil
				}
				dispatch := input.Signal.Kind == SignalManual
				return ReconcilePlan{
					Action:              ActionNoop,
					Reason:              "generation interaction already active",
					Generation:          input.State.Generation,
					DesiredPhase:        input.State.Phase,
					NextScheduler:       input.Scheduler,
					Dispatch:            dispatch,
					LeaseRunID:          lease.RunID,
					DeadlineAt:          deadline,
					DispatchExplanation: input.State.InteractionRequest != "",
				}, nil
			}
			scheduler, cancelRunID, nextPullRequest, err :=
				removePullRequestWork(
					input.Scheduler,
					input.Facts.PullRequest,
					input.Now,
					input.Policy.Scheduler,
				)
			if err != nil {
				return ReconcilePlan{}, err
			}
			if input.Signal.Kind == SignalGovernance ||
				input.Signal.Kind == SignalManual {
				if input.Signal.Kind == SignalManual &&
					input.State.InteractionRequest != "" {
					return recoverReleasedExplanation(
						input,
						input.Scheduler,
					)
				}
				return ReconcilePlan{
					Action:          ActionRepairProjection,
					Reason:          "repair current Review projection",
					Generation:      input.State.Generation,
					DesiredPhase:    input.State.Phase,
					NextScheduler:   scheduler,
					NextPullRequest: nextPullRequest,
					CancelRunID:     cancelRunID,
				}, nil
			}
			return ReconcilePlan{
				Action:          ActionNoop,
				Reason:          "generation already decided",
				Generation:      input.State.Generation,
				DesiredPhase:    input.State.Phase,
				NextScheduler:   scheduler,
				NextPullRequest: nextPullRequest,
				CancelRunID:     cancelRunID,
			}, nil
		}
	}

	nextBudget := contract.InteractionBudget{}
	if input.State != nil &&
		input.State.Generation.HeadSHA == input.Facts.HeadSHA {
		nextBudget = input.State.Budget
	}
	startAutomaticReview := input.State == nil ||
		!sameGeneration ||
		input.State.Phase == contract.PhaseAwaitingReady
	if startAutomaticReview {
		if int(nextBudget.AutomaticReviewsUsed) >=
			input.Policy.MaxAutomaticReviewsPerHead {
			plan, err := reconcileWithoutReviewSession(
				input,
				generation,
				sameGeneration,
				ActionRecordInconclusive,
				contract.PhaseInconclusive,
				contract.DecisionSourcePolicy,
				"automatic Review budget exhausted for current head",
			)
			plan.NextBudget = nextBudget
			return plan, err
		}
		nextBudget.AutomaticReviewsUsed++
	}

	scheduler := input.Scheduler
	cancelRunID := int64(0)
	superseding := input.State != nil && !sameGeneration
	reuseEvidence := ""
	priorFindings := []contract.Finding(nil)
	if superseding {
		priorFindings = append(
			priorFindings,
			input.State.PriorFindings...,
		)
		if exactCodeCoordinates(input.State.Generation, input.Facts) {
			reuseEvidence = input.State.EvidenceDigest
		}
		for _, lease := range scheduler.Active {
			if lease.Generation.PullRequest != input.Facts.PullRequest {
				continue
			}
			cancelRunID = lease.RunID
			var releaseErr error
			scheduler, releaseErr = ReleaseLease(
				scheduler,
				lease.Generation,
				lease.RunID,
				input.Now,
				input.Policy.Scheduler,
			)
			if releaseErr != nil {
				return ReconcilePlan{}, releaseErr
			}
			break
		}
		scheduler.Queue = removeQueuedPR(
			scheduler.Queue,
			input.Facts.PullRequest,
		)
	}

	var err error
	if lease := activeLeaseForGeneration(scheduler, generation); lease != nil {
		deadline := generationDeadline(
			input,
			generation,
			lease.AcquiredAt,
		)
		if !input.Now.Before(deadline) {
			return expireRecoveredLease(
				input,
				scheduler,
				generation,
				*lease,
				cancelRunID,
				priorFindings,
				nextBudget,
			)
		}
		action := ActionAcquireAndDispatch
		if superseding {
			action = ActionSupersedeAndDispatch
		}
		return ReconcilePlan{
			Action:              action,
			Reason:              "recovered existing Review Agent lease",
			Generation:          generation,
			DesiredPhase:        contract.PhaseReviewing,
			NextScheduler:       scheduler,
			Dispatch:            true,
			LeaseRunID:          lease.RunID,
			DeadlineAt:          deadline,
			CancelRunID:         cancelRunID,
			ReuseEvidenceDigest: reuseEvidence,
			NextBudget:          nextBudget,
			PriorFindings:       priorFindings,
		}, nil
	}
	scheduler, err = Enqueue(
		scheduler,
		QueueEntry{
			Generation: generation,
			FirstTimeExternal: firstTimeExternal(
				input.Facts.AuthorAssociation,
			),
			EnqueuedAt: input.Now,
		},
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	acquiredScheduler, lease, err := AcquireNext(
		scheduler,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil ||
		contract.MustGenerationDigest(lease.Generation) !=
			contract.MustGenerationDigest(generation) {
		nextPullRequest := int64(0)
		if lease != nil {
			nextPullRequest = lease.Generation.PullRequest
		}
		scheduler, err = collapseSchedulerTransition(
			input.Scheduler,
			scheduler,
			input.Now,
			input.Policy.Scheduler,
		)
		if err != nil {
			return ReconcilePlan{}, err
		}
		action := ActionEnqueue
		if superseding {
			action = ActionSupersedeAndEnqueue
		}
		return ReconcilePlan{
			Action:              action,
			Reason:              "waiting for Review Agent capacity",
			Generation:          generation,
			DesiredPhase:        contract.PhaseQueued,
			NextScheduler:       scheduler,
			NextPullRequest:     nextPullRequest,
			CancelRunID:         cancelRunID,
			ReuseEvidenceDigest: reuseEvidence,
			NextBudget:          nextBudget,
			PriorFindings:       priorFindings,
		}, nil
	}
	scheduler = acquiredScheduler
	action := ActionAcquireAndDispatch
	if superseding {
		action = ActionSupersedeAndDispatch
	}
	scheduler, err = collapseSchedulerTransition(
		input.Scheduler,
		scheduler,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return ReconcilePlan{
		Action:        action,
		Reason:        "Review Agent lease acquired",
		Generation:    generation,
		DesiredPhase:  contract.PhaseReviewing,
		NextScheduler: scheduler,
		Dispatch:      true,
		LeaseRunID:    lease.RunID,
		DeadlineAt: lease.AcquiredAt.Add(
			input.Policy.MaxGenerationDuration,
		),
		CancelRunID:         cancelRunID,
		ReuseEvidenceDigest: reuseEvidence,
		NextBudget:          nextBudget,
		PriorFindings:       priorFindings,
	}, nil
}

func reconcileCommand(input ReconcileInput) (ReconcilePlan, error) {
	if input.State == nil || input.Signal.Command == nil {
		return ReconcilePlan{}, errors.New("Review command lacks signed state")
	}
	command := *input.Signal.Command
	state := *input.State
	base := ReconcilePlan{
		Generation:    state.Generation,
		DesiredPhase:  state.Phase,
		NextScheduler: input.Scheduler,
		NextBudget:    state.Budget,
		Reason:        state.Reason,
	}
	switch command.Kind {
	case CommandStatus:
		body, err := RenderStatus(state, input.Now)
		if err != nil {
			return ReconcilePlan{}, err
		}
		base.Action = ActionRespondStatus
		base.Reason = "render signed Review Agent status"
		base.StatusBody = body
		return base, nil
	case CommandExplain:
		if !decisionPhase(state.Phase) {
			base.Action = ActionNoop
			base.Reason = "explain requires a completed Review decision"
			return base, nil
		}
		recovering := state.InteractionRequest == command.Payload
		if state.InteractionRequest != "" && !recovering {
			base.Action = ActionNoop
			base.Reason = "another Review explanation is already pending"
			return base, nil
		}
		if recovering {
			base.Action = ActionNoop
			base.Reason = "Review explanation is already pending"
			return base, nil
		}
		if !recovering &&
			(int(state.Budget.ExplanationsUsed) >=
				input.Policy.MaxExplanationSessionsPerHead ||
				int(state.Budget.ResponseBytesUsed) >=
					input.Policy.MaxExplanationResponseBytes) {
			base.Action = ActionNoop
			base.Reason = "Review explanation budget exhausted"
			return base, nil
		}
		if activeLeaseForGeneration(
			input.Scheduler,
			state.Generation,
		) != nil {
			base.Action = ActionNoop
			base.Reason = "Review generation already has active work"
			return base, nil
		}
		budget := state.Budget
		if !recovering {
			budget.ExplanationsUsed++
		}
		return scheduleExplanation(
			input,
			input.Scheduler,
			budget,
		)
	case CommandReconsider:
		if state.InteractionRequest != "" ||
			pullRequestHasWork(
				input.Scheduler,
				state.Generation.PullRequest,
			) {
			base.Action = ActionNoop
			base.Reason = "Review generation already has pending work"
			return base, nil
		}
		if !decisionPhase(state.Phase) {
			base.Action = ActionNoop
			base.Reason = "reconsider requires a completed Review decision"
			return base, nil
		}
		if int(state.Budget.ReconsiderationsUsed) >=
			input.Policy.MaxReconsiderationsPerHead {
			base.Action = ActionNoop
			base.Reason = "Review reconsideration budget exhausted"
			return base, nil
		}
		budget := state.Budget
		budget.ReconsiderationsUsed++
		budget.InfrastructureRetriesUsed = 0
		return scheduleInteraction(
			input,
			ActionReconsiderAndDispatch,
			ActionReconsiderAndEnqueue,
			"explicit reconsideration: "+command.Payload,
			state.EvidenceDigest,
			budget,
			state.PriorFindings,
		)
	case CommandRetry:
		if state.InteractionRequest != "" ||
			pullRequestHasWork(
				input.Scheduler,
				state.Generation.PullRequest,
			) {
			base.Action = ActionNoop
			base.Reason = "Review generation already has pending work"
			return base, nil
		}
		if state.Phase != contract.PhaseInconclusive {
			base.Action = ActionNoop
			base.Reason = "retry requires an inconclusive Review"
			return base, nil
		}
		if int(state.Budget.InfrastructureRetriesUsed) >=
			input.Policy.MaxInfrastructureRetries {
			base.Action = ActionNoop
			base.Reason = "Review infrastructure retry budget exhausted"
			return base, nil
		}
		budget := state.Budget
		budget.InfrastructureRetriesUsed++
		return scheduleInteraction(
			input,
			ActionRetryAndDispatch,
			ActionRetryAndEnqueue,
			"maintainer retry",
			state.EvidenceDigest,
			budget,
			state.PriorFindings,
		)
	case CommandCancel:
		if !pullRequestHasWork(
			input.Scheduler,
			state.Generation.PullRequest,
		) {
			base.Action = ActionNoop
			base.Reason = "Review has no cancellable work"
			return base, nil
		}
		scheduler, cancelRunID, nextPullRequest, err :=
			removePullRequestWork(
				input.Scheduler,
				state.Generation.PullRequest,
				input.Now,
				input.Policy.Scheduler,
			)
		if err != nil {
			return ReconcilePlan{}, err
		}
		base.Action = ActionCancel
		base.Reason = "maintainer canceled Review"
		base.DesiredPhase = contract.PhaseCanceled
		base.NextScheduler = scheduler
		base.CancelRunID = cancelRunID
		base.NextPullRequest = nextPullRequest
		return base, nil
	default:
		return ReconcilePlan{}, errors.New("unsupported Review command plan")
	}
}

func scheduleInteraction(
	input ReconcileInput,
	dispatchAction PlanAction,
	queueAction PlanAction,
	reason string,
	reuseEvidence string,
	budget contract.InteractionBudget,
	priorFindings []contract.Finding,
) (ReconcilePlan, error) {
	generation := generationFromFacts(
		input.Facts,
		input.State.Generation.Generation+1,
	)
	if err := contract.ValidateGenerationIdentity(generation); err != nil {
		return ReconcilePlan{}, err
	}
	scheduler, err := Enqueue(
		input.Scheduler,
		QueueEntry{
			Generation: generation,
			FirstTimeExternal: firstTimeExternal(
				input.Facts.AuthorAssociation,
			),
			EnqueuedAt: input.Now,
		},
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	acquiredScheduler, lease, err := AcquireNext(
		scheduler,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil ||
		contract.MustGenerationDigest(lease.Generation) !=
			contract.MustGenerationDigest(generation) {
		nextPullRequest := int64(0)
		if lease != nil {
			nextPullRequest = lease.Generation.PullRequest
		}
		scheduler, err = collapseSchedulerTransition(
			input.Scheduler,
			scheduler,
			input.Now,
			input.Policy.Scheduler,
		)
		if err != nil {
			return ReconcilePlan{}, err
		}
		return ReconcilePlan{
			Action:              queueAction,
			Reason:              reason,
			Generation:          generation,
			DesiredPhase:        contract.PhaseQueued,
			NextScheduler:       scheduler,
			NextPullRequest:     nextPullRequest,
			ReuseEvidenceDigest: reuseEvidence,
			NextBudget:          budget,
			PriorFindings: append(
				[]contract.Finding(nil),
				priorFindings...,
			),
		}, nil
	}
	scheduler = acquiredScheduler
	scheduler, err = collapseSchedulerTransition(
		input.Scheduler,
		scheduler,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return ReconcilePlan{
		Action:        dispatchAction,
		Reason:        reason,
		Generation:    generation,
		DesiredPhase:  contract.PhaseReviewing,
		NextScheduler: scheduler,
		Dispatch:      true,
		LeaseRunID:    lease.RunID,
		DeadlineAt: lease.AcquiredAt.Add(
			input.Policy.MaxGenerationDuration,
		),
		ReuseEvidenceDigest: reuseEvidence,
		NextBudget:          budget,
		PriorFindings: append(
			[]contract.Finding(nil),
			priorFindings...,
		),
	}, nil
}

func scheduleExplanation(
	input ReconcileInput,
	scheduler SchedulerState,
	budget contract.InteractionBudget,
) (ReconcilePlan, error) {
	base := ReconcilePlan{
		Action:              ActionExplain,
		Reason:              input.State.Reason,
		Generation:          input.State.Generation,
		DesiredPhase:        input.State.Phase,
		NextScheduler:       scheduler,
		InteractionRequest:  input.State.InteractionRequest,
		ReuseEvidenceDigest: input.State.EvidenceDigest,
		ResultDigest:        input.State.ResultDigest,
		DecisionSource:      input.State.DecisionSource,
		NextBudget:          budget,
		PriorFindings: append(
			[]contract.Finding(nil),
			input.State.PriorFindings...,
		),
	}
	if input.Signal.Command != nil {
		base.InteractionRequest = input.Signal.Command.Payload
	}
	queued, err := Enqueue(
		scheduler,
		QueueEntry{
			Generation:        input.State.Generation,
			FirstTimeExternal: firstTimeExternal(input.Facts.AuthorAssociation),
			EnqueuedAt:        input.Now,
		},
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	acquired, lease, err := AcquireNext(
		queued,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil ||
		contract.MustGenerationDigest(lease.Generation) !=
			contract.MustGenerationDigest(input.State.Generation) {
		base.NextScheduler, err = collapseSchedulerTransition(
			scheduler,
			queued,
			input.Now,
			input.Policy.Scheduler,
		)
		if err != nil {
			return ReconcilePlan{}, err
		}
		if lease != nil {
			base.NextPullRequest = lease.Generation.PullRequest
		}
		return base, nil
	}
	base.NextScheduler, err = collapseSchedulerTransition(
		scheduler,
		acquired,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	base.Dispatch = true
	base.DispatchExplanation = true
	base.LeaseRunID = lease.RunID
	base.DeadlineAt = lease.AcquiredAt.Add(input.Policy.MaxGenerationDuration)
	return base, nil
}

func reconcileCompletion(input ReconcileInput) (ReconcilePlan, error) {
	if input.State == nil || input.Signal.Completion == nil {
		return ReconcilePlan{}, errors.New("completion lacks active Review state")
	}
	completion := input.Signal.Completion
	if contract.MustGenerationDigest(completion.Generation) !=
		contract.MustGenerationDigest(input.State.Generation) {
		return ReconcilePlan{
			Action:        ActionNoop,
			Reason:        "stale completion",
			Generation:    input.State.Generation,
			DesiredPhase:  input.State.Phase,
			NextScheduler: input.Scheduler,
		}, nil
	}
	lease := activeLeaseForGeneration(input.Scheduler, completion.Generation)
	if lease == nil || lease.RunID != input.Signal.RunID {
		return ReconcilePlan{}, errors.New(
			"completion lacks matching active Review lease",
		)
	}
	if !input.Now.Before(generationDeadline(
		input,
		completion.Generation,
		lease.AcquiredAt,
	)) {
		if completion.ExplanationDigest != "" &&
			decisionPhase(input.State.Phase) {
			return ReconcilePlan{}, errors.New(
				"Review explanation completion exceeded its wall-time limit",
			)
		}
		return completeInfrastructureFailure(
			input,
			*completion,
			"Review generation exceeded its wall-time limit",
		)
	}
	if completion.ExplanationDigest != "" {
		return reconcileExplanationCompletion(input)
	}
	if completion.ExplanationReply != "" || completion.ResponseBytes != 0 {
		return ReconcilePlan{}, errors.New("invalid Review completion")
	}
	if !validCompletionDecision(completion.Decision) {
		return ReconcilePlan{}, errors.New("invalid Review completion")
	}
	if completion.InfrastructureFailure {
		if completion.Decision != contract.DecisionInconclusive ||
			completion.EvidenceDigest != "" ||
			completion.ResultDigest != "" ||
			len(completion.Findings) != 0 {
			return ReconcilePlan{}, errors.New(
				"invalid Review infrastructure completion",
			)
		}
		if int(input.State.Budget.InfrastructureRetriesUsed) <
			input.Policy.MaxInfrastructureRetries {
			budget := input.State.Budget
			budget.InfrastructureRetriesUsed++
			return ReconcilePlan{
				Action:        ActionRetryAndDispatch,
				Reason:        "automatic infrastructure retry",
				Generation:    completion.Generation,
				DesiredPhase:  contract.PhaseReviewing,
				NextScheduler: input.Scheduler,
				Dispatch:      true,
				LeaseRunID:    lease.RunID,
				DeadlineAt: generationDeadline(
					input,
					completion.Generation,
					lease.AcquiredAt,
				),
				NextBudget: budget,
				PriorFindings: append(
					[]contract.Finding(nil),
					input.State.PriorFindings...,
				),
			}, nil
		}
		return completeInfrastructureFailure(
			input,
			*completion,
			"Review infrastructure retry budget exhausted",
		)
	} else if !digestLike(completion.EvidenceDigest) ||
		!digestLike(completion.ResultDigest) {
		return ReconcilePlan{}, errors.New("invalid Review completion")
	}
	if len(completion.Findings) > contract.MaxFindings {
		return ReconcilePlan{}, errors.New("too many Review completion findings")
	}
	for _, finding := range completion.Findings {
		if err := contract.ValidateFinding(finding); err != nil {
			return ReconcilePlan{}, err
		}
	}
	scheduler, err := ReleaseLease(
		input.Scheduler,
		completion.Generation,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return ReconcilePlan{
		Action:        ActionComplete,
		Reason:        "review completed",
		Generation:    completion.Generation,
		DesiredPhase:  phaseForDecision(completion.Decision),
		NextScheduler: scheduler,
		NextPullRequest: nextEligiblePullRequest(
			scheduler,
			input.Signal.RunID,
			input.Now,
			input.Policy.Scheduler,
		),
		EvidenceDigest: completion.EvidenceDigest,
		ResultDigest:   completion.ResultDigest,
		DecisionSource: contract.DecisionSourceModel,
		PriorFindings: append(
			[]contract.Finding(nil),
			completion.Findings...,
		),
	}, nil
}

func completeInfrastructureFailure(
	input ReconcileInput,
	completion Completion,
	reason string,
) (ReconcilePlan, error) {
	scheduler, err := ReleaseLease(
		input.Scheduler,
		completion.Generation,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return ReconcilePlan{
		Action:        ActionComplete,
		Reason:        reason,
		Generation:    completion.Generation,
		DesiredPhase:  contract.PhaseInconclusive,
		NextScheduler: scheduler,
		NextPullRequest: nextEligiblePullRequest(
			scheduler,
			input.Signal.RunID,
			input.Now,
			input.Policy.Scheduler,
		),
		NextBudget:     input.State.Budget,
		DecisionSource: contract.DecisionSourceInfrastructure,
		PriorFindings: append(
			[]contract.Finding(nil),
			input.State.PriorFindings...,
		),
	}, nil
}

func reconcileWorkerFailure(input ReconcileInput) (ReconcilePlan, error) {
	if input.State == nil {
		return currentStateNoop(input, "worker failure is no longer active"), nil
	}
	if input.Signal.WorkerAttempt <
		input.State.Budget.InfrastructureRetriesUsed {
		lease := activeLeaseForGeneration(
			input.Scheduler,
			input.State.Generation,
		)
		if input.State.Phase != contract.PhaseReviewing ||
			lease == nil || lease.RunID != input.Signal.RunID {
			return currentStateNoop(input, "stale worker failure"), nil
		}
		deadline := generationDeadline(
			input,
			input.State.Generation,
			lease.AcquiredAt,
		)
		if !input.Now.Before(deadline) {
			return completeInfrastructureFailure(
				input,
				Completion{Generation: input.State.Generation},
				"Review generation exceeded its wall-time limit",
			)
		}
		return ReconcilePlan{
			Action:        ActionNoop,
			Reason:        "persisted infrastructure retry still requires dispatch",
			Generation:    input.State.Generation,
			DesiredPhase:  input.State.Phase,
			NextScheduler: input.Scheduler,
			Dispatch:      true,
			LeaseRunID:    lease.RunID,
			DeadlineAt:    deadline,
		}, nil
	}
	if input.Signal.WorkerAttempt >
		input.State.Budget.InfrastructureRetriesUsed {
		return currentStateNoop(input, "invalid worker failure attempt"), nil
	}
	lease := activeLeaseForGeneration(
		input.Scheduler,
		input.State.Generation,
	)
	if lease == nil || lease.RunID != input.Signal.RunID {
		if decisionPhase(input.State.Phase) {
			if input.State.InteractionRequest != "" {
				return recoverReleasedExplanation(input, input.Scheduler)
			}
			plan := currentStateNoop(
				input,
				"repair projection after worker failure",
			)
			plan.Action = ActionRepairProjection
			plan.NextPullRequest = nextEligiblePullRequest(
				input.Scheduler,
				input.Signal.RunID,
				input.Now,
				input.Policy.Scheduler,
			)
			return plan, nil
		}
		if input.State.Phase == contract.PhaseReviewing && lease == nil {
			return recoverReleasedWorker(input)
		}
		return currentStateNoop(input, "stale worker failure"), nil
	}
	if decisionPhase(input.State.Phase) {
		scheduler, err := ReleaseLease(
			input.Scheduler,
			input.State.Generation,
			lease.RunID,
			input.Now,
			input.Policy.Scheduler,
		)
		if err != nil {
			return ReconcilePlan{}, err
		}
		if input.State.InteractionRequest != "" {
			return recoverReleasedExplanation(input, scheduler)
		}
		plan := currentStateNoop(
			input,
			"repair terminal projection after worker failure",
		)
		plan.Action = ActionRepairProjection
		plan.NextScheduler = scheduler
		plan.NextPullRequest = nextEligiblePullRequest(
			scheduler,
			input.Signal.RunID,
			input.Now,
			input.Policy.Scheduler,
		)
		return plan, nil
	}
	if input.State.Phase != contract.PhaseReviewing {
		return currentStateNoop(input, "worker failure is no longer active"), nil
	}
	failure := input
	failure.Signal.Kind = SignalCompletion
	failure.Signal.Completion = &Completion{
		Generation:            input.State.Generation,
		Decision:              contract.DecisionInconclusive,
		InfrastructureFailure: true,
	}
	return reconcileCompletion(failure)
}

func recoverReleasedExplanation(
	input ReconcileInput,
	scheduler SchedulerState,
) (ReconcilePlan, error) {
	base := ReconcilePlan{
		Reason:              "recover failed Review explanation",
		Generation:          input.State.Generation,
		DesiredPhase:        input.State.Phase,
		NextScheduler:       scheduler,
		InteractionRequest:  input.State.InteractionRequest,
		ReuseEvidenceDigest: input.State.EvidenceDigest,
		ResultDigest:        input.State.ResultDigest,
		DecisionSource:      input.State.DecisionSource,
		NextBudget:          input.State.Budget,
		PriorFindings: append(
			[]contract.Finding(nil),
			input.State.PriorFindings...,
		),
	}
	deadline := generationDeadline(
		input,
		input.State.Generation,
		input.Now,
	)
	if !input.Now.Before(deadline) {
		return completeFailedExplanation(
			input,
			scheduler,
			base,
			"Review Agent could not produce a trustworthy explanation before its signed wall-time limit.",
		)
	}
	if int(base.NextBudget.InfrastructureRetriesUsed) >=
		input.Policy.MaxInfrastructureRetries {
		return completeFailedExplanation(
			input,
			scheduler,
			base,
			"Review Agent could not produce a trustworthy explanation because its infrastructure retry budget was exhausted.",
		)
	}
	base.DeadlineAt = deadline
	base.NextBudget.InfrastructureRetriesUsed++
	queued, err := Enqueue(
		scheduler,
		QueueEntry{
			Generation:        input.State.Generation,
			FirstTimeExternal: firstTimeExternal(input.Facts.AuthorAssociation),
			EnqueuedAt:        input.Now,
		},
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	acquired, lease, err := AcquireNext(
		queued,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil ||
		contract.MustGenerationDigest(lease.Generation) !=
			contract.MustGenerationDigest(input.State.Generation) {
		base.Action = ActionRetryAndEnqueue
		base.NextScheduler, err = collapseSchedulerTransition(
			scheduler,
			queued,
			input.Now,
			input.Policy.Scheduler,
		)
		if err != nil {
			return ReconcilePlan{}, err
		}
		if lease != nil {
			base.NextPullRequest = lease.Generation.PullRequest
		}
		return base, nil
	}
	base.Action = ActionRetryAndDispatch
	base.NextScheduler, err = collapseSchedulerTransition(
		scheduler,
		acquired,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	base.Dispatch = true
	base.DispatchExplanation = true
	base.LeaseRunID = lease.RunID
	return base, nil
}

func completeFailedExplanation(
	input ReconcileInput,
	scheduler SchedulerState,
	base ReconcilePlan,
	reply string,
) (ReconcilePlan, error) {
	base.Action = ActionCompleteExplanation
	base.Reason = input.State.Reason
	base.InteractionRequest = ""
	base.ExplanationDigest = input.State.ExplanationDigest
	base.ExplanationReply = input.State.ExplanationReply
	if base.NextBudget.ResponseBytesUsed+uint64(len([]byte(reply))) <=
		uint64(input.Policy.MaxExplanationResponseBytes) {
		explanation := contract.ExplanationResult{
			SchemaVersion: 1,
			Generation:    input.State.Generation,
			Reply:         reply,
		}
		digest, err := contract.ExplanationResultDigest(explanation)
		if err != nil {
			return ReconcilePlan{}, err
		}
		base.ExplanationDigest = digest
		base.ExplanationReply = reply
		base.NextBudget.ResponseBytesUsed += uint64(len([]byte(reply)))
	}
	base.NextPullRequest = nextEligiblePullRequest(
		scheduler,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	return base, nil
}

func recoverReleasedWorker(input ReconcileInput) (ReconcilePlan, error) {
	deadline := generationDeadline(
		input,
		input.State.Generation,
		input.Now,
	)
	if !input.Now.Before(deadline) {
		return ReconcilePlan{
			Action:         ActionComplete,
			Reason:         "Review generation exceeded its wall-time limit",
			Generation:     input.State.Generation,
			DesiredPhase:   contract.PhaseInconclusive,
			NextScheduler:  input.Scheduler,
			NextBudget:     input.State.Budget,
			DecisionSource: contract.DecisionSourceInfrastructure,
			PriorFindings: append(
				[]contract.Finding(nil),
				input.State.PriorFindings...,
			),
			NextPullRequest: nextEligiblePullRequest(
				input.Scheduler,
				input.Signal.RunID,
				input.Now,
				input.Policy.Scheduler,
			),
		}, nil
	}
	if int(input.State.Budget.InfrastructureRetriesUsed) >=
		input.Policy.MaxInfrastructureRetries {
		return ReconcilePlan{
			Action:         ActionComplete,
			Reason:         "Review infrastructure retry budget exhausted",
			Generation:     input.State.Generation,
			DesiredPhase:   contract.PhaseInconclusive,
			NextScheduler:  input.Scheduler,
			NextBudget:     input.State.Budget,
			DecisionSource: contract.DecisionSourceInfrastructure,
			PriorFindings: append(
				[]contract.Finding(nil),
				input.State.PriorFindings...,
			),
			NextPullRequest: nextEligiblePullRequest(
				input.Scheduler,
				input.Signal.RunID,
				input.Now,
				input.Policy.Scheduler,
			),
		}, nil
	}
	budget := input.State.Budget
	budget.InfrastructureRetriesUsed++
	scheduler, err := Enqueue(
		input.Scheduler,
		QueueEntry{
			Generation: input.State.Generation,
			FirstTimeExternal: firstTimeExternal(
				input.Facts.AuthorAssociation,
			),
			EnqueuedAt: input.Now,
		},
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	acquired, lease, err := AcquireNext(
		scheduler,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil ||
		contract.MustGenerationDigest(lease.Generation) !=
			contract.MustGenerationDigest(input.State.Generation) {
		nextPullRequest := int64(0)
		if lease != nil {
			nextPullRequest = lease.Generation.PullRequest
		}
		scheduler, err = collapseSchedulerTransition(
			input.Scheduler,
			scheduler,
			input.Now,
			input.Policy.Scheduler,
		)
		if err != nil {
			return ReconcilePlan{}, err
		}
		return ReconcilePlan{
			Action:          ActionRetryAndEnqueue,
			Reason:          "recover released failed worker",
			Generation:      input.State.Generation,
			DesiredPhase:    contract.PhaseQueued,
			NextScheduler:   scheduler,
			NextPullRequest: nextPullRequest,
			NextBudget:      budget,
			PriorFindings: append(
				[]contract.Finding(nil),
				input.State.PriorFindings...,
			),
		}, nil
	}
	acquired, err = collapseSchedulerTransition(
		input.Scheduler,
		acquired,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return ReconcilePlan{
		Action:        ActionRetryAndDispatch,
		Reason:        "recover released failed worker",
		Generation:    input.State.Generation,
		DesiredPhase:  contract.PhaseReviewing,
		NextScheduler: acquired,
		Dispatch:      true,
		LeaseRunID:    lease.RunID,
		DeadlineAt:    deadline,
		NextBudget:    budget,
		PriorFindings: append(
			[]contract.Finding(nil),
			input.State.PriorFindings...,
		),
	}, nil
}

func generationDeadline(
	input ReconcileInput,
	generation contract.GenerationIdentity,
	fallback time.Time,
) time.Time {
	if input.State != nil &&
		contract.MustGenerationDigest(input.State.Generation) ==
			contract.MustGenerationDigest(generation) &&
		!input.State.SessionDeadlineAt.IsZero() {
		return input.State.SessionDeadlineAt
	}
	return fallback.Add(input.Policy.MaxGenerationDuration)
}

func expireRecoveredLease(
	input ReconcileInput,
	scheduler SchedulerState,
	generation contract.GenerationIdentity,
	lease Lease,
	cancelRunID int64,
	priorFindings []contract.Finding,
	budget contract.InteractionBudget,
) (ReconcilePlan, error) {
	released, err := ReleaseLease(
		scheduler,
		generation,
		lease.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if cancelRunID == 0 {
		cancelRunID = lease.RunID
	}
	return ReconcilePlan{
		Action:         ActionRecordInconclusive,
		Reason:         "recovered Review generation exceeded its wall-time limit",
		Generation:     generation,
		DesiredPhase:   contract.PhaseInconclusive,
		NextScheduler:  released,
		CancelRunID:    cancelRunID,
		DecisionSource: contract.DecisionSourceInfrastructure,
		NextBudget:     budget,
		PriorFindings: append(
			[]contract.Finding(nil),
			priorFindings...,
		),
		NextPullRequest: nextEligiblePullRequest(
			released,
			input.Signal.RunID,
			input.Now,
			input.Policy.Scheduler,
		),
	}, nil
}

func currentStateNoop(input ReconcileInput, reason string) ReconcilePlan {
	plan := ReconcilePlan{
		Action:        ActionNoop,
		Reason:        reason,
		NextScheduler: input.Scheduler,
	}
	if input.State != nil {
		plan.Generation = input.State.Generation
		plan.DesiredPhase = input.State.Phase
	}
	return plan
}

func reconcileExplanationCompletion(
	input ReconcileInput,
) (ReconcilePlan, error) {
	completion := input.Signal.Completion
	state := input.State
	explanation := contract.ExplanationResult{
		SchemaVersion: 1,
		Generation:    completion.Generation,
		Reply:         completion.ExplanationReply,
	}
	explanationDigest, explanationErr :=
		contract.ExplanationResultDigest(explanation)
	if !decisionPhase(state.Phase) ||
		!digestLike(completion.ExplanationDigest) ||
		explanationErr != nil ||
		explanationDigest != completion.ExplanationDigest ||
		completion.ResponseBytes == 0 ||
		completion.ResponseBytes != uint64(len([]byte(
			completion.ExplanationReply,
		))) ||
		completion.ResponseBytes >
			uint64(input.Policy.MaxExplanationResponseBytes) ||
		state.Budget.ResponseBytesUsed+completion.ResponseBytes >
			uint64(input.Policy.MaxExplanationResponseBytes) ||
		completion.Decision != "" ||
		completion.EvidenceDigest != "" ||
		completion.ResultDigest != "" ||
		len(completion.Findings) != 0 ||
		completion.InfrastructureFailure {
		return ReconcilePlan{}, errors.New(
			"invalid Review explanation completion",
		)
	}
	scheduler, err := ReleaseLease(
		input.Scheduler,
		completion.Generation,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	budget := state.Budget
	budget.ResponseBytesUsed += completion.ResponseBytes
	return ReconcilePlan{
		Action:        ActionCompleteExplanation,
		Reason:        state.Reason,
		Generation:    state.Generation,
		DesiredPhase:  state.Phase,
		NextScheduler: scheduler,
		NextPullRequest: nextEligiblePullRequest(
			scheduler,
			input.Signal.RunID,
			input.Now,
			input.Policy.Scheduler,
		),
		ReuseEvidenceDigest: state.EvidenceDigest,
		ResultDigest:        state.ResultDigest,
		ExplanationDigest:   completion.ExplanationDigest,
		ExplanationReply:    completion.ExplanationReply,
		NextBudget:          budget,
	}, nil
}

func activeLeaseForGeneration(
	scheduler SchedulerState,
	generation contract.GenerationIdentity,
) *Lease {
	digest := contract.MustGenerationDigest(generation)
	for index := range scheduler.Active {
		if contract.MustGenerationDigest(
			scheduler.Active[index].Generation,
		) == digest {
			lease := scheduler.Active[index]
			return &lease
		}
	}
	return nil
}

func nextEligiblePullRequest(
	scheduler SchedulerState,
	runID int64,
	now time.Time,
	limits SchedulerLimits,
) int64 {
	_, lease, err := AcquireNext(scheduler, runID, now, limits)
	if err != nil || lease == nil {
		return 0
	}
	return lease.Generation.PullRequest
}

func removePullRequestWork(
	scheduler SchedulerState,
	pullRequest int64,
	now time.Time,
	limits SchedulerLimits,
) (SchedulerState, int64, int64, error) {
	candidate := scheduler
	candidate.Queue = removeQueuedPR(candidate.Queue, pullRequest)
	candidate.Active = append([]Lease(nil), scheduler.Active...)
	cancelRunID := int64(0)
	for index := range candidate.Active {
		if candidate.Active[index].Generation.PullRequest != pullRequest {
			continue
		}
		cancelRunID = candidate.Active[index].RunID
		candidate.Active = append(
			candidate.Active[:index],
			candidate.Active[index+1:]...,
		)
		break
	}
	if len(candidate.Queue) != len(scheduler.Queue) ||
		len(candidate.Active) != len(scheduler.Active) {
		var err error
		candidate, err = collapseSchedulerTransition(
			scheduler,
			func() SchedulerState {
				changed := candidate
				changed.Sequence = scheduler.Sequence + 1
				return changed
			}(),
			now,
			limits,
		)
		if err != nil {
			return SchedulerState{}, 0, 0, err
		}
	}
	return candidate, cancelRunID, nextEligiblePullRequest(
		candidate,
		1,
		now,
		limits,
	), nil
}

// collapseSchedulerTransition makes a composed in-memory scheduling decision
// one append-only successor of the last persisted scheduler state.
func collapseSchedulerTransition(
	previous SchedulerState,
	candidate SchedulerState,
	now time.Time,
	limits SchedulerLimits,
) (SchedulerState, error) {
	if previous.Sequence == candidate.Sequence {
		return candidate, nil
	}
	previousDigest, err := SchedulerStateDigest(previous, limits)
	if err != nil {
		return SchedulerState{}, err
	}
	candidate.Sequence = previous.Sequence + 1
	candidate.PreviousStateDigest = previousDigest
	candidate.UpdatedAt = now
	if err := ValidateSchedulerState(candidate, limits); err != nil {
		return SchedulerState{}, err
	}
	return candidate, nil
}

func ineligibleReason(facts PullRequestFacts, policy Policy) string {
	if facts.ContextFailureReason != "" {
		return facts.ContextFailureReason
	}
	if !supportedBase(policy, facts.BaseRef) {
		return "unsupported base branch"
	}
	if facts.TestMergeSHA == "" {
		return "test-merge revision is unavailable"
	}
	if facts.Mergeability != MergeabilityClean {
		return "pull request is not cleanly mergeable"
	}
	if facts.ChangedFiles <= 0 {
		return "changed-file inventory is empty"
	}
	if facts.ChangedFiles > policy.MaxChangedFiles {
		return "changed-file budget exceeded"
	}
	if facts.ChangedBytes < 0 ||
		facts.ChangedBytes > policy.MaxChangedBytes {
		return "changed-byte budget exceeded"
	}
	if facts.ChangedLines < 0 ||
		facts.ChangedLines > policy.MaxChangedLines {
		return "changed-line budget exceeded"
	}
	return ""
}

func reconcileWithoutReviewSession(
	input ReconcileInput,
	generation contract.GenerationIdentity,
	sameGeneration bool,
	action PlanAction,
	phase contract.Phase,
	source contract.DecisionSource,
	reason string,
) (ReconcilePlan, error) {
	scheduler, cancelRunID, nextPullRequest, err := removePullRequestWork(
		input.Scheduler,
		input.Facts.PullRequest,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if sameGeneration && input.State.Phase == phase {
		action = ActionNoop
	}
	return ReconcilePlan{
		Action:          action,
		Reason:          reason,
		Generation:      generation,
		DesiredPhase:    phase,
		NextScheduler:   scheduler,
		NextPullRequest: nextPullRequest,
		CancelRunID:     cancelRunID,
		DecisionSource:  source,
		PriorFindings: func() []contract.Finding {
			if input.State == nil {
				return nil
			}
			return append(
				[]contract.Finding(nil),
				input.State.PriorFindings...,
			)
		}(),
	}, nil
}

func sameGenerationFacts(
	generation contract.GenerationIdentity,
	facts PullRequestFacts,
) bool {
	return generation.Repository == facts.Repository &&
		generation.PullRequest == facts.PullRequest &&
		generation.HeadSHA == facts.HeadSHA &&
		generation.BaseSHA == facts.BaseSHA &&
		generation.TestMergeSHA ==
			NormalizeTestMergeSHA(facts.TestMergeSHA) &&
		generation.IntentDigest == facts.IntentDigest &&
		generation.StateParentSHA == facts.StateParentSHA
}

func exactCodeCoordinates(
	generation contract.GenerationIdentity,
	facts PullRequestFacts,
) bool {
	return generation.HeadSHA == facts.HeadSHA &&
		generation.BaseSHA == facts.BaseSHA &&
		generation.TestMergeSHA ==
			NormalizeTestMergeSHA(facts.TestMergeSHA)
}

func removeQueuedPR(queue []QueueEntry, pullRequest int64) []QueueEntry {
	result := make([]QueueEntry, 0, len(queue))
	for _, entry := range queue {
		if entry.Generation.PullRequest != pullRequest {
			result = append(result, entry)
		}
	}
	return result
}

func pullRequestHasWork(state SchedulerState, pullRequest int64) bool {
	for _, lease := range state.Active {
		if lease.Generation.PullRequest == pullRequest {
			return true
		}
	}
	for _, entry := range state.Queue {
		if entry.Generation.PullRequest == pullRequest {
			return true
		}
	}
	return false
}

func validCompletionDecision(decision contract.Decision) bool {
	switch decision {
	case contract.DecisionApproved,
		contract.DecisionChangesRequired,
		contract.DecisionInconclusive:
		return true
	default:
		return false
	}
}

func phaseForDecision(decision contract.Decision) contract.Phase {
	switch decision {
	case contract.DecisionApproved:
		return contract.PhaseApproved
	case contract.DecisionChangesRequired:
		return contract.PhaseChangesRequired
	default:
		return contract.PhaseInconclusive
	}
}

func decisionPhase(phase contract.Phase) bool {
	switch phase {
	case contract.PhaseApproved,
		contract.PhaseChangesRequired,
		contract.PhaseInconclusive:
		return true
	default:
		return false
	}
}

func digestLike(value string) bool {
	return lifecycleDigestPattern.MatchString(value)
}
