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
)

// Completion is a trusted worker handoff fenced to one generation.
type Completion struct {
	Generation            contract.GenerationIdentity
	Decision              contract.Decision
	EvidenceDigest        string
	ResultDigest          string
	InfrastructureFailure bool
}

// Signal identifies a candidate event and the exact Actions run authority.
type Signal struct {
	Kind       SignalKind
	RunID      int64
	Command    *Command
	Completion *Completion
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
	ActionComplete              PlanAction = "complete"
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
	Action              PlanAction
	Reason              string
	Generation          contract.GenerationIdentity
	DesiredPhase        contract.Phase
	NextScheduler       SchedulerState
	Dispatch            bool
	CancelRunID         int64
	ReuseEvidenceDigest string
	EvidenceDigest      string
	ResultDigest        string
	StatusBody          string
	DispatchExplanation bool
	NextBudget          contract.InteractionBudget
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

	if input.Signal.Kind == SignalCompletion {
		return reconcileCompletion(input)
	}
	if input.Signal.Kind == SignalCommand {
		return reconcileCommand(input)
	}

	nextNumber := uint64(1)
	if input.State != nil {
		nextNumber = input.State.Generation.Generation + 1
	}
	generation := generationFromFacts(input.Facts, nextNumber)

	if !input.Facts.Open {
		return ReconcilePlan{
			Action:        ActionAppendState,
			Reason:        "pull request closed",
			Generation:    generation,
			DesiredPhase:  contract.PhaseClosed,
			NextScheduler: input.Scheduler,
		}, nil
	}
	if input.Facts.Draft {
		return ReconcilePlan{
			Action:        ActionAppendState,
			Reason:        "pull request is draft",
			Generation:    generation,
			DesiredPhase:  contract.PhaseAwaitingReady,
			NextScheduler: input.Scheduler,
		}, nil
	}
	if reason := ineligibleReason(input.Facts, input.Policy); reason != "" {
		return ReconcilePlan{
			Action:        ActionRecordInconclusive,
			Reason:        reason,
			Generation:    generation,
			DesiredPhase:  contract.PhaseInconclusive,
			NextScheduler: input.Scheduler,
		}, nil
	}
	if err := contract.ValidateGenerationIdentity(generation); err != nil {
		return ReconcilePlan{}, err
	}

	if input.State != nil &&
		sameGenerationFacts(input.State.Generation, input.Facts) {
		switch input.State.Phase {
		case contract.PhaseReviewing, contract.PhaseQueued:
			return ReconcilePlan{
				Action:        ActionNoop,
				Reason:        "generation already active",
				Generation:    input.State.Generation,
				DesiredPhase:  input.State.Phase,
				NextScheduler: input.Scheduler,
			}, nil
		case contract.PhaseApproved,
			contract.PhaseChangesRequired,
			contract.PhaseInconclusive:
			return ReconcilePlan{
				Action:        ActionNoop,
				Reason:        "generation already decided",
				Generation:    input.State.Generation,
				DesiredPhase:  input.State.Phase,
				NextScheduler: input.Scheduler,
			}, nil
		}
	}

	scheduler := input.Scheduler
	cancelRunID := int64(0)
	superseding := input.State != nil &&
		!sameGenerationFacts(input.State.Generation, input.Facts)
	reuseEvidence := ""
	if superseding {
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
	scheduler, lease, err := AcquireNext(
		scheduler,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil {
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
			CancelRunID:         cancelRunID,
			ReuseEvidenceDigest: reuseEvidence,
		}, nil
	}
	action := ActionAcquireAndDispatch
	if superseding {
		action = ActionSupersedeAndDispatch
	}
	return ReconcilePlan{
		Action:              action,
		Reason:              "Review Agent lease acquired",
		Generation:          generation,
		DesiredPhase:        contract.PhaseReviewing,
		NextScheduler:       scheduler,
		Dispatch:            true,
		CancelRunID:         cancelRunID,
		ReuseEvidenceDigest: reuseEvidence,
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
		if int(state.Budget.ExplanationsUsed) >=
			input.Policy.MaxExplanationSessionsPerHead {
			return ReconcilePlan{}, errors.New(
				"Review explanation budget exhausted",
			)
		}
		if int(state.Budget.ResponseBytesUsed) >=
			input.Policy.MaxExplanationResponseBytes {
			return ReconcilePlan{}, errors.New(
				"Review explanation response budget exhausted",
			)
		}
		base.Action = ActionExplain
		base.Reason = command.Payload
		base.ResultDigest = state.ResultDigest
		base.DispatchExplanation = true
		base.NextBudget.ExplanationsUsed++
		return base, nil
	case CommandReconsider:
		if !decisionPhase(state.Phase) {
			return ReconcilePlan{}, errors.New(
				"reconsider requires a completed Review decision",
			)
		}
		if int(state.Budget.ReconsiderationsUsed) >=
			input.Policy.MaxReconsiderationsPerHead {
			return ReconcilePlan{}, errors.New(
				"Review reconsideration budget exhausted",
			)
		}
		budget := state.Budget
		budget.ReconsiderationsUsed++
		return scheduleInteraction(
			input,
			ActionReconsiderAndDispatch,
			ActionReconsiderAndEnqueue,
			"explicit reconsideration: "+command.Payload,
			state.EvidenceDigest,
			budget,
		)
	case CommandRetry:
		if state.Phase != contract.PhaseInconclusive {
			return ReconcilePlan{}, errors.New(
				"retry requires an inconclusive Review",
			)
		}
		if int(state.Budget.InfrastructureRetriesUsed) >=
			input.Policy.MaxInfrastructureRetries {
			return ReconcilePlan{}, errors.New(
				"Review infrastructure retry budget exhausted",
			)
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
		)
	case CommandCancel:
		scheduler := input.Scheduler
		for _, lease := range scheduler.Active {
			if lease.Generation.PullRequest != state.Generation.PullRequest {
				continue
			}
			var err error
			scheduler, err = ReleaseLease(
				scheduler,
				lease.Generation,
				lease.RunID,
				input.Now,
			)
			if err != nil {
				return ReconcilePlan{}, err
			}
			base.CancelRunID = lease.RunID
			break
		}
		base.Action = ActionCancel
		base.Reason = "maintainer canceled Review"
		base.DesiredPhase = contract.PhaseCanceled
		base.NextScheduler = scheduler
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
	scheduler, lease, err := AcquireNext(
		scheduler,
		input.Signal.RunID,
		input.Now,
		input.Policy.Scheduler,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if lease == nil {
		return ReconcilePlan{
			Action:              queueAction,
			Reason:              reason,
			Generation:          generation,
			DesiredPhase:        contract.PhaseQueued,
			NextScheduler:       scheduler,
			ReuseEvidenceDigest: reuseEvidence,
			NextBudget:          budget,
		}, nil
	}
	return ReconcilePlan{
		Action:              dispatchAction,
		Reason:              reason,
		Generation:          generation,
		DesiredPhase:        contract.PhaseReviewing,
		NextScheduler:       scheduler,
		Dispatch:            true,
		ReuseEvidenceDigest: reuseEvidence,
		NextBudget:          budget,
	}, nil
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
	if !validCompletionDecision(completion.Decision) ||
		!digestLike(completion.EvidenceDigest) ||
		!digestLike(completion.ResultDigest) {
		return ReconcilePlan{}, errors.New("invalid Review completion")
	}
	scheduler, err := ReleaseLease(
		input.Scheduler,
		completion.Generation,
		input.Signal.RunID,
		input.Now,
	)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return ReconcilePlan{
		Action:         ActionComplete,
		Reason:         "review completed",
		Generation:     completion.Generation,
		DesiredPhase:   phaseForDecision(completion.Decision),
		NextScheduler:  scheduler,
		EvidenceDigest: completion.EvidenceDigest,
		ResultDigest:   completion.ResultDigest,
	}, nil
}

func ineligibleReason(facts PullRequestFacts, policy Policy) string {
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

func sameGenerationFacts(
	generation contract.GenerationIdentity,
	facts PullRequestFacts,
) bool {
	return generation.Repository == facts.Repository &&
		generation.PullRequest == facts.PullRequest &&
		generation.HeadSHA == facts.HeadSHA &&
		generation.BaseSHA == facts.BaseSHA &&
		generation.TestMergeSHA == facts.TestMergeSHA &&
		generation.IntentDigest == facts.IntentDigest
}

func exactCodeCoordinates(
	generation contract.GenerationIdentity,
	facts PullRequestFacts,
) bool {
	return generation.HeadSHA == facts.HeadSHA &&
		generation.BaseSHA == facts.BaseSHA &&
		generation.TestMergeSHA == facts.TestMergeSHA
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
