package issueagentgithub

import (
	"context"
	"errors"
	"regexp"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagent "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

var candidateBaseSyncDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

const maxCandidateCommitChain = 16

// CandidateBaseSyncRejection marks a deterministic safety refusal that the
// Controller must surface as signed needs_human feedback instead of retrying.
type CandidateBaseSyncRejection interface {
	error
	CandidateBaseSyncRejected()
}

type candidateBaseSyncRejection struct {
	reason string
}

func (rejection *candidateBaseSyncRejection) Error() string {
	if rejection == nil {
		return "Candidate base synchronization was rejected"
	}
	return rejection.reason
}

func (*candidateBaseSyncRejection) CandidateBaseSyncRejected() {}

func rejectCandidateBaseSync(reason string) error {
	return &candidateBaseSyncRejection{reason: reason}
}

// CandidateBaseSyncStateStore is the signed state boundary used by a
// mechanical base synchronization.
type CandidateBaseSyncStateStore interface {
	Load(context.Context, int64) (LoadedState, bool, error)
	Advance(context.Context, StateAdvanceRequest) (StatePublication, error)
}

// CandidateBaseSyncGitHub contains only the GitHub capabilities required to
// rebuild one exact App-owned candidate on current main.
type CandidateBaseSyncGitHub interface {
	PullRequest(context.Context, int64) (PullRequestFacts, error)
	Commit(context.Context, string) (CommitFacts, error)
	CommitAttribution(context.Context, string) (CommitAttributionFacts, error)
	CompareCandidate(context.Context, string, string, int) ([]CompareFileFacts, error)
	ResolveTreePath(context.Context, string, string) (TreeEntryFacts, bool, error)
	ReadGitBlob(context.Context, string, int64) ([]byte, error)
	BuildResultTree(context.Context, string, contract.ChangeSet) (string, error)
	PublishRebasedCommit(context.Context, RebasePlan) (PublishedCommit, error)
}

// CandidateBaseSynchronizer owns exact stale-base recovery for Agent PRs.
type CandidateBaseSynchronizer struct {
	repository string
	appLogin   string
	state      CandidateBaseSyncStateStore
	github     CandidateBaseSyncGitHub
}

// CandidateBaseSyncRequest fences one mechanical rebase transaction.
type CandidateBaseSyncRequest struct {
	IssueNumber         int64
	ExpectedStateHead   string
	CurrentMainSHA      string
	IssueSnapshotDigest string
	Now                 time.Time
}

// CandidateBaseSyncResult is the exact synchronized branch and state identity.
type CandidateBaseSyncResult struct {
	HeadSHA      string
	StateHeadSHA string
	State        contract.IssueAgentState
}

// NewCandidateBaseSynchronizer constructs the sole stale-base writer.
func NewCandidateBaseSynchronizer(
	repository string,
	appLogin string,
	state CandidateBaseSyncStateStore,
	github CandidateBaseSyncGitHub,
) (*CandidateBaseSynchronizer, error) {
	if !repositoryNamePattern.MatchString(repository) ||
		!appBotLoginPattern.MatchString(appLogin) || state == nil || github == nil {
		return nil, errors.New("Candidate base synchronizer configuration is invalid")
	}
	return &CandidateBaseSynchronizer{
		repository: repository, appLogin: appLogin, state: state, github: github,
	}, nil
}

// Synchronize recreates the exact App-owned candidate on current main, swaps
// the PR ref through CAS, then records the new head in signed state.
func (synchronizer *CandidateBaseSynchronizer) Synchronize(
	ctx context.Context,
	request CandidateBaseSyncRequest,
) (CandidateBaseSyncResult, error) {
	if synchronizer == nil || ctx == nil ||
		request.IssueNumber <= 0 ||
		!gitObjectPattern.MatchString(request.ExpectedStateHead) ||
		len(request.ExpectedStateHead) != 40 ||
		!gitObjectPattern.MatchString(request.CurrentMainSHA) ||
		len(request.CurrentMainSHA) != 40 ||
		!candidateBaseSyncDigestPattern.MatchString(request.IssueSnapshotDigest) ||
		request.Now.IsZero() || request.Now.Location() != time.UTC {
		return CandidateBaseSyncResult{}, errors.New(
			"Candidate base synchronization request is invalid",
		)
	}
	loaded, found, err := synchronizer.state.Load(ctx, request.IssueNumber)
	if err != nil || !found {
		return CandidateBaseSyncResult{}, errors.New("Candidate base synchronization state is unavailable")
	}
	if loaded.HeadSHA != request.ExpectedStateHead ||
		loaded.State.Repository != synchronizer.repository ||
		loaded.State.IssueNumber != request.IssueNumber ||
		loaded.State.Work == nil {
		return CandidateBaseSyncResult{}, errors.New("Candidate base synchronization state is stale")
	}
	state := loaded.State
	if state.State != contract.IssueStateDraft &&
		state.State != contract.IssueStateReadyForReview {
		return CandidateBaseSyncResult{}, errors.New(
			"Candidate base synchronization state is not reviewable",
		)
	}
	pull, err := synchronizer.github.PullRequest(ctx, state.Work.PullRequest)
	if err != nil || pull.Number != state.Work.PullRequest ||
		pull.State != "open" || pull.Draft || pull.Merged ||
		pull.BaseRef != "main" || pull.HeadRef != state.Work.Branch {
		return CandidateBaseSyncResult{}, errors.New("Candidate base synchronization PR is stale")
	}
	var published PublishedCommit
	synchronizedSourceSHA := request.CurrentMainSHA
	if pull.HeadSHA == state.Work.HeadSHA {
		plan, planErr := synchronizer.planRebase(
			ctx, state, request.CurrentMainSHA,
		)
		if planErr != nil {
			return CandidateBaseSyncResult{}, planErr
		}
		published, err = synchronizer.github.PublishRebasedCommit(ctx, plan)
		if err != nil || published.TreeSHA != plan.ExpectedResultTreeSHA {
			return CandidateBaseSyncResult{}, errors.New("Candidate base synchronization ref swap failed")
		}
	} else {
		published, synchronizedSourceSHA, err = synchronizer.recoverInterruptedHead(
			ctx, state, pull.HeadSHA,
		)
		if err != nil {
			return CandidateBaseSyncResult{}, err
		}
	}
	pull, err = synchronizer.github.PullRequest(ctx, state.Work.PullRequest)
	if err != nil || pull.State != "open" || pull.Draft || pull.Merged ||
		pull.BaseRef != "main" || pull.HeadRef != state.Work.Branch ||
		pull.HeadSHA != published.CommitSHA {
		return CandidateBaseSyncResult{}, errors.New("Candidate base synchronization PR did not adopt exact head")
	}
	next, err := issueagent.BuildBaseSyncedState(
		state, synchronizedSourceSHA, published.CommitSHA,
		request.IssueSnapshotDigest, request.Now,
	)
	if err != nil {
		return CandidateBaseSyncResult{}, err
	}
	stateCommit, err := synchronizer.github.Commit(ctx, loaded.HeadSHA)
	if err != nil {
		return CandidateBaseSyncResult{}, err
	}
	publication, err := synchronizer.state.Advance(ctx, StateAdvanceRequest{
		State: next, ExpectedParentSHA: loaded.HeadSHA,
		BaseTreeSHA: stateCommit.TreeSHA, ExistingBranch: true,
	})
	if err != nil {
		return CandidateBaseSyncResult{}, err
	}
	return CandidateBaseSyncResult{
		HeadSHA: published.CommitSHA, StateHeadSHA: publication.HeadSHA,
		State: next,
	}, nil
}

func (synchronizer *CandidateBaseSynchronizer) recoverInterruptedHead(
	ctx context.Context,
	state contract.IssueAgentState,
	headSHA string,
) (PublishedCommit, string, error) {
	commit, err := synchronizer.github.Commit(ctx, headSHA)
	if err != nil {
		return PublishedCommit{}, "", err
	}
	attribution, err := synchronizer.github.CommitAttribution(ctx, headSHA)
	if err != nil {
		return PublishedCommit{}, "", err
	}
	message := issueagent.CandidateCommitMessage(state.IssueNumber)
	if len(commit.Parents) != 1 || !ExactAppCommit(
		commit, attribution, commit.Parents[0], message, synchronizer.appLogin,
	) {
		return PublishedCommit{}, "", rejectCandidateBaseSync(
			"Agent pull request head changed outside the Publisher",
		)
	}
	recoveredSourceSHA := commit.Parents[0]
	plan, err := synchronizer.planRebase(ctx, state, recoveredSourceSHA)
	if err != nil {
		return PublishedCommit{}, "", err
	}
	if !ExactRebasedIntegration(
		commit, attribution, recoveredSourceSHA,
		plan.ExpectedResultTreeSHA, message, synchronizer.appLogin,
	) {
		return PublishedCommit{}, "", rejectCandidateBaseSync(
			"Agent pull request head changed outside the Publisher",
		)
	}
	return PublishedCommit{
		CommitSHA: headSHA, TreeSHA: commit.TreeSHA,
	}, recoveredSourceSHA, nil
}

func (synchronizer *CandidateBaseSynchronizer) planRebase(
	ctx context.Context,
	state contract.IssueAgentState,
	currentMainSHA string,
) (RebasePlan, error) {
	oldHead := state.Work.HeadSHA
	message := issueagent.CandidateCommitMessage(state.IssueNumber)
	candidate, commitCount, err := synchronizer.verifyCandidateChain(
		ctx, state, message,
	)
	if err != nil {
		return RebasePlan{}, err
	}
	oldBase, err := synchronizer.github.Commit(ctx, state.SourceSHA)
	if err != nil {
		return RebasePlan{}, err
	}
	currentMain, err := synchronizer.github.Commit(ctx, currentMainSHA)
	if err != nil {
		return RebasePlan{}, err
	}
	if currentMain.SHA != currentMainSHA {
		return RebasePlan{}, errors.New("current main commit is unavailable")
	}
	changes, err := synchronizer.github.CompareCandidate(
		ctx, state.SourceSHA, oldHead, commitCount,
	)
	if err != nil {
		var rejection CandidateComparisonRejection
		if errors.As(err, &rejection) {
			return RebasePlan{}, rejectCandidateBaseSync(rejection.Error())
		}
		return RebasePlan{}, err
	}
	changeSet := contract.ChangeSet{Files: make(
		[]contract.FileChange, 0, len(changes),
	)}
	for _, change := range changes {
		oldEntry, oldFound, resolveErr := synchronizer.github.ResolveTreePath(
			ctx, oldBase.TreeSHA, change.Path,
		)
		if resolveErr != nil {
			return RebasePlan{}, resolveErr
		}
		mainEntry, mainFound, resolveErr := synchronizer.github.ResolveTreePath(
			ctx, currentMain.TreeSHA, change.Path,
		)
		if resolveErr != nil {
			return RebasePlan{}, resolveErr
		}
		if !sameTreeEntry(
			oldEntry, oldFound, mainEntry, mainFound,
		) {
			return RebasePlan{}, rejectCandidateBaseSync(
				"current main changed a candidate path",
			)
		}
		candidateEntry, candidateFound, resolveErr := synchronizer.github.ResolveTreePath(
			ctx, candidate.TreeSHA, change.Path,
		)
		if resolveErr != nil {
			return RebasePlan{}, resolveErr
		}
		switch change.Status {
		case "removed":
			if !oldFound || oldEntry.Type != "blob" || candidateFound {
				return RebasePlan{}, rejectCandidateBaseSync(
					"candidate deletion is inconsistent",
				)
			}
			changeSet.Files = append(changeSet.Files, contract.FileChange{
				Path: change.Path, Operation: contract.FileOperationDelete,
			})
		case "added", "modified":
			if change.Status == "added" && oldFound ||
				change.Status == "modified" && !oldFound ||
				!candidateFound || candidateEntry.Type != "blob" ||
				candidateEntry.SHA != change.SHA ||
				(candidateEntry.Mode != "100644" && candidateEntry.Mode != "100755") {
				return RebasePlan{}, rejectCandidateBaseSync(
					"candidate file is inconsistent",
				)
			}
			content, readErr := synchronizer.github.ReadGitBlob(
				ctx, candidateEntry.SHA, 8<<20,
			)
			if readErr != nil {
				return RebasePlan{}, readErr
			}
			changeSet.Files = append(changeSet.Files, contract.FileChange{
				Path: change.Path, Operation: contract.FileOperationUpsert,
				Mode:          contract.FileMode(candidateEntry.Mode),
				ContentBase64: contract.EncodeFileContent(content),
			})
		default:
			return RebasePlan{}, rejectCandidateBaseSync(
				"candidate comparison is unsupported",
			)
		}
	}
	if err := contract.ValidateChangeSet(
		changeSet, contract.PublisherChangeSetLimits(),
	); err != nil {
		return RebasePlan{}, rejectCandidateBaseSync(err.Error())
	}
	resultTree, err := synchronizer.github.BuildResultTree(
		ctx, currentMain.TreeSHA, changeSet,
	)
	if err != nil {
		return RebasePlan{}, err
	}
	return RebasePlan{
		Branch: state.Work.Branch, ExpectedOldHeadSHA: oldHead,
		CurrentMainSHA: currentMainSHA, ExpectedResultTreeSHA: resultTree,
		Message: message, ExpectedAuthorLogin: synchronizer.appLogin,
		ChangeSet: changeSet,
	}, nil
}

func (synchronizer *CandidateBaseSynchronizer) verifyCandidateChain(
	ctx context.Context,
	state contract.IssueAgentState,
	message string,
) (CommitFacts, int, error) {
	maxCommits := int(state.Budget.ReviewIterations) + 1
	if maxCommits <= 0 || maxCommits > maxCandidateCommitChain {
		return CommitFacts{}, 0, rejectCandidateBaseSync(
			"Agent candidate commit chain exceeds its signed Review budget",
		)
	}
	sha := state.Work.HeadSHA
	var head CommitFacts
	for count := 1; count <= maxCommits; count++ {
		commit, err := synchronizer.github.Commit(ctx, sha)
		if err != nil {
			return CommitFacts{}, 0, err
		}
		if count == 1 {
			head = commit
		}
		if len(commit.Parents) != 1 {
			return CommitFacts{}, 0, rejectCandidateBaseSync(
				"Agent candidate history is not one linear commit chain",
			)
		}
		attribution, err := synchronizer.github.CommitAttribution(ctx, sha)
		if err != nil {
			return CommitFacts{}, 0, err
		}
		if !ExactAppCommit(
			commit, attribution, commit.Parents[0], message,
			synchronizer.appLogin,
		) {
			return CommitFacts{}, 0, rejectCandidateBaseSync(
				"Agent candidate history is not exact and App-signed",
			)
		}
		if commit.Parents[0] == state.SourceSHA {
			return head, count, nil
		}
		sha = commit.Parents[0]
	}
	return CommitFacts{}, 0, rejectCandidateBaseSync(
		"Agent candidate history does not reach its signed source",
	)
}

func sameTreeEntry(
	left TreeEntryFacts,
	leftFound bool,
	right TreeEntryFacts,
	rightFound bool,
) bool {
	if leftFound != rightFound {
		return false
	}
	return !leftFound || left.Type == right.Type && left.Mode == right.Mode &&
		left.SHA == right.SHA
}
