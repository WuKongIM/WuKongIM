package issueagent

import (
	"errors"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// BuildIssueState applies one pure decision to the canonical durable state.
func BuildIssueState(
	current *contract.IssueAgentState,
	facts IssueSnapshotFacts,
	decision IssueDecision,
	now time.Time,
) (contract.IssueAgentState, error) {
	if !v2RepositoryPattern.MatchString(facts.Repository) ||
		facts.IssueNumber <= 0 ||
		!v2DigestPattern.MatchString(facts.IssueSnapshotDigest) ||
		!v2SHAPattern.MatchString(facts.SourceSHA) ||
		now.IsZero() || now.Location() != time.UTC {
		return contract.IssueAgentState{}, errors.New("invalid Issue state input")
	}
	if decision.Kind == "" || decision.NextState == "" || decision.Reason == "" {
		return contract.IssueAgentState{}, errors.New("invalid Issue decision")
	}

	next := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          facts.Repository,
		IssueNumber:         facts.IssueNumber,
		Sequence:            1,
		State:               decision.NextState,
		Reason:              decision.Reason,
		IssueSnapshotDigest: facts.IssueSnapshotDigest,
		SourceSHA:           facts.SourceSHA,
		Task:                decision.Task,
		Authorization:       nil,
		UpdatedAt:           now,
	}
	if current != nil {
		if err := contract.ValidateIssueAgentState(*current); err != nil {
			return contract.IssueAgentState{}, errors.New("invalid current Issue Agent state")
		}
		if current.Repository != facts.Repository ||
			current.IssueNumber != facts.IssueNumber {
			return contract.IssueAgentState{}, errors.New("Issue state identity changed")
		}
		previousDigest, err := contract.IssueAgentStateDigest(*current)
		if err != nil {
			return contract.IssueAgentState{}, err
		}
		next.Sequence = current.Sequence + 1
		next.PreviousStateDigest = previousDigest
		next.Budget = current.Budget
		next.Authorization = current.Authorization
		next.Work = current.Work
		next.StatusCommentID = current.StatusCommentID
		next.ContextDigest = current.ContextDigest
		next.CandidateDigest = current.CandidateDigest
		next.EvidenceDigest = current.EvidenceDigest
		next.ReviewDigest = current.ReviewDigest
		next.TakenOverBy = current.TakenOverBy
		if decision.Kind == IssueDecisionWait {
			next.Task = current.Task
		}
	}
	switch decision.Kind {
	case IssueDecisionDispatchEngineer:
		if facts.Authorization != nil {
			next.Authorization = facts.Authorization
		}
		next.Budget.EngineerAttempts++
		next.CandidateDigest = ""
		next.EvidenceDigest = ""
	case IssueDecisionDispatchReview:
		if current != nil {
			next.SourceSHA = current.SourceSHA
		}
		if facts.Authorization != nil {
			next.Authorization = facts.Authorization
		}
		if facts.PullRequest != nil && next.Work != nil {
			next.Work.Draft = facts.PullRequest.Draft
		}
		next.Budget.ReviewIterations++
		next.ReviewDigest = decision.ReviewDigest
		next.CandidateDigest = ""
		next.EvidenceDigest = ""
	case IssueDecisionNeedsHuman:
		if current != nil && current.Work != nil {
			next.SourceSHA = current.SourceSHA
		}
		next.Task = nil
	case IssueDecisionTakeOver:
		if facts.Authorization != nil {
			next.TakenOverBy = facts.Authorization.Actor
		}
		next.Task = nil
	case IssueDecisionCancel,
		IssueDecisionComplete,
		IssueDecisionMarkReady:
		next.Task = nil
		if decision.Kind == IssueDecisionMarkReady && next.Work != nil {
			next.Work.Draft = false
		}
	case IssueDecisionMarkDraft:
		next.Task = nil
		if next.Work != nil {
			next.Work.Draft = true
		}
	}
	if err := contract.ValidateIssueAgentState(next); err != nil {
		return contract.IssueAgentState{}, err
	}
	return next, nil
}

// AttachStatusComment binds the one App-owned status projection to state.
func AttachStatusComment(
	state contract.IssueAgentState,
	commentID int64,
	now time.Time,
) (contract.IssueAgentState, error) {
	if err := contract.ValidateIssueAgentState(state); err != nil {
		return contract.IssueAgentState{}, err
	}
	if state.StatusCommentID != 0 || commentID <= 0 ||
		now.IsZero() || now.Location() != time.UTC {
		return contract.IssueAgentState{}, errors.New(
			"status comment attachment is invalid",
		)
	}
	state.StatusCommentID = commentID
	state.UpdatedAt = now
	if err := contract.ValidateIssueAgentState(state); err != nil {
		return contract.IssueAgentState{}, err
	}
	return state, nil
}

// BuildNeedsHumanState terminates one task without publishing candidate code.
func BuildNeedsHumanState(
	current contract.IssueAgentState,
	reason string,
	now time.Time,
) (contract.IssueAgentState, error) {
	if err := contract.ValidateIssueAgentState(current); err != nil {
		return contract.IssueAgentState{}, err
	}
	if current.State != contract.IssueStateEngineering &&
		current.State != contract.IssueStateReviewing ||
		current.Task == nil ||
		reason == "" || len(reason) > 2048 ||
		now.IsZero() || now.Location() != time.UTC {
		return contract.IssueAgentState{}, errors.New(
			"needs-human state input is invalid",
		)
	}
	previousDigest, err := contract.IssueAgentStateDigest(current)
	if err != nil {
		return contract.IssueAgentState{}, err
	}
	next := current
	next.Sequence++
	next.PreviousStateDigest = previousDigest
	next.State = contract.IssueStateNeedsHuman
	next.Reason = reason
	next.Task = nil
	next.ContextDigest = ""
	next.CandidateDigest = ""
	next.EvidenceDigest = ""
	next.UpdatedAt = now
	if err := contract.ValidateIssueAgentState(next); err != nil {
		return contract.IssueAgentState{}, err
	}
	return next, nil
}

// BuildBaseSyncedState records one exact Publisher-owned mechanical rebase.
func BuildBaseSyncedState(
	current contract.IssueAgentState,
	currentMainSHA string,
	newHeadSHA string,
	issueSnapshotDigest string,
	now time.Time,
) (contract.IssueAgentState, error) {
	if err := contract.ValidateIssueAgentState(current); err != nil {
		return contract.IssueAgentState{}, err
	}
	if (current.State != contract.IssueStateDraft &&
		current.State != contract.IssueStateReadyForReview) ||
		current.Work == nil || current.Work.HeadSHA == newHeadSHA ||
		!v2SHAPattern.MatchString(currentMainSHA) ||
		!v2SHAPattern.MatchString(newHeadSHA) ||
		!v2DigestPattern.MatchString(issueSnapshotDigest) ||
		current.Budget.BaseSyncs == ^uint32(0) ||
		now.IsZero() || now.Location() != time.UTC {
		return contract.IssueAgentState{}, errors.New(
			"base-synchronized state input is invalid",
		)
	}
	previousDigest, err := contract.IssueAgentStateDigest(current)
	if err != nil {
		return contract.IssueAgentState{}, err
	}
	next := current
	next.Sequence++
	next.PreviousStateDigest = previousDigest
	next.State = contract.IssueStateReadyForReview
	next.Reason = "Agent pull request synchronized with current main and awaits fresh Review"
	next.SourceSHA = currentMainSHA
	next.IssueSnapshotDigest = issueSnapshotDigest
	next.Task = nil
	work := *current.Work
	work.HeadSHA = newHeadSHA
	work.Draft = false
	next.Work = &work
	next.Budget.BaseSyncs++
	next.ReviewDigest = ""
	next.UpdatedAt = now
	if err := contract.ValidateIssueAgentState(next); err != nil {
		return contract.IssueAgentState{}, err
	}
	return next, nil
}
