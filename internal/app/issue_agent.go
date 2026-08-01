package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	issueagentverify "github.com/WuKongIM/WuKongIM/internal/runtime/issueagentverify"
	issueagent "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

const issueAgentStatusMarker = "<!-- wukongim-issue-agent-status -->"
const issueAgentTrackingLabel = "ready-for-agent"
const issueAgentBranchPrefix = "agent/issue-"
const issueAgentPRSignalWorkflow = "Safety Automation - Issue Agent PR Signal"
const issueAuthorPermissionRecoveryAttempts = 6
const issueAuthorPermissionRecoveryWindow = 3100 * time.Millisecond

var (
	affectedCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	affectedTagPattern    = regexp.MustCompile(
		`^v[0-9]+[.][0-9]+[.][0-9]+(?:[-+][A-Za-z0-9_.-]+)?$`,
	)
)

// IssueAgentConfig contains only composition and credential inputs.
type IssueAgentConfig struct {
	HTTPClient          *http.Client
	APIBaseURL          string
	Repository          string
	GitHubToken         string
	AppLogin            string
	AppID               int64
	AppInstallationID   int64
	RepositoryID        int64
	AppPrivateKeyPEM    []byte
	ReviewAgentAppLogin string
	WorkingDirectory    string
	Now                 func() time.Time
}

type issueAgentPolicy struct {
	SchemaVersion        int    `json:"schema_version"`
	Enabled              bool   `json:"enabled"`
	RolloutMode          string `json:"rollout_mode"`
	DefaultBranch        string `json:"default_branch"`
	PublisherEnvironment string `json:"publisher_environment"`
	Engineer             struct {
		ActionSHA            string `json:"action_sha"`
		CodexVersion         string `json:"codex_version"`
		Model                string `json:"model"`
		ReasoningEffort      string `json:"reasoning_effort"`
		Sandbox              string `json:"sandbox"`
		NetworkAccess        bool   `json:"network_access"`
		Ephemeral            bool   `json:"ephemeral"`
		WallTimeSeconds      uint64 `json:"wall_time_seconds"`
		ModifyTestIterations uint32 `json:"modify_test_iterations"`
	} `json:"engineer"`
	Budgets struct {
		MaxEngineerAttempts   uint32 `json:"max_engineer_attempts_per_issue"`
		MaxReviewIterations   uint32 `json:"max_review_iterations"`
		TaskStaleAfterSeconds uint64 `json:"task_stale_after_seconds"`
	} `json:"budgets"`
	CandidateLimits issueagentverify.CaptureLimits             `json:"candidate_limits"`
	ProtectedPaths  []string                                   `json:"protected_paths"`
	HighRiskPaths   []string                                   `json:"high_risk_paths"`
	HighRiskTopics  []string                                   `json:"high_risk_topics"`
	RequiredSuites  []string                                   `json:"required_suites"`
	Verification    []issueagentverify.VerificationCommandPlan `json:"verification_commands"`
	KnowledgePaths  []string                                   `json:"knowledge_paths"`
}

type reconcileResult struct {
	Dispatch     bool               `json:"dispatch"`
	Repository   string             `json:"repository"`
	IssueNumber  int64              `json:"issue_number"`
	TaskID       string             `json:"task_id"`
	BaseSHA      string             `json:"base_sha"`
	ControlSHA   string             `json:"control_sha"`
	StateHeadSHA string             `json:"state_head_sha"`
	State        string             `json:"state"`
	Reason       string             `json:"reason"`
	Warnings     []reconcileWarning `json:"warnings,omitempty"`
}

type reconcileWarning struct {
	Projection string `json:"projection"`
	Reason     string `json:"reason"`
}

type committedIssueProjection func() error

type issueAgentPullRequest struct {
	Number int64 `json:"number"`
	Head   struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

type issueAgentEvent struct {
	Issue struct {
		Number int64 `json:"number"`
	} `json:"issue"`
	PullRequest issueAgentPullRequest `json:"pull_request"`
	Review      struct {
		ID int64 `json:"id"`
	} `json:"review"`
	Comment struct {
		ID int64 `json:"id"`
	} `json:"comment"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	WorkflowRun struct {
		ID             int64                   `json:"id"`
		Name           string                  `json:"name"`
		Event          string                  `json:"event"`
		Conclusion     string                  `json:"conclusion"`
		HeadBranch     string                  `json:"head_branch"`
		PullRequests   []issueAgentPullRequest `json:"pull_requests"`
		HeadRepository struct {
			FullName string `json:"full_name"`
		} `json:"head_repository"`
		Actor struct {
			Login string `json:"login"`
		} `json:"actor"`
	} `json:"workflow_run"`
}

// NewIssueAgentOperations composes the direct v2 command surface.
func NewIssueAgentOperations(config IssueAgentConfig) issueagentcli.Operations {
	return issueagentcli.Operations{
		ReconcileGitHub: func(
			ctx context.Context,
			request issueagentcli.ReconcileGitHubRequest,
		) (any, error) {
			return reconcileGitHub(ctx, config, request)
		},
		RecoverTask: func(
			ctx context.Context,
			request issueagentcli.RecoverTaskRequest,
		) (any, error) {
			return recoverIssueTask(ctx, config, request)
		},
		BuildContext: func(
			ctx context.Context,
			request issueagentcli.BuildContextRequest,
		) (any, error) {
			return buildIssueContext(ctx, config, request)
		},
		CaptureCandidate: func(
			_ context.Context,
			request issueagentcli.CaptureCandidateRequest,
		) (any, error) {
			return issueagentverify.CaptureCandidate(
				request.Baseline,
				request.Workspace,
				request.TaskID,
				request.BaseSHA,
				request.Limits,
			)
		},
		VerifyCandidate: func(
			ctx context.Context,
			request issueagentcli.VerifyCandidateRequest,
		) (any, error) {
			runner, err := issueagentverify.NewProcessRunner(
				request.Checkout,
				request.TemporaryRoot,
				1<<20,
			)
			if err != nil {
				return nil, err
			}
			return issueagentverify.VerifyCandidate(
				ctx,
				request.Checkout,
				request.Snapshot,
				request.Policy,
				runner,
				request.Now,
			)
		},
		MintAppToken: func(
			ctx context.Context,
			request issueagentcli.MintAppTokenRequest,
		) (any, error) {
			if request.Repository != config.Repository {
				return nil, errors.New("App token repository is invalid")
			}
			return mintIssueAgentToken(ctx, config)
		},
		PublishCandidate: func(
			ctx context.Context,
			request issueagentcli.PublishCandidateRequest,
		) (any, error) {
			return publishIssueCandidate(ctx, config, request)
		},
	}
}

func reconcileGitHub(
	ctx context.Context,
	config IssueAgentConfig,
	request issueagentcli.ReconcileGitHubRequest,
) (reconcileResult, error) {
	if err := validateCompositionConfig(config, request.Repository); err != nil {
		return reconcileResult{}, err
	}
	if request.Now.IsZero() || request.Now.Location() != time.UTC {
		return reconcileResult{}, errors.New("reconcile time must use UTC")
	}
	client, err := issueAgentClient(ctx, config, true)
	if err != nil {
		return reconcileResult{}, err
	}
	issueNumber, err := resolveIssueNumber(
		ctx,
		client,
		request,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	if issueNumber == 0 {
		return reconcileResult{
			Dispatch: false, Repository: request.Repository,
			Reason: "no bounded Issue candidate",
		}, nil
	}
	policy, policyDigest, err := loadIssueAgentPolicy(config)
	if err != nil {
		return reconcileResult{}, err
	}
	issue, err := client.Issue(ctx, issueNumber)
	if err != nil {
		return reconcileResult{}, err
	}
	comments, err := client.ListIssueComments(ctx, issueNumber)
	if err != nil {
		return reconcileResult{}, err
	}
	main, err := client.DefaultBranchHead(ctx, policy.DefaultBranch)
	if err != nil {
		return reconcileResult{}, err
	}
	if request.ControlSHA != main.SHA {
		return reconcileResult{}, errors.New(
			"Controller source does not match current protected main",
		)
	}
	affectedSHA, missingVersion, err := resolveAffectedSource(
		ctx,
		client,
		issue.Body,
		main.SHA,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	stateStore, err := issueAgentStateStore(config, client)
	if err != nil {
		return reconcileResult{}, err
	}
	loaded, found, err := stateStore.Load(ctx, issueNumber)
	if err != nil {
		return reconcileResult{}, err
	}
	var current *contract.IssueAgentState
	if found {
		current = &loaded.State
	}
	authorization, authorPermission, err := currentAuthorization(
		ctx,
		client,
		issue,
		comments,
		current,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	snapshotDigest, err := digestJSON(struct {
		Issue    issueagentgithub.IssueFacts
		Comments []issueagentgithub.IssueComment
	}{Issue: issue, Comments: comments})
	if err != nil {
		return reconcileResult{}, err
	}
	informationComplete, missingInformation := issueagent.AssessBugIssue(
		issue.Title,
		issue.Body,
		issue.Labels,
		missingVersion,
	)
	facts := issueagent.IssueSnapshotFacts{
		Repository: request.Repository, IssueNumber: issueNumber,
		Open: issue.State == "open", AuthorAssociation: issue.AuthorAssociation,
		AuthorPermission:    authorPermission,
		IssueSnapshotDigest: snapshotDigest,
		SourceSHA:           main.SHA, AffectedSHA: affectedSHA,
		InformationComplete: informationComplete,
		MissingInformation:  missingInformation,
		Risk: issueagent.ClassifyIssueRisk(
			issue.Title,
			issue.Body,
			issue.Labels,
			policy.HighRiskTopics,
		),
		Authorization: authorization,
	}
	if current != nil && current.Work != nil {
		pull, pullErr := client.PullRequest(
			ctx,
			current.Work.PullRequest,
		)
		if pullErr != nil {
			return reconcileResult{}, pullErr
		}
		facts.PullRequest = &issueagent.PullRequestFacts{
			Number: pull.Number, HeadSHA: pull.HeadSHA,
			Open: pull.State == "open", Draft: pull.Draft, Merged: pull.Merged,
		}
	}
	reviewAuthorization, reviewDigest, err := currentReviewAuthorization(
		ctx,
		client,
		request,
		current,
		config.ReviewAgentAppLogin,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	if reviewAuthorization != nil &&
		(authorization == nil ||
			authorization.Command == "" ||
			authorization.Command == "/agent fix" ||
			authorization.Command == "/agent retry") {
		authorization = reviewAuthorization
		facts.Authorization = reviewAuthorization
		facts.ReviewDigest = reviewDigest
	}
	engineerPromptDigest, err := digestFile(
		filepath.Join(
			config.WorkingDirectory,
			".github/issue-agent/prompts/engineer.md",
		),
	)
	if err != nil {
		return reconcileResult{}, err
	}
	reviewPromptDigest, err := digestFile(
		filepath.Join(
			config.WorkingDirectory,
			".github/issue-agent/prompts/review.md",
		),
	)
	if err != nil {
		return reconcileResult{}, err
	}
	decision, err := issueagent.ReconcileIssue(
		facts,
		current,
		issueagent.ReconcileIssuePolicy{
			Enabled: policy.Enabled, PolicyDigest: policyDigest,
			EngineerPromptDigest: engineerPromptDigest,
			ReviewPromptDigest:   reviewPromptDigest,
			MaxEngineerAttempts:  policy.Budgets.MaxEngineerAttempts,
			MaxReviewIterations:  policy.Budgets.MaxReviewIterations,
			TaskStaleAfter: time.Duration(
				policy.Budgets.TaskStaleAfterSeconds,
			) * time.Second,
		},
		request.Now,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	trackIssue := issueagent.TracksIssueState(decision.NextState)
	currentlyActive := current != nil &&
		issueagent.TracksIssueState(current.State)
	if trackIssue || currentlyActive {
		var labelsChanged bool
		issue, labelsChanged, err = setIssueAgentTracking(
			ctx,
			client,
			issue,
			true,
		)
		if err != nil {
			return reconcileResult{}, err
		}
		if labelsChanged {
			facts.IssueSnapshotDigest, err = digestJSON(struct {
				Issue    issueagentgithub.IssueFacts
				Comments []issueagentgithub.IssueComment
			}{Issue: issue, Comments: comments})
			if err != nil {
				return reconcileResult{}, err
			}
			facts.Risk = issueagent.ClassifyIssueRisk(
				issue.Title,
				issue.Body,
				issue.Labels,
				policy.HighRiskTopics,
			)
		}
	}
	if decision.Kind == issueagent.IssueDecisionWait && current != nil {
		if err := repairIssueStatus(
			ctx,
			client,
			config.AppLogin,
			*current,
		); err != nil {
			return reconcileResult{}, err
		}
		if !trackIssue {
			if _, _, err := setIssueAgentTracking(
				ctx,
				client,
				issue,
				false,
			); err != nil {
				return reconcileResult{}, err
			}
		}
		return reconcileResult{
			Repository: request.Repository, IssueNumber: issueNumber,
			StateHeadSHA: loaded.HeadSHA, State: string(current.State),
			Reason: decision.Reason,
		}, nil
	}
	next, err := issueagent.BuildIssueState(
		current,
		facts,
		decision,
		request.Now,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	statusID, err := ensureIssueStatus(
		ctx,
		client,
		config.AppLogin,
		comments,
		next,
	)
	if err != nil {
		return reconcileResult{}, err
	}
	if next.StatusCommentID == 0 {
		next, err = issueagent.AttachStatusComment(
			next,
			statusID,
			request.Now,
		)
		if err != nil {
			return reconcileResult{}, err
		}
	}
	if decision.Task != nil {
		bundle, bundleErr := buildContextForState(
			ctx,
			config,
			client,
			policy,
			next,
		)
		if bundleErr != nil {
			return reconcileResult{}, bundleErr
		}
		next.ContextDigest, err = contract.ContextBundleDigest(bundle)
		if err != nil {
			return reconcileResult{}, err
		}
	}
	expectedParent := main.SHA
	existingBranch := false
	if found {
		expectedParent = loaded.HeadSHA
		existingBranch = true
	}
	parent, err := client.Commit(ctx, expectedParent)
	if err != nil {
		return reconcileResult{}, err
	}
	publication, err := stateStore.Advance(
		ctx,
		issueagentgithub.StateAdvanceRequest{
			State: next, ExpectedParentSHA: expectedParent,
			BaseTreeSHA: parent.TreeSHA, ExistingBranch: existingBranch,
		},
	)
	if err != nil {
		return reconcileResult{}, err
	}
	result := reconcileResult{
		Dispatch: decision.Kind == issueagent.IssueDecisionDispatchEngineer ||
			decision.Kind == issueagent.IssueDecisionDispatchReview,
		Repository: request.Repository, IssueNumber: issueNumber,
		ControlSHA: next.SourceSHA, StateHeadSHA: publication.HeadSHA,
		State:  string(next.State),
		Reason: decision.Reason,
	}
	if next.Task != nil {
		result.TaskID = next.Task.ID
		result.BaseSHA = next.Task.BaseSHA
	}
	statusProjection := func() error {
		return repairIssueStatus(
			ctx,
			client,
			config.AppLogin,
			next,
		)
	}
	var trackingProjection committedIssueProjection
	if !trackIssue {
		trackingProjection = func() error {
			_, _, projectionErr := setIssueAgentTracking(
				ctx,
				client,
				issue,
				false,
			)
			return projectionErr
		}
	}
	return finalizeCommittedReconcile(
		result,
		statusProjection,
		trackingProjection,
	), nil
}

// finalizeCommittedReconcile preserves the authoritative signed transition
// while retaining the sweep label until its ordered GitHub projections succeed.
func finalizeCommittedReconcile(
	result reconcileResult,
	statusProjection committedIssueProjection,
	trackingProjection committedIssueProjection,
) reconcileResult {
	if err := statusProjection(); err != nil {
		result.Warnings = append(result.Warnings, reconcileWarning{
			Projection: "status",
			Reason:     err.Error(),
		})
		return result
	}
	if trackingProjection != nil {
		if err := trackingProjection(); err != nil {
			result.Warnings = append(result.Warnings, reconcileWarning{
				Projection: "tracking",
				Reason:     err.Error(),
			})
		}
	}
	return result
}

func recoverIssueTask(
	ctx context.Context,
	config IssueAgentConfig,
	request issueagentcli.RecoverTaskRequest,
) (any, error) {
	if err := validateCompositionConfig(config, request.Repository); err != nil {
		return nil, err
	}
	client, err := issueAgentClient(ctx, config, false)
	if err != nil {
		return nil, err
	}
	store, err := issueAgentStateStore(config, client)
	if err != nil {
		return nil, err
	}
	loaded, found, err := store.Load(ctx, request.IssueNumber)
	if err != nil || !found || loaded.HeadSHA != request.StateHeadSHA ||
		loaded.State.Task == nil ||
		loaded.State.Task.ID != request.TaskID ||
		loaded.State.Task.BaseSHA != request.BaseSHA {
		return nil, errors.New("signed Issue task does not match workflow input")
	}
	if loaded.State.SourceSHA != request.ControlSHA {
		return nil, errors.New("signed Issue task control source is stale")
	}
	return map[string]any{
		"valid": true, "state": loaded.State.State,
		"sequence": loaded.State.Sequence,
	}, nil
}

func buildIssueContext(
	ctx context.Context,
	config IssueAgentConfig,
	request issueagentcli.BuildContextRequest,
) (contract.ContextBundle, error) {
	if err := validateCompositionConfig(config, request.Repository); err != nil {
		return contract.ContextBundle{}, err
	}
	client, err := issueAgentClient(ctx, config, false)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	store, err := issueAgentStateStore(config, client)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	loaded, found, err := store.Load(ctx, request.IssueNumber)
	if err != nil || !found || loaded.HeadSHA != request.StateHeadSHA ||
		loaded.State.Task == nil ||
		loaded.State.Task.ID != request.TaskID ||
		loaded.State.SourceSHA != request.ControlSHA {
		return contract.ContextBundle{}, errors.New(
			"Context Builder task state is stale",
		)
	}
	policy, _, err := loadIssueAgentPolicy(config)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	bundle, err := buildContextForState(
		ctx,
		config,
		client,
		policy,
		loaded.State,
	)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	digest, err := contract.ContextBundleDigest(bundle)
	if err != nil || digest != loaded.State.ContextDigest {
		return contract.ContextBundle{}, errors.New(
			"Context Bundle does not match signed state",
		)
	}
	return bundle, nil
}

func publishIssueCandidate(
	ctx context.Context,
	config IssueAgentConfig,
	request issueagentcli.PublishCandidateRequest,
) (any, error) {
	if err := validateCompositionConfig(config, request.Repository); err != nil {
		return nil, err
	}
	if request.IssueNumber <= 0 ||
		request.Now.IsZero() || request.Now.Location() != time.UTC {
		return nil, errors.New("candidate publication request is invalid")
	}
	client, err := issueAgentClient(ctx, config, false)
	if err != nil {
		return nil, err
	}
	store, err := issueAgentStateStore(config, client)
	if err != nil {
		return nil, err
	}
	loaded, found, err := store.Load(ctx, request.IssueNumber)
	if err != nil || !found || loaded.HeadSHA != request.ExpectedStateHead ||
		loaded.State.Task == nil ||
		loaded.State.SourceSHA != request.ControlSHA {
		return nil, errors.New("candidate publication state is stale")
	}
	contextBundle, err := readContextBundle(request.ContextPath)
	if err != nil {
		return finalizeIssueTaskNeedsHuman(
			ctx,
			config,
			client,
			store,
			loaded,
			"task did not produce a valid Context Bundle",
			request.Now,
		)
	}
	engineerResult, err := readEngineerResult(request.EngineerResultPath)
	if err != nil {
		return finalizeIssueTaskNeedsHuman(
			ctx,
			config,
			client,
			store,
			loaded,
			"Codex did not produce a valid complete result",
			request.Now,
		)
	}
	candidate, err := readCandidate(request.CandidatePath)
	if err != nil {
		return finalizeIssueTaskNeedsHuman(
			ctx,
			config,
			client,
			store,
			loaded,
			"Codex task did not produce a valid bounded candidate",
			request.Now,
		)
	}
	evidence, err := readEvidence(request.EvidencePath)
	if err != nil {
		return finalizeIssueTaskNeedsHuman(
			ctx,
			config,
			client,
			store,
			loaded,
			"clean Verifier did not produce valid trusted evidence",
			request.Now,
		)
	}
	expectedParent := loaded.State.Task.BaseSHA
	existingBranch := false
	if loaded.State.Work != nil {
		expectedParent = loaded.State.Work.HeadSHA
		existingBranch = true
	}
	parent, err := client.Commit(ctx, expectedParent)
	if err != nil {
		return nil, err
	}
	input := issueagent.CandidatePublicationInput{
		State: loaded.State, Context: contextBundle,
		Engineer: engineerResult, Candidate: candidate,
		Evidence: evidence, ExistingBranch: existingBranch,
		ExpectedParentSHA: expectedParent, BaseTreeSHA: parent.TreeSHA,
	}
	if _, err := issueagent.PlanCandidatePublication(input); err != nil {
		return finalizeIssueTaskNeedsHuman(
			ctx,
			config,
			client,
			store,
			loaded,
			"candidate is not eligible for automatic publication",
			request.Now,
		)
	}
	publisher, err := issueagentgithub.NewCandidatePublisher(
		config.Repository,
		config.AppLogin,
		store,
		client,
	)
	if err != nil {
		return nil, err
	}
	return publisher.Publish(
		ctx,
		issueagentgithub.CandidatePublishRequest{
			ExpectedStateHead: request.ExpectedStateHead,
			Input:             input,
			Now:               request.Now,
		},
	)
}

func finalizeIssueTaskNeedsHuman(
	ctx context.Context,
	config IssueAgentConfig,
	client *issueagentgithub.Client,
	store *issueagentgithub.StateStore,
	expected issueagentgithub.LoadedState,
	reason string,
	now time.Time,
) (any, error) {
	loaded, found, err := store.Load(ctx, expected.State.IssueNumber)
	if err != nil || !found || loaded.HeadSHA != expected.HeadSHA {
		return nil, errors.New("Issue Agent state changed before failure finalization")
	}
	issue, err := client.Issue(ctx, loaded.State.IssueNumber)
	if err != nil || issue.State != "open" {
		return nil, errors.New("Issue changed before failure finalization")
	}
	if loaded.State.Authorization == nil {
		return nil, errors.New("Issue Agent task lacks durable authorization")
	}
	permission, err := client.ActorPermission(
		ctx,
		loaded.State.Authorization.Actor,
	)
	if err != nil || !issueagent.WritePermission(string(permission)) {
		return nil, errors.New(
			"Issue Agent authorization is no longer valid",
		)
	}
	next, err := issueagent.BuildNeedsHumanState(
		loaded.State,
		reason,
		now,
	)
	if err != nil {
		return nil, err
	}
	parent, err := client.Commit(ctx, loaded.HeadSHA)
	if err != nil {
		return nil, err
	}
	publication, err := store.Advance(
		ctx,
		issueagentgithub.StateAdvanceRequest{
			State: next, ExpectedParentSHA: loaded.HeadSHA,
			BaseTreeSHA: parent.TreeSHA, ExistingBranch: true,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := repairIssueStatus(
		ctx,
		client,
		config.AppLogin,
		next,
	); err != nil {
		return nil, err
	}
	if _, _, err := setIssueAgentTracking(
		ctx,
		client,
		issue,
		false,
	); err != nil {
		return nil, err
	}
	return reconcileResult{
		Repository: config.Repository, IssueNumber: next.IssueNumber,
		ControlSHA: next.SourceSHA, StateHeadSHA: publication.HeadSHA,
		State: string(next.State), Reason: next.Reason,
	}, nil
}

func buildContextForState(
	ctx context.Context,
	config IssueAgentConfig,
	client *issueagentgithub.Client,
	policy issueAgentPolicy,
	state contract.IssueAgentState,
) (contract.ContextBundle, error) {
	if state.Task == nil || state.Authorization == nil {
		return contract.ContextBundle{}, errors.New(
			"task state lacks trusted authorization",
		)
	}
	var source issueagentgithub.ContextSource = client
	if state.Task.Kind == contract.TaskKindReview {
		if config.ReviewAgentAppLogin == "" {
			return contract.ContextBundle{}, errors.New(
				"Review Agent App identity is not configured",
			)
		}
		source = issueAgentReviewContextSource{
			Client:   client,
			AppLogin: config.ReviewAgentAppLogin,
			HeadSHA:  state.Task.BaseSHA,
		}
	}
	builder, err := issueagentgithub.NewContextBuilder(source)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	instructions, err := client.InstructionFileDigests(
		ctx,
		state.Task.BaseSHA,
	)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	outputSchemaDigest, err := digestFile(filepath.Join(
		config.WorkingDirectory,
		".github/issue-agent/engineer-result.schema.json",
	))
	if err != nil {
		return contract.ContextBundle{}, err
	}
	pullRequest := int64(0)
	if state.Work != nil {
		pullRequest = state.Work.PullRequest
	}
	return builder.Build(
		ctx,
		issueagentgithub.BuildContextRequest{
			Repository: config.Repository, IssueNumber: state.IssueNumber,
			PullRequestNumber: pullRequest,
			StatusCommentID:   state.StatusCommentID,
			Sequence:          state.Sequence,
			Task:              *state.Task, Authorization: *state.Authorization,
			RequiredTests:      policy.RequiredSuites,
			RiskCeiling:        []string{"low"},
			InstructionDigests: instructions,
			KnowledgePaths:     policy.KnowledgePaths,
			OutputSchemaDigest: outputSchemaDigest,
			Limits: contract.EngineerLimits{
				WallTimeSeconds:      policy.Engineer.WallTimeSeconds,
				ModifyTestIterations: policy.Engineer.ModifyTestIterations,
			},
			CreatedAt: state.UpdatedAt,
		},
	)
}

func currentAuthorization(
	ctx context.Context,
	client *issueagentgithub.Client,
	issue issueagentgithub.IssueFacts,
	comments []issueagentgithub.IssueComment,
	current *contract.IssueAgentState,
) (*contract.AuthorizationRecord, string, error) {
	trustedAuthor := issueagent.TrustedAssociation(issue.AuthorAssociation)
	authorPermission, err := readIssueAuthorPermission(
		ctx,
		client,
		issue.Author,
		trustedAuthor,
	)
	if err != nil {
		if trustedAuthor {
			return nil, "", fmt.Errorf(
				"read trusted Issue author permission: %w",
				err,
			)
		}
		authorPermission = issueagentgithub.PermissionRead
	}
	for index := len(comments) - 1; index >= 0; index-- {
		command, ok := issueagent.ParseIssueCommand(comments[index].Body)
		if !ok {
			continue
		}
		eventID := "issue_comment:" +
			strconv.FormatInt(comments[index].ID, 10)
		permission, permissionErr := client.ActorPermission(
			ctx,
			comments[index].Author,
		)
		if permissionErr != nil ||
			!issueagent.WritePermission(string(permission)) {
			continue
		}
		if current != nil && current.Authorization != nil &&
			current.Authorization.EventID == eventID {
			return &contract.AuthorizationRecord{
				Actor: comments[index].Author, Permission: string(permission),
				EventID: eventID,
			}, string(authorPermission), nil
		}
		return &contract.AuthorizationRecord{
			Actor:      comments[index].Author,
			Permission: string(permission),
			EventID:    eventID,
			Command:    string(command),
		}, string(authorPermission), nil
	}
	if trustedAuthor &&
		issueagent.WritePermission(string(authorPermission)) {
		return &contract.AuthorizationRecord{
			Actor: issue.Author, Permission: string(authorPermission),
			EventID: "issue:" + strconv.FormatInt(issue.Number, 10),
		}, string(authorPermission), nil
	}
	return nil, string(authorPermission), nil
}

func readIssueAuthorPermission(
	ctx context.Context,
	client *issueagentgithub.Client,
	author string,
	retry bool,
) (issueagentgithub.Permission, error) {
	permissionContext := ctx
	cancel := func() {}
	if retry {
		permissionContext, cancel = context.WithTimeout(
			ctx,
			issueAuthorPermissionRecoveryWindow,
		)
	}
	defer cancel()

	permission, err := client.ActorPermission(permissionContext, author)
	if !retry || err == nil && issueagent.WritePermission(string(permission)) {
		return permission, err
	}
	for attempt := 1; attempt < issueAuthorPermissionRecoveryAttempts; attempt++ {
		delay := time.Duration(0)
		if attempt >= 2 {
			delay = (100 * time.Millisecond) << (attempt - 2)
		}
		if waitErr := waitIssueAuthorPermissionRecovery(
			permissionContext,
			delay,
		); waitErr != nil {
			return "", waitErr
		}
		permission, err = client.ActorPermission(permissionContext, author)
		if err == nil && issueagent.WritePermission(string(permission)) {
			return permission, nil
		}
	}
	return permission, err
}

func waitIssueAuthorPermissionRecovery(
	ctx context.Context,
	delay time.Duration,
) error {
	if delay == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func currentReviewAuthorization(
	ctx context.Context,
	client *issueagentgithub.Client,
	request issueagentcli.ReconcileGitHubRequest,
	current *contract.IssueAgentState,
	reviewAgentAppLogin string,
) (*contract.AuthorizationRecord, string, error) {
	if current == nil || current.Work == nil ||
		current.State != contract.IssueStateDraft &&
			current.State != contract.IssueStateReadyForReview {
		return nil, "", nil
	}
	event, err := readIssueAgentEvent(request.EventPath)
	if err != nil {
		return nil, "", err
	}
	if request.EventName == "workflow_run" &&
		event.WorkflowRun.HeadRepository.FullName != request.Repository {
		return nil, "", errors.New("Review signal repository does not match")
	}
	wakeup, relevant, err := reviewWakeupFromEvent(request.EventName, event)
	if err != nil || !relevant {
		return nil, "", err
	}
	if wakeup.PullRequest.Number != current.Work.PullRequest ||
		wakeup.PullRequest.Head.Ref != current.Work.Branch {
		return nil, "", errors.New("Review event does not match Agent work")
	}
	if reviewAgentAppLogin == "" ||
		wakeup.Actor != reviewAgentAppLogin {
		return nil, "", nil
	}
	threads, requested, err := client.ReadReviewAgentFindings(
		ctx,
		current.Work.PullRequest,
		current.Work.HeadSHA,
		reviewAgentAppLogin,
	)
	if err != nil {
		return nil, "", err
	}
	if !requested {
		return nil, "", nil
	}
	digest, err := digestJSON(threads)
	if err != nil {
		return nil, "", err
	}
	if current.ReviewDigest == digest {
		return nil, "", nil
	}
	return &contract.AuthorizationRecord{
		Actor:      wakeup.Actor,
		Permission: "review_agent",
		EventID:    wakeup.EventID,
	}, digest, nil
}

type issueAgentReviewContextSource struct {
	*issueagentgithub.Client
	AppLogin string
	HeadSHA  string
}

func (source issueAgentReviewContextSource) ReadContextReviewThreads(
	ctx context.Context,
	pullRequest int64,
) ([]contract.ReviewThreadSnapshot, error) {
	threads, requested, err := source.Client.ReadReviewAgentFindings(
		ctx,
		pullRequest,
		source.HeadSHA,
		source.AppLogin,
	)
	if err != nil {
		return nil, err
	}
	if !requested {
		return nil, errors.New(
			"Review Agent change request is no longer current",
		)
	}
	return threads, nil
}

func (source issueAgentReviewContextSource) ReadActorPermission(
	ctx context.Context,
	actor string,
) (issueagentgithub.Permission, error) {
	if actor == source.AppLogin {
		return issueagentgithub.Permission("review_agent"), nil
	}
	return source.Client.ReadActorPermission(ctx, actor)
}

type issueAgentReviewWakeup struct {
	PullRequest issueAgentPullRequest
	Actor       string
	EventID     string
}

func reviewWakeupFromEvent(
	eventName string,
	event issueAgentEvent,
) (issueAgentReviewWakeup, bool, error) {
	var wakeup issueAgentReviewWakeup
	switch eventName {
	case "pull_request_review", "pull_request_review_comment":
		objectID := event.Review.ID
		if eventName == "pull_request_review_comment" {
			objectID = event.Comment.ID
		}
		wakeup = issueAgentReviewWakeup{
			PullRequest: event.PullRequest,
			Actor:       event.Sender.Login,
			EventID: eventName + ":" +
				strconv.FormatInt(objectID, 10),
		}
		if objectID <= 0 {
			return issueAgentReviewWakeup{}, false,
				errors.New("Review event lacks an immutable identity")
		}
	case "workflow_run":
		if event.WorkflowRun.Name != issueAgentPRSignalWorkflow ||
			event.WorkflowRun.Conclusion != "success" ||
			event.WorkflowRun.Event != "pull_request_review" &&
				event.WorkflowRun.Event != "pull_request_review_comment" {
			return issueAgentReviewWakeup{}, false, nil
		}
		if event.WorkflowRun.ID <= 0 ||
			len(event.WorkflowRun.PullRequests) != 1 ||
			event.WorkflowRun.HeadBranch !=
				event.WorkflowRun.PullRequests[0].Head.Ref {
			return issueAgentReviewWakeup{}, false,
				errors.New("Review signal lacks one immutable PR identity")
		}
		wakeup = issueAgentReviewWakeup{
			PullRequest: event.WorkflowRun.PullRequests[0],
			Actor:       event.WorkflowRun.Actor.Login,
			EventID: "workflow_run:" +
				strconv.FormatInt(event.WorkflowRun.ID, 10),
		}
	default:
		return issueAgentReviewWakeup{}, false, nil
	}
	if wakeup.PullRequest.Number <= 0 ||
		wakeup.PullRequest.Head.Ref == "" ||
		wakeup.Actor == "" {
		return issueAgentReviewWakeup{}, false,
			errors.New("Review event is incomplete")
	}
	return wakeup, true, nil
}

func ensureIssueStatus(
	ctx context.Context,
	client *issueagentgithub.Client,
	appLogin string,
	comments []issueagentgithub.IssueComment,
	state contract.IssueAgentState,
) (int64, error) {
	if state.StatusCommentID > 0 {
		return state.StatusCommentID, nil
	}
	var found int64
	for _, comment := range comments {
		if comment.Author == appLogin && comment.AuthorType == "Bot" &&
			strings.Contains(comment.Body, issueAgentStatusMarker) {
			if found != 0 {
				return 0, errors.New("Issue has duplicate Agent status comments")
			}
			found = comment.ID
		}
	}
	if found > 0 {
		return found, nil
	}
	body, err := issueagent.RenderIssueStatus(state)
	if err != nil {
		return 0, err
	}
	comment, err := client.CreateIssueComment(
		ctx,
		state.IssueNumber,
		body,
	)
	if err != nil || comment.Author != appLogin ||
		comment.AuthorType != "Bot" {
		return 0, errors.New("create Issue Agent status comment")
	}
	return comment.ID, nil
}

func repairIssueStatus(
	ctx context.Context,
	client *issueagentgithub.Client,
	appLogin string,
	state contract.IssueAgentState,
) error {
	if state.StatusCommentID <= 0 {
		return errors.New("Issue Agent state lacks a status comment")
	}
	current, err := client.IssueComment(
		ctx,
		state.StatusCommentID,
		state.IssueNumber,
	)
	if err != nil || current.Author != appLogin || current.AuthorType != "Bot" {
		return errors.New("Issue Agent status comment is not App-owned")
	}
	body, err := issueagent.RenderIssueStatus(state)
	if err != nil {
		return err
	}
	if current.Body == body {
		return nil
	}
	updated, err := client.UpdateIssueComment(
		ctx,
		state.IssueNumber,
		state.StatusCommentID,
		body,
	)
	if err != nil || updated.Author != appLogin ||
		updated.AuthorType != "Bot" || updated.Body != body {
		return errors.New("repair Issue Agent status comment")
	}
	return nil
}

func issueAgentClient(
	ctx context.Context,
	config IssueAgentConfig,
	allowMint bool,
) (*issueagentgithub.Client, error) {
	token := config.GitHubToken
	if token == "" && allowMint {
		minted, err := mintIssueAgentToken(ctx, config)
		if err != nil {
			return nil, err
		}
		token = minted.Token
	}
	return issueagentgithub.NewClient(
		issueagentgithub.ClientConfig{
			BaseURL: config.APIBaseURL, Repository: config.Repository,
			Token: token, MaxPages: 10, MaxBodyBytes: 16 << 20,
		},
		config.HTTPClient,
	)
}

func mintIssueAgentToken(
	ctx context.Context,
	config IssueAgentConfig,
) (issueagentgithub.InstallationToken, error) {
	minter, err := issueagentgithub.NewAppTokenMinter(
		issueagentgithub.AppTokenConfig{
			BaseURL: config.APIBaseURL, AppID: config.AppID,
			InstallationID: config.AppInstallationID,
			RepositoryID:   config.RepositoryID,
			Repository:     config.Repository,
			PrivateKeyPEM:  config.AppPrivateKeyPEM,
		},
		config.HTTPClient,
		config.Now,
	)
	if err != nil {
		return issueagentgithub.InstallationToken{}, err
	}
	return minter.Mint(ctx)
}

func issueAgentStateStore(
	config IssueAgentConfig,
	client *issueagentgithub.Client,
) (*issueagentgithub.StateStore, error) {
	return issueagentgithub.NewStateStore(
		config.Repository,
		config.AppLogin,
		client,
	)
}

func resolveIssueNumber(
	ctx context.Context,
	client *issueagentgithub.Client,
	request issueagentcli.ReconcileGitHubRequest,
) (int64, error) {
	if request.IssueNumber > 0 {
		return request.IssueNumber, nil
	}
	if request.EventPath != "" {
		event, err := readIssueAgentEvent(request.EventPath)
		if err != nil {
			return 0, err
		}
		if event.Issue.Number > 0 {
			return event.Issue.Number, nil
		}
		pullRequest := event.PullRequest
		if request.EventName == "workflow_run" {
			if event.WorkflowRun.Name != issueAgentPRSignalWorkflow ||
				event.WorkflowRun.Conclusion != "success" ||
				event.WorkflowRun.HeadRepository.FullName !=
					request.Repository {
				return 0, nil
			}
			if len(event.WorkflowRun.PullRequests) != 1 {
				return 0, nil
			}
			pullRequest = event.WorkflowRun.PullRequests[0]
			if event.WorkflowRun.HeadBranch != pullRequest.Head.Ref {
				return 0, nil
			}
		}
		if strings.HasPrefix(
			pullRequest.Head.Ref,
			issueAgentBranchPrefix,
		) {
			number, err := strconv.ParseInt(
				strings.TrimPrefix(
					pullRequest.Head.Ref,
					issueAgentBranchPrefix,
				),
				10,
				64,
			)
			if err == nil && number > 0 {
				return number, nil
			}
		}
	}
	if request.EventName == "schedule" {
		issues, err := client.ListOpenIssueNumbersByLabel(
			ctx,
			issueAgentTrackingLabel,
		)
		if err != nil || len(issues) == 0 {
			return 0, err
		}
		tick := request.Now.Unix() / int64((5*time.Minute)/time.Second)
		return issues[int(tick%int64(len(issues)))], nil
	}
	return 0, nil
}

func setIssueAgentTracking(
	ctx context.Context,
	client *issueagentgithub.Client,
	issue issueagentgithub.IssueFacts,
	tracked bool,
) (issueagentgithub.IssueFacts, bool, error) {
	_, found := slices.BinarySearch(issue.Labels, issueAgentTrackingLabel)
	if found == tracked {
		return issue, false, nil
	}
	if err := client.SetIssueLabelPresence(
		ctx,
		issue.Number,
		issueAgentTrackingLabel,
		tracked,
	); err != nil {
		return issueagentgithub.IssueFacts{}, false, err
	}
	current, err := client.Issue(ctx, issue.Number)
	_, currentlyTracked := slices.BinarySearch(
		current.Labels,
		issueAgentTrackingLabel,
	)
	if err != nil || currentlyTracked != tracked {
		return issueagentgithub.IssueFacts{}, false,
			errors.New("Issue Agent tracking label write is inconsistent")
	}
	return current, true, nil
}

func readIssueAgentEvent(path string) (issueAgentEvent, error) {
	if path == "" {
		return issueAgentEvent{}, errors.New("GitHub event path is required")
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) > 1<<20 {
		return issueAgentEvent{}, errors.New("read bounded GitHub event")
	}
	var event issueAgentEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&event); err != nil {
		return issueAgentEvent{}, errors.New("decode GitHub event")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return issueAgentEvent{}, errors.New("GitHub event has trailing JSON")
	}
	return event, nil
}

func resolveAffectedSource(
	ctx context.Context,
	client *issueagentgithub.Client,
	body string,
	defaultSHA string,
) (string, string, error) {
	version, ambiguous := issueagent.IssueFormValue(body, "Affected version")
	if ambiguous {
		return defaultSHA,
			"Affected version appears more than once; keep one exact value.",
			nil
	}
	if version == "" || version == "_No response_" {
		return defaultSHA, "", nil
	}
	resolver, err := issueagentgithub.NewVersionSourceResolver(client)
	if err != nil {
		return "", "", err
	}
	if affectedCommitPattern.MatchString(version) {
		exists, err := resolver.CommitExists(ctx, version)
		if err != nil {
			return "", "", err
		}
		if exists {
			return version, "", nil
		}
		return defaultSHA,
			"Affected version commit does not exist in this repository.",
			nil
	}
	if affectedTagPattern.MatchString(version) {
		commits, err := resolver.ResolveTag(ctx, version)
		if err != nil {
			return "", "", err
		}
		if len(commits) == 1 {
			return commits[0], "", nil
		}
		return defaultSHA,
			"Affected version release tag does not exist in this repository.",
			nil
	}
	return defaultSHA,
		"Affected version must be an existing release tag or full commit SHA.",
		nil
}

func loadIssueAgentPolicy(
	config IssueAgentConfig,
) (issueAgentPolicy, string, error) {
	path := filepath.Join(
		config.WorkingDirectory,
		".github/issue-agent/policy.json",
	)
	body, err := os.ReadFile(path)
	if err != nil || len(body) > 1<<20 {
		return issueAgentPolicy{}, "", errors.New("read Issue Agent policy")
	}
	var policy issueAgentPolicy
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return issueAgentPolicy{}, "", errors.New("decode Issue Agent policy")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return issueAgentPolicy{}, "", errors.New("Issue Agent policy has trailing JSON")
	}
	if policy.SchemaVersion != 2 || !policy.Enabled ||
		policy.RolloutMode != "active" ||
		policy.DefaultBranch != "main" ||
		policy.PublisherEnvironment != "issue-agent-publisher" ||
		policy.Engineer.ActionSHA == "" ||
		policy.Engineer.CodexVersion == "" ||
		policy.Engineer.Model == "" ||
		policy.Engineer.ReasoningEffort != "high" ||
		policy.Engineer.Sandbox != "workspace-write" ||
		!policy.Engineer.NetworkAccess || !policy.Engineer.Ephemeral ||
		policy.Budgets.MaxEngineerAttempts == 0 ||
		policy.Budgets.MaxReviewIterations == 0 ||
		policy.Budgets.TaskStaleAfterSeconds <
			policy.Engineer.WallTimeSeconds ||
		policy.Budgets.TaskStaleAfterSeconds > 24*60*60 ||
		len(policy.RequiredSuites) == 0 ||
		len(policy.Verification) == 0 {
		return issueAgentPolicy{}, "", errors.New("Issue Agent policy is invalid")
	}
	sum := sha256.Sum256(body)
	return policy, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateCompositionConfig(
	config IssueAgentConfig,
	repository string,
) error {
	if repository == "" || repository != config.Repository ||
		config.HTTPClient == nil || config.APIBaseURL == "" ||
		config.AppLogin == "" || config.WorkingDirectory == "" ||
		config.Now == nil {
		return errors.New("Issue Agent composition is incomplete")
	}
	return nil
}

func digestFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("inspect bounded trusted file %q", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read trusted file %q", path)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func digestJSON(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("encode Issue snapshot")
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func readContextBundle(path string) (contract.ContextBundle, error) {
	file, err := os.Open(path)
	if err != nil {
		return contract.ContextBundle{}, errors.New("open Context Bundle")
	}
	defer file.Close()
	return contract.DecodeContextBundle(file, 16<<20)
}

func readEngineerResult(path string) (contract.EngineerResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return contract.EngineerResult{}, errors.New("open Engineer Result")
	}
	defer file.Close()
	return contract.DecodeEngineerResult(file, 2<<20)
}

func readCandidate(path string) (contract.CandidateSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return contract.CandidateSnapshot{}, errors.New("open Candidate Snapshot")
	}
	defer file.Close()
	return contract.DecodeCandidateSnapshot(file, 40<<20)
}

func readEvidence(path string) (contract.CandidateEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return contract.CandidateEvidence{}, errors.New("open Candidate Evidence")
	}
	defer file.Close()
	return contract.DecodeCandidateEvidence(file, 4<<20)
}
