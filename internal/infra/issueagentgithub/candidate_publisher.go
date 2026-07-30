package issueagentgithub

import (
	"context"
	"errors"
	"slices"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagent "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

// CandidatePublicationStateStore is the signed per-Issue state boundary.
type CandidatePublicationStateStore interface {
	Load(context.Context, int64) (LoadedState, bool, error)
	Advance(context.Context, StateAdvanceRequest) (StatePublication, error)
}

// CandidatePublicationGitHub is the only GitHub write capability for repairs.
type CandidatePublicationGitHub interface {
	Issue(context.Context, int64) (IssueFacts, error)
	IssueComment(context.Context, int64, int64) (IssueComment, error)
	ActorPermission(context.Context, string) (Permission, error)
	RefIfExists(context.Context, string) (RefFacts, bool, error)
	Commit(context.Context, string) (CommitFacts, error)
	CommitAttribution(context.Context, string) (CommitAttributionFacts, error)
	CompareOneCommit(context.Context, string, string) ([]CompareFileFacts, error)
	PublishCommit(context.Context, CommitPlan) (PublishedCommit, error)
	EnsureDraftPullRequest(
		context.Context,
		DraftPullRequest,
	) (PullRequestFacts, error)
	PullRequest(context.Context, int64) (PullRequestFacts, error)
	UpdatePullRequest(
		context.Context,
		int64,
		string,
		string,
		string,
	) (PullRequestFacts, error)
	UpdateIssueComment(
		context.Context,
		int64,
		int64,
		string,
	) (IssueComment, error)
}

// CandidatePublisher owns all repair-branch, PR, state, and status writes.
type CandidatePublisher struct {
	repository string
	appLogin   string
	state      CandidatePublicationStateStore
	github     CandidatePublicationGitHub
}

// CandidatePublishRequest fences one complete publication transaction.
type CandidatePublishRequest struct {
	ExpectedStateHead string
	Input             issueagent.CandidatePublicationInput
	Now               time.Time
}

// CandidatePublication is one accepted or idempotently recovered result.
type CandidatePublication struct {
	CommitSHA    string
	PullRequest  int64
	StateHeadSHA string
}

// NewCandidatePublisher constructs the sole repair writer.
func NewCandidatePublisher(
	repository string,
	appLogin string,
	state CandidatePublicationStateStore,
	github CandidatePublicationGitHub,
) (*CandidatePublisher, error) {
	if !repositoryNamePattern.MatchString(repository) ||
		!appBotLoginPattern.MatchString(appLogin) ||
		state == nil || github == nil {
		return nil, errors.New("Candidate Publisher configuration is invalid")
	}
	return &CandidatePublisher{
		repository: repository,
		appLogin:   appLogin,
		state:      state,
		github:     github,
	}, nil
}

// Publish writes an exact App-signed repair without executing candidate code.
func (publisher *CandidatePublisher) Publish(
	ctx context.Context,
	request CandidatePublishRequest,
) (CandidatePublication, error) {
	if publisher == nil || ctx == nil ||
		!gitObjectPattern.MatchString(request.ExpectedStateHead) ||
		len(request.ExpectedStateHead) != 40 ||
		request.Now.IsZero() || request.Now.Location() != time.UTC {
		return CandidatePublication{}, errors.New(
			"Candidate Publisher request is invalid",
		)
	}
	plan, err := issueagent.PlanCandidatePublication(request.Input)
	if err != nil {
		return CandidatePublication{}, err
	}
	if plan.Repository != publisher.repository {
		return CandidatePublication{}, errors.New(
			"Candidate Publisher repository is inconsistent",
		)
	}
	loaded, recovered, err := publisher.loadPublicationState(
		ctx,
		request,
		plan,
	)
	if err != nil {
		return CandidatePublication{}, err
	}
	if recovered {
		return publisher.recoverPublished(ctx, loaded, plan)
	}
	if err := publisher.checkAuthorization(ctx, request.Input.Context); err != nil {
		return CandidatePublication{}, err
	}
	parent, err := publisher.github.Commit(ctx, plan.ExpectedParentSHA)
	if err != nil || parent.TreeSHA != plan.BaseTreeSHA {
		return CandidatePublication{}, errors.New(
			"Candidate Publisher base tree is stale",
		)
	}
	commitSHA, err := publisher.ensureCandidateCommit(ctx, plan)
	if err != nil {
		return CandidatePublication{}, err
	}

	loaded, _, err = publisher.loadPublicationState(ctx, request, plan)
	if err != nil {
		return CandidatePublication{}, err
	}
	if err := publisher.checkAuthorization(ctx, request.Input.Context); err != nil {
		return CandidatePublication{}, err
	}
	ref, exists, err := publisher.github.RefIfExists(ctx, plan.Branch)
	if err != nil || !exists || ref.SHA != commitSHA {
		return CandidatePublication{}, errors.New(
			"Candidate Publisher branch changed before PR write",
		)
	}
	pull, err := publisher.ensurePullRequest(ctx, request.Input.State, plan, commitSHA)
	if err != nil {
		return CandidatePublication{}, err
	}

	loaded, _, err = publisher.loadPublicationState(ctx, request, plan)
	if err != nil {
		return CandidatePublication{}, err
	}
	if err := publisher.checkAuthorization(ctx, request.Input.Context); err != nil {
		return CandidatePublication{}, err
	}
	ref, exists, err = publisher.github.RefIfExists(ctx, plan.Branch)
	if err != nil || !exists || ref.SHA != commitSHA {
		return CandidatePublication{}, errors.New(
			"Candidate Publisher branch changed before state write",
		)
	}
	currentPull, err := publisher.github.PullRequest(ctx, pull.Number)
	expectedDraft := request.Input.State.Work == nil ||
		request.Input.State.Work.Draft
	if err != nil || currentPull.State != "open" ||
		currentPull.Draft != expectedDraft ||
		currentPull.Merged || currentPull.HeadRef != plan.Branch ||
		currentPull.HeadSHA != commitSHA || currentPull.BaseRef != "main" {
		return CandidatePublication{}, errors.New(
			"Candidate Publisher pull request changed before state write",
		)
	}
	next, err := issueagent.BuildPublishedState(
		loaded.State,
		commitSHA,
		pull.Number,
		plan.CandidateDigest,
		plan.EvidenceDigest,
		request.Now,
	)
	if err != nil {
		return CandidatePublication{}, err
	}
	stateCommit, err := publisher.github.Commit(ctx, loaded.HeadSHA)
	if err != nil {
		return CandidatePublication{}, err
	}
	statePublication, err := publisher.state.Advance(
		ctx,
		StateAdvanceRequest{
			State: next, ExpectedParentSHA: loaded.HeadSHA,
			BaseTreeSHA: stateCommit.TreeSHA, ExistingBranch: true,
		},
	)
	if err != nil {
		return CandidatePublication{}, err
	}
	if err := publisher.repairStatus(
		ctx,
		statePublication.HeadSHA,
		next,
	); err != nil {
		return CandidatePublication{}, err
	}
	return CandidatePublication{
		CommitSHA: commitSHA, PullRequest: pull.Number,
		StateHeadSHA: statePublication.HeadSHA,
	}, nil
}

func (publisher *CandidatePublisher) loadPublicationState(
	ctx context.Context,
	request CandidatePublishRequest,
	plan issueagent.CandidatePublicationPlan,
) (LoadedState, bool, error) {
	loaded, found, err := publisher.state.Load(ctx, plan.IssueNumber)
	if err != nil || !found {
		return LoadedState{}, false, errors.New(
			"Candidate Publisher state is unavailable",
		)
	}
	if loaded.HeadSHA == request.ExpectedStateHead {
		expectedDigest, expectedErr := contract.IssueAgentStateDigest(
			request.Input.State,
		)
		actualDigest, actualErr := contract.IssueAgentStateDigest(loaded.State)
		if expectedErr != nil || actualErr != nil ||
			expectedDigest != actualDigest {
			return LoadedState{}, false, errors.New(
				"Candidate Publisher state content changed",
			)
		}
		return loaded, false, nil
	}
	if (loaded.State.State == contract.IssueStateDraft ||
		loaded.State.State == contract.IssueStateReadyForReview) &&
		loaded.State.PreviousStateDigest != "" &&
		loaded.State.CandidateDigest == plan.CandidateDigest &&
		loaded.State.EvidenceDigest == plan.EvidenceDigest &&
		loaded.State.Work != nil &&
		loaded.State.Work.Branch == plan.Branch {
		return loaded, true, nil
	}
	return LoadedState{}, false, errors.New(
		"Candidate Publisher state head is stale",
	)
}

func (publisher *CandidatePublisher) checkAuthorization(
	ctx context.Context,
	bundle contract.ContextBundle,
) error {
	issue, err := publisher.github.Issue(ctx, bundle.IssueNumber)
	if err != nil || issue.State != "open" ||
		issue.ID != bundle.Untrusted.Issue.ID ||
		issue.Title != bundle.Untrusted.Issue.Title ||
		issue.Body != bundle.Untrusted.Issue.Body ||
		issue.Author != bundle.Untrusted.Issue.Author ||
		issue.AuthorAssociation !=
			bundle.Untrusted.Issue.AuthorAssociation ||
		!issue.UpdatedAt.Equal(bundle.Untrusted.Issue.UpdatedAt) {
		return errors.New("Candidate Publisher Issue snapshot is stale")
	}
	permission, err := publisher.github.ActorPermission(
		ctx,
		bundle.Trusted.Authorization.Actor,
	)
	if err != nil {
		return errors.New("Candidate Publisher authorization re-read failed")
	}
	switch permission {
	case PermissionWrite, PermissionMaintain, PermissionAdmin:
		return nil
	default:
		return errors.New(
			"Candidate Publisher authorization is no longer valid",
		)
	}
}

func (publisher *CandidatePublisher) ensureCandidateCommit(
	ctx context.Context,
	plan issueagent.CandidatePublicationPlan,
) (string, error) {
	ref, exists, err := publisher.github.RefIfExists(ctx, plan.Branch)
	if err != nil {
		return "", err
	}
	if exists && ref.SHA != plan.ExpectedParentSHA {
		if publisher.exactCandidateCommit(ctx, plan, ref.SHA) {
			return ref.SHA, nil
		}
		return "", errors.New(
			"Candidate Publisher found an unexpected Agent branch head",
		)
	}
	if exists != plan.ExistingBranch {
		return "", errors.New(
			"Candidate Publisher Agent branch existence is stale",
		)
	}
	published, err := publisher.github.PublishCommit(ctx, CommitPlan{
		Branch: plan.Branch, ExpectedParentSHA: plan.ExpectedParentSHA,
		BaseTreeSHA: plan.BaseTreeSHA, Message: plan.CommitMessage,
		ExistingBranch: plan.ExistingBranch, ChangeSet: plan.ChangeSet,
	})
	if err != nil {
		return "", err
	}
	if !publisher.exactCandidateCommit(ctx, plan, published.CommitSHA) {
		return "", errors.New(
			"Candidate Publisher commit is not exact and App-signed",
		)
	}
	return published.CommitSHA, nil
}

func (publisher *CandidatePublisher) exactCandidateCommit(
	ctx context.Context,
	plan issueagent.CandidatePublicationPlan,
	commitSHA string,
) bool {
	commit, err := publisher.github.Commit(ctx, commitSHA)
	if err != nil {
		return false
	}
	attribution, err := publisher.github.CommitAttribution(ctx, commitSHA)
	if err != nil || !ExactAppCommit(
		commit,
		attribution,
		plan.ExpectedParentSHA,
		plan.CommitMessage,
		publisher.appLogin,
	) {
		return false
	}
	files, err := publisher.github.CompareOneCommit(
		ctx,
		plan.ExpectedParentSHA,
		commitSHA,
	)
	if err != nil || len(files) != len(plan.ChangeSet.Files) {
		return false
	}
	for index, change := range plan.ChangeSet.Files {
		if files[index].Path != change.Path {
			return false
		}
		if change.Operation == contract.FileOperationDelete {
			if files[index].Status != "removed" {
				return false
			}
			continue
		}
		content, err := contract.DecodeFileContent(change)
		if err != nil || files[index].SHA != gitBlobObjectSHA(content) {
			return false
		}
	}
	return true
}

func (publisher *CandidatePublisher) ensurePullRequest(
	ctx context.Context,
	state contract.IssueAgentState,
	plan issueagent.CandidatePublicationPlan,
	commitSHA string,
) (PullRequestFacts, error) {
	if state.Work == nil {
		return publisher.github.EnsureDraftPullRequest(
			ctx,
			DraftPullRequest{
				Title: plan.PullRequestTitle, Body: plan.PullRequestBody,
				Head: plan.Branch, Base: "main",
			},
		)
	}
	pull, err := publisher.github.PullRequest(ctx, state.Work.PullRequest)
	if err != nil || pull.State != "open" ||
		pull.Draft != state.Work.Draft || pull.Merged ||
		pull.HeadRef != plan.Branch || pull.HeadSHA != commitSHA ||
		pull.BaseRef != "main" {
		return PullRequestFacts{}, errors.New(
			"existing Agent pull request is stale",
		)
	}
	updated, err := publisher.github.UpdatePullRequest(
		ctx,
		pull.Number,
		plan.PullRequestTitle,
		plan.PullRequestBody,
		"open",
	)
	if err != nil || updated.Draft != state.Work.Draft ||
		updated.HeadSHA != commitSHA {
		return PullRequestFacts{}, errors.New(
			"updated Agent pull request is inconsistent",
		)
	}
	return updated, nil
}

func (publisher *CandidatePublisher) repairStatus(
	ctx context.Context,
	expectedStateHead string,
	state contract.IssueAgentState,
) error {
	loaded, found, err := publisher.state.Load(ctx, state.IssueNumber)
	if err != nil || !found || loaded.HeadSHA != expectedStateHead {
		return errors.New(
			"Candidate Publisher state changed before status write",
		)
	}
	if state.StatusCommentID <= 0 {
		return errors.New("Issue Agent status comment is unavailable")
	}
	comment, err := publisher.github.IssueComment(
		ctx,
		state.StatusCommentID,
		state.IssueNumber,
	)
	if err != nil || comment.Author != publisher.appLogin ||
		comment.AuthorType != "Bot" {
		return errors.New("Issue Agent status comment is not App-owned")
	}
	body, err := issueagent.RenderIssueStatus(state)
	if err != nil {
		return err
	}
	updated, err := publisher.github.UpdateIssueComment(
		ctx,
		state.IssueNumber,
		state.StatusCommentID,
		body,
	)
	if err != nil || updated.ID != state.StatusCommentID ||
		updated.Author != publisher.appLogin ||
		updated.AuthorType != "Bot" ||
		!slices.Equal([]byte(updated.Body), []byte(body)) {
		return errors.New("Issue Agent status repair is inconsistent")
	}
	return nil
}

func (publisher *CandidatePublisher) recoverPublished(
	ctx context.Context,
	loaded LoadedState,
	plan issueagent.CandidatePublicationPlan,
) (CandidatePublication, error) {
	if loaded.State.Work == nil {
		return CandidatePublication{}, errors.New(
			"recovered Candidate Publisher state lacks work",
		)
	}
	ref, exists, err := publisher.github.RefIfExists(ctx, plan.Branch)
	if err != nil || !exists || ref.SHA != loaded.State.Work.HeadSHA ||
		!publisher.exactCandidateCommit(ctx, plan, ref.SHA) {
		return CandidatePublication{}, errors.New(
			"recovered Candidate Publisher branch is inconsistent",
		)
	}
	pull, err := publisher.github.PullRequest(
		ctx,
		loaded.State.Work.PullRequest,
	)
	if err != nil || pull.State != "open" ||
		pull.Draft != loaded.State.Work.Draft ||
		pull.HeadSHA != ref.SHA || pull.HeadRef != plan.Branch {
		return CandidatePublication{}, errors.New(
			"recovered Candidate Publisher PR is inconsistent",
		)
	}
	if err := publisher.repairStatus(ctx, loaded.HeadSHA, loaded.State); err != nil {
		return CandidatePublication{}, err
	}
	return CandidatePublication{
		CommitSHA: ref.SHA, PullRequest: pull.Number,
		StateHeadSHA: loaded.HeadSHA,
	}, nil
}
