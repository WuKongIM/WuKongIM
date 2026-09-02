package issueagentgithub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	issueagent "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCandidatePublisherPublishesAndRecoversExactTransaction(t *testing.T) {
	t.Parallel()

	input := candidatePublicationInput(t)
	plan, err := issueagent.PlanCandidatePublication(input)
	require.NoError(t, err)
	stateStore := &candidatePublicationStateStore{
		loaded: issueagentgithub.LoadedState{
			HeadSHA: fortyHex("d"),
			State:   input.State,
		},
		found: true,
	}
	github := newCandidatePublicationGitHub(t, input, plan)
	publisher, err := issueagentgithub.NewCandidatePublisher(
		input.State.Repository,
		"wukongim-issue-agent[bot]",
		stateStore,
		github,
	)
	require.NoError(t, err)

	request := issueagentgithub.CandidatePublishRequest{
		ExpectedStateHead: stateStore.loaded.HeadSHA,
		Input:             input,
		Now:               time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	}
	first, err := publisher.Publish(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, github.publishedSHA, first.CommitSHA)
	require.Equal(t, int64(9), first.PullRequest)
	require.Equal(t, stateStore.publishedHead, first.StateHeadSHA)
	require.Equal(t, 1, github.publishCalls)
	require.Equal(t, 1, github.ensureDraftCalls)
	require.Equal(t, 1, stateStore.advanceCalls)
	require.Equal(t, 3, github.permissionCalls)
	require.NotEmpty(t, github.updatedStatusBody)
	require.Equal(t, contract.IssueStateDraft, stateStore.loaded.State.State)

	// Replaying the original request after the durable state write must verify
	// the exact branch, commit, PR, and status projection without another write.
	recovered, err := publisher.Publish(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, first, recovered)
	require.Equal(t, 1, github.publishCalls)
	require.Equal(t, 1, github.ensureDraftCalls)
	require.Equal(t, 1, stateStore.advanceCalls)
}

func TestCandidatePublisherStopsAfterPermissionDropsBeforePRWrite(t *testing.T) {
	t.Parallel()

	input := candidatePublicationInput(t)
	plan, err := issueagent.PlanCandidatePublication(input)
	require.NoError(t, err)
	stateStore := &candidatePublicationStateStore{
		loaded: issueagentgithub.LoadedState{
			HeadSHA: fortyHex("d"),
			State:   input.State,
		},
		found: true,
	}
	github := newCandidatePublicationGitHub(t, input, plan)
	github.permissions = []issueagentgithub.Permission{
		issueagentgithub.PermissionWrite,
		issueagentgithub.PermissionRead,
	}
	publisher, err := issueagentgithub.NewCandidatePublisher(
		input.State.Repository,
		"wukongim-issue-agent[bot]",
		stateStore,
		github,
	)
	require.NoError(t, err)

	_, err = publisher.Publish(context.Background(), issueagentgithub.CandidatePublishRequest{
		ExpectedStateHead: stateStore.loaded.HeadSHA,
		Input:             input,
		Now:               time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.EqualError(t, err, "Candidate Publisher authorization is no longer valid")
	require.Equal(t, 1, github.publishCalls, "the commit may exist after the first authority fence")
	require.Zero(t, github.ensureDraftCalls, "permission loss must fence the PR write")
	require.Zero(t, stateStore.advanceCalls, "permission loss must fence durable publication state")
}

func TestCandidatePublisherUpdatesExistingReviewPullRequestWithoutReplacingIt(t *testing.T) {
	t.Parallel()

	input := candidatePublicationInput(t)
	input.Context.Task.Kind = contract.TaskKindReview
	input.State.Task.Kind = contract.TaskKindReview
	input.State.State = contract.IssueStateReviewing
	input.State.ReviewDigest =
		"sha256:abababababababababababababababababababababababababababababababab"
	input.State.Work = &contract.IssueWork{
		Branch: "agent/issue-42", HeadSHA: input.ExpectedParentSHA,
		PullRequest: 9, Draft: false,
	}
	input.ExistingBranch = true
	contextDigest, err := contract.ContextBundleDigest(input.Context)
	require.NoError(t, err)
	input.State.ContextDigest = contextDigest
	plan, err := issueagent.PlanCandidatePublication(input)
	require.NoError(t, err)
	stateStore := &candidatePublicationStateStore{
		loaded: issueagentgithub.LoadedState{
			HeadSHA: fortyHex("d"), State: input.State,
		},
		found: true,
	}
	github := newCandidatePublicationGitHub(t, input, plan)
	github.initialRefExists = true
	publisher, err := issueagentgithub.NewCandidatePublisher(
		input.State.Repository, "wukongim-issue-agent[bot]",
		stateStore, github,
	)
	require.NoError(t, err)

	result, err := publisher.Publish(context.Background(),
		issueagentgithub.CandidatePublishRequest{
			ExpectedStateHead: stateStore.loaded.HeadSHA, Input: input,
			Now: time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
		})
	require.NoError(t, err)
	require.Equal(t, int64(9), result.PullRequest)
	require.Equal(t, 1, github.publishCalls)
	require.Zero(t, github.ensureDraftCalls)
	require.Equal(t, 1, github.updatePullCalls)
	require.Equal(t, contract.IssueStateReadyForReview, stateStore.loaded.State.State)
	require.False(t, stateStore.loaded.State.Work.Draft)
}

type candidatePublicationStateStore struct {
	loaded        issueagentgithub.LoadedState
	found         bool
	advanceCalls  int
	publishedHead string
}

func (store *candidatePublicationStateStore) Load(
	context.Context,
	int64,
) (issueagentgithub.LoadedState, bool, error) {
	return store.loaded, store.found, nil
}

func (store *candidatePublicationStateStore) Advance(
	_ context.Context,
	request issueagentgithub.StateAdvanceRequest,
) (issueagentgithub.StatePublication, error) {
	store.advanceCalls++
	store.publishedHead = fortyHex("e")
	store.loaded = issueagentgithub.LoadedState{
		HeadSHA: store.publishedHead,
		State:   request.State,
	}
	return issueagentgithub.StatePublication{HeadSHA: store.publishedHead}, nil
}

type candidatePublicationGitHub struct {
	t                 *testing.T
	input             issueagent.CandidatePublicationInput
	plan              issueagent.CandidatePublicationPlan
	publishedSHA      string
	published         bool
	publishCalls      int
	ensureDraftCalls  int
	permissionCalls   int
	permissions       []issueagentgithub.Permission
	pull              issueagentgithub.PullRequestFacts
	updatedStatusBody string
	initialRefExists  bool
	updatePullCalls   int
}

func newCandidatePublicationGitHub(
	t *testing.T,
	input issueagent.CandidatePublicationInput,
	plan issueagent.CandidatePublicationPlan,
) *candidatePublicationGitHub {
	t.Helper()
	return &candidatePublicationGitHub{
		t:            t,
		input:        input,
		plan:         plan,
		publishedSHA: fortyHex("b"),
		permissions:  []issueagentgithub.Permission{issueagentgithub.PermissionWrite},
	}
}

func (github *candidatePublicationGitHub) Issue(
	context.Context,
	int64,
) (issueagentgithub.IssueFacts, error) {
	issue := github.input.Context.Untrusted.Issue
	return issueagentgithub.IssueFacts{
		ID: issue.ID, Number: issue.Number, State: "open",
		Title: issue.Title, Body: issue.Body, Author: issue.Author,
		AuthorAssociation: issue.AuthorAssociation, UpdatedAt: issue.UpdatedAt,
	}, nil
}

func (github *candidatePublicationGitHub) IssueComment(
	_ context.Context,
	commentID int64,
	_ int64,
) (issueagentgithub.IssueComment, error) {
	return issueagentgithub.IssueComment{
		ID: commentID, Author: "wukongim-issue-agent[bot]", AuthorType: "Bot",
	}, nil
}

func (github *candidatePublicationGitHub) ActorPermission(
	context.Context,
	string,
) (issueagentgithub.Permission, error) {
	index := github.permissionCalls
	github.permissionCalls++
	if index >= len(github.permissions) {
		return github.permissions[len(github.permissions)-1], nil
	}
	return github.permissions[index], nil
}

func (github *candidatePublicationGitHub) RefIfExists(
	_ context.Context,
	branch string,
) (issueagentgithub.RefFacts, bool, error) {
	require.Equal(github.t, github.plan.Branch, branch)
	if !github.published {
		if github.initialRefExists {
			return issueagentgithub.RefFacts{
				Name: branch, SHA: github.plan.ExpectedParentSHA,
			}, true, nil
		}
		return issueagentgithub.RefFacts{}, false, nil
	}
	return issueagentgithub.RefFacts{Name: branch, SHA: github.publishedSHA}, true, nil
}

func (github *candidatePublicationGitHub) Commit(
	_ context.Context,
	sha string,
) (issueagentgithub.CommitFacts, error) {
	switch sha {
	case github.plan.ExpectedParentSHA:
		return issueagentgithub.CommitFacts{
			SHA: sha, TreeSHA: github.plan.BaseTreeSHA,
		}, nil
	case github.publishedSHA:
		return issueagentgithub.CommitFacts{
			SHA: sha, TreeSHA: fortyHex("c"),
			Parents: []string{github.plan.ExpectedParentSHA},
			Message: github.plan.CommitMessage, Verified: true,
			VerificationReason: "valid",
		}, nil
	case fortyHex("d"):
		return issueagentgithub.CommitFacts{SHA: sha, TreeSHA: fortyHex("f")}, nil
	default:
		return issueagentgithub.CommitFacts{}, errors.New("unexpected commit")
	}
}

func (github *candidatePublicationGitHub) CommitAttribution(
	_ context.Context,
	sha string,
) (issueagentgithub.CommitAttributionFacts, error) {
	return issueagentgithub.CommitAttributionFacts{
		SHA: sha, AuthorLogin: "wukongim-issue-agent[bot]", AuthorType: "Bot",
		SignatureValid: true, SignatureState: "VALID", WasSignedByGitHub: true,
	}, nil
}

func (github *candidatePublicationGitHub) CompareOneCommit(
	_ context.Context,
	baseSHA string,
	headSHA string,
) ([]issueagentgithub.CompareFileFacts, error) {
	require.Equal(github.t, github.plan.ExpectedParentSHA, baseSHA)
	require.Equal(github.t, github.publishedSHA, headSHA)
	result := make([]issueagentgithub.CompareFileFacts, 0, len(github.plan.ChangeSet.Files))
	for _, file := range github.plan.ChangeSet.Files {
		status := "removed"
		sha := ""
		if file.Operation == contract.FileOperationUpsert {
			status = "added"
			content, err := contract.DecodeFileContent(file)
			require.NoError(github.t, err)
			sha = testGitBlobSHA(content)
		}
		result = append(result, issueagentgithub.CompareFileFacts{
			Path: file.Path, Status: status, SHA: sha,
		})
	}
	return result, nil
}

func (github *candidatePublicationGitHub) PublishCommit(
	_ context.Context,
	plan issueagentgithub.CommitPlan,
) (issueagentgithub.PublishedCommit, error) {
	github.publishCalls++
	require.Equal(github.t, github.plan.Branch, plan.Branch)
	require.Equal(github.t, github.plan.ExpectedParentSHA, plan.ExpectedParentSHA)
	require.Equal(github.t, github.plan.ChangeSet, plan.ChangeSet)
	github.published = true
	return issueagentgithub.PublishedCommit{
		CommitSHA: github.publishedSHA,
		TreeSHA:   fortyHex("c"),
	}, nil
}

func (github *candidatePublicationGitHub) EnsureDraftPullRequest(
	_ context.Context,
	request issueagentgithub.DraftPullRequest,
) (issueagentgithub.PullRequestFacts, error) {
	github.ensureDraftCalls++
	require.Equal(github.t, github.plan.Branch, request.Head)
	require.Equal(github.t, "main", request.Base)
	github.pull = issueagentgithub.PullRequestFacts{
		Number: 9, State: "open", Draft: true,
		BaseRef: "main", BaseSHA: github.plan.ExpectedParentSHA,
		HeadRef: github.plan.Branch, HeadSHA: github.publishedSHA,
	}
	return github.pull, nil
}

func (github *candidatePublicationGitHub) PullRequest(
	context.Context,
	int64,
) (issueagentgithub.PullRequestFacts, error) {
	if github.pull.Number == 0 && github.input.State.Work != nil {
		github.pull = issueagentgithub.PullRequestFacts{
			Number: github.input.State.Work.PullRequest,
			State:  "open", Draft: github.input.State.Work.Draft,
			BaseRef: "main", BaseSHA: github.plan.ExpectedParentSHA,
			HeadRef: github.plan.Branch, HeadSHA: github.publishedSHA,
		}
	}
	return github.pull, nil
}

func (github *candidatePublicationGitHub) UpdatePullRequest(
	_ context.Context,
	number int64,
	title string,
	body string,
	state string,
) (issueagentgithub.PullRequestFacts, error) {
	github.updatePullCalls++
	require.Equal(github.t, github.plan.PullRequestTitle, title)
	require.Equal(github.t, github.plan.PullRequestBody, body)
	require.Equal(github.t, "open", state)
	pull, err := github.PullRequest(context.Background(), number)
	return pull, err
}

func (github *candidatePublicationGitHub) UpdateIssueComment(
	_ context.Context,
	_ int64,
	commentID int64,
	body string,
) (issueagentgithub.IssueComment, error) {
	github.updatedStatusBody = body
	return issueagentgithub.IssueComment{
		ID: commentID, Author: "wukongim-issue-agent[bot]", AuthorType: "Bot",
		Body: body,
	}, nil
}

func candidatePublicationInput(t *testing.T) issueagent.CandidatePublicationInput {
	t.Helper()
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	task := contract.TaskIdentity{
		ID:           "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Kind:         contract.TaskKindEngineer,
		BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
		AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
		PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	bundle := contract.ContextBundle{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 1, Task: task,
		Trusted: contract.TrustedContext{
			Authorization: contract.AuthorizationRecord{
				Actor: "maintainer", Permission: "write",
				EventID: "issue-42", Command: "/agent fix",
			},
			Labels: []string{"bug"}, RequiredTests: []string{"focused"},
			RiskCeiling: []string{"low"},
			ContextDocumentDigests: []contract.FileDigest{{
				Path: "AGENTS.md", GitBlobSHA: fortyHex("d"),
			}},
			KnowledgePaths:     []string{"docs/development/PROJECT_KNOWLEDGE.md"},
			OutputSchemaDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Limits: contract.EngineerLimits{
				WallTimeSeconds: 5400, ModifyTestIterations: 3,
			},
		},
		Untrusted: contract.UntrustedContext{
			Issue: contract.IssueSnapshot{
				ID: "I_42", Number: 42, Title: "broken", Body: "steps",
				Author: "reporter", AuthorAssociation: "NONE", UpdatedAt: now,
			},
			Comments:      []contract.CommentSnapshot{},
			ReviewThreads: []contract.ReviewThreadSnapshot{},
		},
		CreatedAt: now,
	}
	candidate := contract.CandidateSnapshot{
		SchemaVersion: 2, TaskID: task.ID, BaseSHA: task.BaseSHA,
		ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
			Path: "internal/example/fix.go", Operation: contract.FileOperationUpsert,
			Mode: contract.FileModeRegular,
			ContentBase64: contract.EncodeFileContent(
				[]byte("package example\n\nfunc fixed() {}\n"),
			),
		}}},
	}
	candidateDigest, err := contract.CandidateSnapshotDigest(candidate)
	require.NoError(t, err)
	changeDigest, err := contract.ChangeSetDigest(candidate.ChangeSet)
	require.NoError(t, err)
	evidence := contract.CandidateEvidence{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, TaskID: task.ID, BaseSHA: task.BaseSHA,
		CandidateDigest: candidateDigest, ChangeSetDigest: changeDigest,
		Risk: contract.CandidateRiskLow, PublicationEligible: true,
		RequiredSuites: []string{"focused"},
		Commands: []contract.VerificationCommand{{
			Arguments: []string{"go", "test", "./internal/example"}, WorkingDir: ".",
			ExitCode:     0,
			StdoutDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			StderrDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			DurationMS:   10,
		}},
		CreatedAt: now,
	}
	evidenceDigest, err := contract.CandidateEvidenceDigest(evidence)
	require.NoError(t, err)
	contextDigest, err := contract.ContextBundleDigest(bundle)
	require.NoError(t, err)
	state := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
		Sequence: 2, State: contract.IssueStateEngineering,
		Reason: "candidate captured", StatusCommentID: 51,
		PreviousStateDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		IssueSnapshotDigest: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		SourceSHA:           task.BaseSHA, Task: &task,
		Authorization: &bundle.Trusted.Authorization,
		ContextDigest: contextDigest, CandidateDigest: candidateDigest,
		EvidenceDigest: evidenceDigest, UpdatedAt: now,
	}
	return issueagent.CandidatePublicationInput{
		State: state, Context: bundle,
		Engineer: contract.EngineerResult{
			SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
			IssueNumber: 42, TaskID: task.ID,
			Outcome:         contract.EngineerOutcomeReady,
			ExternalSymptom: "request fails", RootCause: "wrong condition",
			CausalPath:         "request -> condition -> failure",
			EvidenceReferences: []string{"focused regression test"},
			ProposedRisk:       []string{"narrow behavior change"},
			TestsAttempted:     []string{"go test ./internal/example"},
			Summary:            "Correct the condition and add a regression test.", Ready: true,
		},
		Candidate: candidate, Evidence: evidence,
		ExpectedParentSHA: task.BaseSHA,
		BaseTreeSHA:       fortyHex("a"),
	}
}
