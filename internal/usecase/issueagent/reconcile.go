package issueagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

var (
	v2RepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	v2SHAPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	v2DigestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// IssueSnapshotFacts are current authenticated GitHub facts, not event claims.
type IssueSnapshotFacts struct {
	Repository          string
	IssueNumber         int64
	Open                bool
	AuthorAssociation   string
	AuthorPermission    string
	IssueSnapshotDigest string
	SourceSHA           string
	AffectedSHA         string
	InformationComplete bool
	MissingInformation  string
	Risk                contract.CandidateRisk
	Authorization       *contract.AuthorizationRecord
	ReviewDigest        string
	PullRequest         *PullRequestFacts
}

// PullRequestFacts are freshly read facts for the one managed Agent PR.
type PullRequestFacts struct {
	Number  int64
	HeadSHA string
	Open    bool
	Draft   bool
	Merged  bool
}

// ReconcileIssuePolicy is protected v2 admission policy.
type ReconcileIssuePolicy struct {
	Enabled              bool
	PolicyDigest         string
	EngineerPromptDigest string
	ReviewPromptDigest   string
	MaxEngineerAttempts  uint32
	MaxReviewIterations  uint32
	TaskStaleAfter       time.Duration
}

// IssueDecisionKind is one deterministic controller action.
type IssueDecisionKind string

const (
	IssueDecisionWaitAuthorization  IssueDecisionKind = "wait_authorization"
	IssueDecisionDispatchEngineer   IssueDecisionKind = "dispatch_engineer"
	IssueDecisionRequestInformation IssueDecisionKind = "request_information"
	IssueDecisionNeedsHuman         IssueDecisionKind = "needs_human"
	IssueDecisionDispatchReview     IssueDecisionKind = "dispatch_review"
	IssueDecisionWait               IssueDecisionKind = "wait"
	IssueDecisionTakeOver           IssueDecisionKind = "take_over"
	IssueDecisionCancel             IssueDecisionKind = "cancel"
	IssueDecisionMarkReady          IssueDecisionKind = "mark_ready"
	IssueDecisionMarkDraft          IssueDecisionKind = "mark_draft"
	IssueDecisionComplete           IssueDecisionKind = "complete"
)

// IssueDecision is the pure next action derived from current facts and state.
type IssueDecision struct {
	Kind         IssueDecisionKind
	NextState    contract.IssueState
	Reason       string
	Task         *contract.TaskIdentity
	ReviewDigest string
}

// ReconcileIssue derives one v2 controller action without external effects.
func ReconcileIssue(
	facts IssueSnapshotFacts,
	current *contract.IssueAgentState,
	policy ReconcileIssuePolicy,
	now time.Time,
) (IssueDecision, error) {
	if err := validateIssueSnapshotFacts(facts, policy, now); err != nil {
		return IssueDecision{}, err
	}
	if current != nil {
		if err := contract.ValidateIssueAgentState(*current); err != nil {
			return IssueDecision{}, errors.New("invalid current Issue Agent state")
		}
	}
	if facts.Authorization != nil {
		if err := validateAuthorizationRecord(*facts.Authorization); err != nil {
			return IssueDecision{}, err
		}
		switch facts.Authorization.Command {
		case "/agent take-over":
			return IssueDecision{
				Kind:      IssueDecisionTakeOver,
				NextState: contract.IssueStateTakenOver,
				Reason:    "maintainer took ownership of the Agent branch",
			}, nil
		case "/agent cancel":
			return IssueDecision{
				Kind:      IssueDecisionCancel,
				NextState: contract.IssueStateCancelled,
				Reason:    "maintainer cancelled automatic work",
			}, nil
		case "/agent retry":
			if current == nil || current.State != contract.IssueStateNeedsHuman {
				facts.Authorization = nil
				break
			}
			retryKind := contract.TaskKindEngineer
			retryAttempts := current.Budget.EngineerAttempts
			retryBudget := policy.MaxEngineerAttempts
			if current.Work != nil {
				retryKind = contract.TaskKindReview
				retryAttempts = current.Budget.ReviewIterations
				retryBudget = policy.MaxReviewIterations
			}
			if retryAttempts >= retryBudget {
				return IssueDecision{
					Kind:      IssueDecisionNeedsHuman,
					NextState: contract.IssueStateNeedsHuman,
					Reason:    "automatic retry budget is exhausted",
				}, nil
			}
			if facts.Risk != contract.CandidateRiskLow {
				return IssueDecision{
					Kind:      IssueDecisionNeedsHuman,
					NextState: contract.IssueStateNeedsHuman,
					Reason:    "trusted risk policy blocks retry publication",
				}, nil
			}
			retryFacts := facts
			if retryKind == contract.TaskKindReview {
				retryFacts.SourceSHA = current.Work.HeadSHA
				retryFacts.ReviewDigest = current.ReviewDigest
			}
			task, err := newIssueTask(retryFacts, policy, retryKind)
			if err != nil {
				return IssueDecision{}, err
			}
			kind := IssueDecisionDispatchEngineer
			nextState := contract.IssueStateEngineering
			if retryKind == contract.TaskKindReview {
				kind = IssueDecisionDispatchReview
				nextState = contract.IssueStateReviewing
			}
			return IssueDecision{
				Kind:         kind,
				NextState:    nextState,
				Reason:       "maintainer authorized a fresh retry",
				Task:         &task,
				ReviewDigest: retryFacts.ReviewDigest,
			}, nil
		}
	}
	taskStaleAfter := policy.TaskStaleAfter
	if taskStaleAfter == 0 {
		taskStaleAfter = 4 * time.Hour
	}
	if current != nil &&
		(current.State == contract.IssueStateEngineering ||
			current.State == contract.IssueStateReviewing) &&
		!now.Before(current.UpdatedAt.Add(taskStaleAfter)) {
		return IssueDecision{
			Kind:      IssueDecisionNeedsHuman,
			NextState: contract.IssueStateNeedsHuman,
			Reason:    "active task did not reach a terminal Publisher result",
		}, nil
	}
	if current != nil && current.Work != nil && facts.PullRequest != nil {
		if facts.PullRequest.Number != current.Work.PullRequest ||
			facts.PullRequest.HeadSHA != current.Work.HeadSHA {
			return IssueDecision{
				Kind:      IssueDecisionNeedsHuman,
				NextState: contract.IssueStateNeedsHuman,
				Reason:    "Agent pull request head changed outside the Publisher",
			}, nil
		}
		if facts.PullRequest.Merged {
			return IssueDecision{
				Kind:      IssueDecisionComplete,
				NextState: contract.IssueStateCompleted,
				Reason:    "human merged the Agent pull request",
			}, nil
		}
		if !facts.PullRequest.Open {
			return IssueDecision{
				Kind:      IssueDecisionNeedsHuman,
				NextState: contract.IssueStateNeedsHuman,
				Reason:    "Agent pull request closed without merge",
			}, nil
		}
	}
	if !facts.Open {
		return IssueDecision{
			Kind:      IssueDecisionCancel,
			NextState: contract.IssueStateCancelled,
			Reason:    "Issue was closed before an Agent repair was merged",
		}, nil
	}
	if facts.ReviewDigest != "" {
		if current == nil ||
			current.State != contract.IssueStateDraft &&
				current.State != contract.IssueStateReadyForReview ||
			current.Work == nil {
			return IssueDecision{}, errors.New("Review task lacks current Agent work")
		}
		if facts.Authorization == nil ||
			facts.Authorization.Permission != "review_agent" ||
			facts.Authorization.Command != "" {
			return IssueDecision{}, errors.New(
				"Review task lacks current Review Agent authority",
			)
		}
		if current.Budget.ReviewIterations >= policy.MaxReviewIterations {
			return IssueDecision{
				Kind:      IssueDecisionNeedsHuman,
				NextState: contract.IssueStateNeedsHuman,
				Reason:    "automatic Review iteration budget is exhausted",
			}, nil
		}
		reviewFacts := facts
		reviewFacts.SourceSHA = current.Work.HeadSHA
		task, err := newIssueTask(reviewFacts, policy, contract.TaskKindReview)
		if err != nil {
			return IssueDecision{}, err
		}
		return IssueDecision{
			Kind:         IssueDecisionDispatchReview,
			NextState:    contract.IssueStateReviewing,
			Reason:       "Review Agent requested changes on the current head",
			Task:         &task,
			ReviewDigest: facts.ReviewDigest,
		}, nil
	}
	if current != nil && current.Work != nil && facts.PullRequest != nil {
		if current.State == contract.IssueStateDraft &&
			!facts.PullRequest.Draft {
			return IssueDecision{
				Kind:      IssueDecisionMarkReady,
				NextState: contract.IssueStateReadyForReview,
				Reason:    "maintainer marked the Agent pull request ready",
			}, nil
		}
		if current.State == contract.IssueStateReadyForReview &&
			facts.PullRequest.Draft {
			return IssueDecision{
				Kind:      IssueDecisionMarkDraft,
				NextState: contract.IssueStateDraft,
				Reason:    "maintainer converted the Agent pull request to Draft",
			}, nil
		}
	}
	if current != nil {
		switch current.State {
		case contract.IssueStateEngineering,
			contract.IssueStateReviewing,
			contract.IssueStateDraft,
			contract.IssueStateReadyForReview,
			contract.IssueStateNeedsHuman,
			contract.IssueStateCompleted,
			contract.IssueStateCancelled,
			contract.IssueStateTakenOver:
			return IssueDecision{
				Kind:      IssueDecisionWait,
				NextState: current.State,
				Reason:    "current Issue Agent state has no new action",
			}, nil
		}
	}
	if !facts.InformationComplete {
		reason := facts.MissingInformation
		if reason == "" {
			reason = "waiting for concrete reproduction information"
		}
		return IssueDecision{
			Kind:      IssueDecisionRequestInformation,
			NextState: contract.IssueStateWaitingForInformation,
			Reason:    reason,
		}, nil
	}
	authorized, err := issueAuthorized(facts)
	if err != nil {
		return IssueDecision{}, err
	}
	if !authorized {
		return IssueDecision{
			Kind:      IssueDecisionWaitAuthorization,
			NextState: contract.IssueStateWaitingForAuthorization,
			Reason:    "waiting for a current maintainer to authorize /agent fix",
		}, nil
	}
	if facts.Risk != contract.CandidateRiskLow {
		return IssueDecision{
			Kind:      IssueDecisionNeedsHuman,
			NextState: contract.IssueStateNeedsHuman,
			Reason:    "trusted risk policy requires human ownership",
		}, nil
	}
	task, err := newIssueTask(facts, policy, contract.TaskKindEngineer)
	if err != nil {
		return IssueDecision{}, err
	}
	return IssueDecision{
		Kind:      IssueDecisionDispatchEngineer,
		NextState: contract.IssueStateEngineering,
		Reason:    "authorized low-risk Bug is ready for engineering",
		Task:      &task,
	}, nil
}

func validateIssueSnapshotFacts(
	facts IssueSnapshotFacts,
	policy ReconcileIssuePolicy,
	now time.Time,
) error {
	if !policy.Enabled {
		return errors.New("Issue Agent v2 is disabled")
	}
	if !v2RepositoryPattern.MatchString(facts.Repository) ||
		facts.IssueNumber <= 0 ||
		!v2DigestPattern.MatchString(facts.IssueSnapshotDigest) ||
		!v2SHAPattern.MatchString(facts.SourceSHA) ||
		!v2SHAPattern.MatchString(facts.AffectedSHA) {
		return errors.New("invalid current Issue facts")
	}
	if facts.ReviewDigest != "" &&
		!v2DigestPattern.MatchString(facts.ReviewDigest) {
		return errors.New("invalid Review thread digest")
	}
	if len(facts.MissingInformation) > 512 {
		return errors.New("missing-information reason is too large")
	}
	if facts.PullRequest != nil &&
		(facts.PullRequest.Number <= 0 ||
			!v2SHAPattern.MatchString(facts.PullRequest.HeadSHA)) {
		return errors.New("invalid pull request facts")
	}
	switch facts.Risk {
	case contract.CandidateRiskLow,
		contract.CandidateRiskInvestigation,
		contract.CandidateRiskHigh:
	default:
		return errors.New("invalid current Issue risk")
	}
	if !v2DigestPattern.MatchString(policy.PolicyDigest) ||
		!v2DigestPattern.MatchString(policy.EngineerPromptDigest) ||
		!v2DigestPattern.MatchString(policy.ReviewPromptDigest) ||
		policy.MaxEngineerAttempts == 0 ||
		policy.MaxReviewIterations == 0 ||
		policy.TaskStaleAfter < 0 ||
		policy.TaskStaleAfter > 24*time.Hour {
		return errors.New("invalid Issue Agent v2 policy")
	}
	if now.IsZero() || now.Location() != time.UTC {
		return errors.New("controller time must use UTC")
	}
	return nil
}

func trustedIssueAuthor(association, permission string) bool {
	return TrustedAssociation(association) && WritePermission(permission)
}

func issueAuthorized(facts IssueSnapshotFacts) (bool, error) {
	if trustedIssueAuthor(facts.AuthorAssociation, facts.AuthorPermission) {
		return true, nil
	}
	if facts.Authorization == nil {
		return false, nil
	}
	authorization := *facts.Authorization
	if err := validateAuthorizationRecord(authorization); err != nil {
		return false, err
	}
	switch authorization.Command {
	case "/agent fix", "/agent retry", "":
		return true, nil
	default:
		return false, errors.New("Issue authorization cannot start engineering")
	}
}

func validateAuthorizationRecord(authorization contract.AuthorizationRecord) error {
	if authorization.Actor == "" || authorization.EventID == "" {
		return errors.New("invalid Issue authorization identity")
	}
	switch authorization.Permission {
	case "write", "maintain", "admin", "review_agent":
		return nil
	default:
		return errors.New("Issue authorization lacks current permission")
	}
}

func newIssueTask(
	facts IssueSnapshotFacts,
	policy ReconcileIssuePolicy,
	kind contract.TaskKind,
) (contract.TaskIdentity, error) {
	promptDigest := policy.EngineerPromptDigest
	if kind == contract.TaskKindReview {
		promptDigest = policy.ReviewPromptDigest
	}
	identity := struct {
		Repository          string            `json:"repository"`
		IssueNumber         int64             `json:"issue_number"`
		IssueSnapshotDigest string            `json:"issue_snapshot_digest"`
		ReviewDigest        string            `json:"review_digest"`
		AuthorizationEvent  string            `json:"authorization_event"`
		Kind                contract.TaskKind `json:"kind"`
		BaseSHA             string            `json:"base_sha"`
		AffectedSHA         string            `json:"affected_sha"`
		PolicyDigest        string            `json:"policy_digest"`
		PromptDigest        string            `json:"prompt_digest"`
	}{
		Repository: facts.Repository, IssueNumber: facts.IssueNumber,
		IssueSnapshotDigest: facts.IssueSnapshotDigest,
		ReviewDigest:        facts.ReviewDigest,
		Kind:                kind, BaseSHA: facts.SourceSHA,
		AffectedSHA: facts.AffectedSHA, PolicyDigest: policy.PolicyDigest,
		PromptDigest: promptDigest,
	}
	if facts.Authorization != nil {
		identity.AuthorizationEvent = facts.Authorization.EventID
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return contract.TaskIdentity{}, errors.New("encode Issue task identity")
	}
	sum := sha256.Sum256(body)
	return contract.TaskIdentity{
		ID: "sha256:" + hex.EncodeToString(sum[:]), Kind: kind,
		BaseSHA: facts.SourceSHA, AffectedSHA: facts.AffectedSHA,
		PolicyDigest: policy.PolicyDigest, PromptDigest: promptDigest,
	}, nil
}
