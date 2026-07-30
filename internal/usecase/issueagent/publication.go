package issueagent

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

var publicationSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CandidatePublicationInput contains all immutable inputs to one repair write.
type CandidatePublicationInput struct {
	State             contract.IssueAgentState
	Context           contract.ContextBundle
	Engineer          contract.EngineerResult
	Candidate         contract.CandidateSnapshot
	Evidence          contract.CandidateEvidence
	ExistingBranch    bool
	ExpectedParentSHA string
	BaseTreeSHA       string
}

// CandidatePublicationPlan is the exact bounded effect a Publisher may write.
type CandidatePublicationPlan struct {
	Repository        string
	IssueNumber       int64
	Branch            string
	ExpectedParentSHA string
	BaseTreeSHA       string
	ExistingBranch    bool
	CommitMessage     string
	ChangeSet         contract.ChangeSet
	PullRequestTitle  string
	PullRequestBody   string
	Authorization     contract.AuthorizationRecord
	CandidateDigest   string
	EvidenceDigest    string
}

// PlanCandidatePublication rejects stale, advisory-only, or unverified output.
func PlanCandidatePublication(
	input CandidatePublicationInput,
) (CandidatePublicationPlan, error) {
	if err := contract.ValidateIssueAgentState(input.State); err != nil {
		return CandidatePublicationPlan{}, err
	}
	if input.State.State != contract.IssueStateEngineering &&
		input.State.State != contract.IssueStateReviewing ||
		input.State.Task == nil {
		return CandidatePublicationPlan{}, errors.New(
			"candidate publication requires an active task",
		)
	}
	if err := contract.ValidateContextBundle(input.Context); err != nil {
		return CandidatePublicationPlan{}, err
	}
	if err := contract.ValidateEngineerResult(input.Engineer); err != nil {
		return CandidatePublicationPlan{}, err
	}
	if err := contract.ValidateCandidateSnapshot(input.Candidate); err != nil {
		return CandidatePublicationPlan{}, err
	}
	if err := contract.ValidateCandidateEvidence(input.Evidence); err != nil {
		return CandidatePublicationPlan{}, err
	}
	if !input.Engineer.Ready ||
		input.Engineer.Outcome != contract.EngineerOutcomeReady {
		return CandidatePublicationPlan{}, errors.New(
			"Engineer did not return a complete repair",
		)
	}
	if !input.Evidence.PublicationEligible ||
		input.Evidence.Risk != contract.CandidateRiskLow {
		return CandidatePublicationPlan{}, errors.New(
			"Verifier rejected candidate publication",
		)
	}
	if input.State.Repository != input.Context.Repository ||
		input.State.Repository != input.Engineer.Repository ||
		input.State.Repository != input.Evidence.Repository ||
		input.State.IssueNumber != input.Context.IssueNumber ||
		input.State.IssueNumber != input.Engineer.IssueNumber ||
		input.State.IssueNumber != input.Evidence.IssueNumber ||
		input.Context.Sequence > input.State.Sequence {
		return CandidatePublicationPlan{}, errors.New(
			"candidate publication identity is stale",
		)
	}
	task := *input.State.Task
	if task != input.Context.Task ||
		task.ID != input.Engineer.TaskID ||
		task.ID != input.Candidate.TaskID ||
		task.ID != input.Evidence.TaskID ||
		task.BaseSHA != input.Candidate.BaseSHA ||
		task.BaseSHA != input.Evidence.BaseSHA {
		return CandidatePublicationPlan{}, errors.New(
			"candidate publication task identity does not match",
		)
	}
	contextDigest, err := contract.ContextBundleDigest(input.Context)
	if err != nil {
		return CandidatePublicationPlan{}, err
	}
	candidateDigest, err := contract.CandidateSnapshotDigest(input.Candidate)
	if err != nil {
		return CandidatePublicationPlan{}, err
	}
	changeSetDigest, err := contract.ChangeSetDigest(input.Candidate.ChangeSet)
	if err != nil {
		return CandidatePublicationPlan{}, err
	}
	evidenceDigest, err := contract.CandidateEvidenceDigest(input.Evidence)
	if err != nil {
		return CandidatePublicationPlan{}, err
	}
	if input.State.ContextDigest != contextDigest ||
		input.State.CandidateDigest != "" &&
			input.State.CandidateDigest != candidateDigest ||
		input.State.EvidenceDigest != "" &&
			input.State.EvidenceDigest != evidenceDigest ||
		input.Evidence.CandidateDigest != candidateDigest ||
		input.Evidence.ChangeSetDigest != changeSetDigest {
		return CandidatePublicationPlan{}, errors.New(
			"candidate publication digest does not match signed state",
		)
	}
	if !publicationSHAPattern.MatchString(input.ExpectedParentSHA) ||
		!publicationSHAPattern.MatchString(input.BaseTreeSHA) {
		return CandidatePublicationPlan{}, errors.New(
			"candidate publication Git identity is invalid",
		)
	}
	branch := "agent/issue-" + strconv.FormatInt(input.State.IssueNumber, 10)
	if input.State.Work == nil {
		if input.ExistingBranch ||
			input.ExpectedParentSHA != task.BaseSHA {
			return CandidatePublicationPlan{}, errors.New(
				"new Agent branch fence is stale",
			)
		}
	} else if !input.ExistingBranch ||
		input.State.Work.Branch != branch ||
		input.State.Work.HeadSHA != input.ExpectedParentSHA {
		return CandidatePublicationPlan{}, errors.New(
			"existing Agent branch fence is stale",
		)
	}

	return CandidatePublicationPlan{
		Repository: input.State.Repository, IssueNumber: input.State.IssueNumber,
		Branch: branch, ExpectedParentSHA: input.ExpectedParentSHA,
		BaseTreeSHA: input.BaseTreeSHA, ExistingBranch: input.ExistingBranch,
		CommitMessage: "fix(agent): resolve issue #" +
			strconv.FormatInt(input.State.IssueNumber, 10),
		ChangeSet: input.Candidate.ChangeSet,
		PullRequestTitle: "fix(agent): issue #" +
			strconv.FormatInt(input.State.IssueNumber, 10),
		PullRequestBody: renderCandidatePullRequest(
			input.State.IssueNumber,
			input.Engineer,
			input.Evidence,
		),
		Authorization:   input.Context.Trusted.Authorization,
		CandidateDigest: candidateDigest,
		EvidenceDigest:  evidenceDigest,
	}, nil
}

// BuildPublishedState records one exact App commit and Draft PR.
func BuildPublishedState(
	current contract.IssueAgentState,
	commitSHA string,
	pullRequest int64,
	candidateDigest string,
	evidenceDigest string,
	now time.Time,
) (contract.IssueAgentState, error) {
	if err := contract.ValidateIssueAgentState(current); err != nil {
		return contract.IssueAgentState{}, err
	}
	if current.State != contract.IssueStateEngineering &&
		current.State != contract.IssueStateReviewing ||
		current.Task == nil ||
		!publicationSHAPattern.MatchString(commitSHA) ||
		pullRequest <= 0 ||
		!v2DigestPattern.MatchString(candidateDigest) ||
		!v2DigestPattern.MatchString(evidenceDigest) ||
		now.IsZero() || now.Location() != time.UTC {
		return contract.IssueAgentState{}, errors.New(
			"published state input is invalid",
		)
	}
	previousDigest, err := contract.IssueAgentStateDigest(current)
	if err != nil {
		return contract.IssueAgentState{}, err
	}
	next := current
	next.Sequence++
	next.PreviousStateDigest = previousDigest
	draft := true
	if current.Work != nil {
		draft = current.Work.Draft
	}
	next.State = contract.IssueStateDraft
	next.Reason = "complete low-risk repair published as a Draft PR"
	if !draft {
		next.State = contract.IssueStateReadyForReview
		next.Reason = "complete low-risk Review repair published to the ready PR"
	}
	next.Task = nil
	next.Work = &contract.IssueWork{
		Branch:  "agent/issue-" + strconv.FormatInt(current.IssueNumber, 10),
		HeadSHA: commitSHA, PullRequest: pullRequest, Draft: draft,
	}
	next.CandidateDigest = candidateDigest
	next.EvidenceDigest = evidenceDigest
	next.UpdatedAt = now
	if err := contract.ValidateIssueAgentState(next); err != nil {
		return contract.IssueAgentState{}, err
	}
	return next, nil
}

func renderCandidatePullRequest(
	issueNumber int64,
	result contract.EngineerResult,
	evidence contract.CandidateEvidence,
) string {
	risk := slices.Clone(result.ProposedRisk)
	if len(risk) == 0 {
		risk = []string{"No additional risk identified by the Engineer."}
	}
	tests := make([]string, 0, len(evidence.Commands))
	for _, command := range evidence.Commands {
		tests = append(tests, "- `"+strings.Join(command.Arguments, " ")+"`")
	}
	uncertainty := result.UnresolvedUncertainty
	if uncertainty == "" {
		uncertainty = "None reported."
	}
	return fmt.Sprintf(
		"## Root cause\n\n%s\n\n"+
			"## Causal path\n\n%s\n\n"+
			"## Change summary\n\n%s\n\n"+
			"## Trusted verification\n\n%s\n\n"+
			"## Risk\n\n- Verifier classification: `%s`\n- %s\n\n"+
			"## Unresolved uncertainty\n\n%s\n\n"+
			"Fixes #%d\n",
		result.RootCause,
		result.CausalPath,
		result.Summary,
		strings.Join(tests, "\n"),
		evidence.Risk,
		strings.Join(risk, "\n- "),
		uncertainty,
		issueNumber,
	)
}
