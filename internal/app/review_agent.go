package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	cli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

// ReviewAgentAppConfig is one role-specific GitHub App installation.
type ReviewAgentAppConfig struct {
	AppID          int64
	InstallationID int64
	RepositoryID   int64
	PrivateKeyPEM  []byte
}

// ReviewAgentConfig wires the standalone Review Agent without constructing
// product cluster or server runtimes.
type ReviewAgentConfig struct {
	HTTPClient         *http.Client
	APIBaseURL         string
	GraphQLURL         string
	Repository         string
	GitHubReadToken    string
	ControlSHA         string
	PolicyPath         string
	PromptPath         string
	ResultSchemaPath   string
	WorkspaceDirectory string
	EvidenceLedgerPath string
	ExecutorHome       string
	ExecutablePath     string
	TemporaryDirectory string
	ProcessSandboxPath string
	ProcessHelperPath  string
	StateWriterApp     *ReviewAgentAppConfig
	ReviewApp          *ReviewAgentAppConfig
	Now                func() time.Time
}

// NewReviewAgentOperations composes role-specific dependencies lazily.
func NewReviewAgentOperations(config ReviewAgentConfig) cli.Operations {
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	reconcile := func(
		ctx context.Context,
		request cli.ReconcileGitHubRequest,
	) (cli.ReconcileGitHubResponse, error) {
		return reconcileReviewGitHub(ctx, config, now, request)
	}
	return cli.Operations{
		ReconcileGitHub: reconcile,
		RecoverReview:   reconcile,
		BuildContext: func(
			ctx context.Context,
			request cli.BuildContextRequest,
		) (cli.BuildContextResponse, error) {
			return buildReviewContext(ctx, config, request)
		},
		VerifyBaseline: func(
			ctx context.Context,
			request cli.VerifyBaselineRequest,
		) (contract.ReviewEvidence, error) {
			return verifyReviewBaseline(ctx, config, now, request)
		},
		ValidateReviewResult: func(
			_ context.Context,
			request cli.ValidateReviewResultRequest,
		) (verify.ValidatedDecision, error) {
			return verify.ValidateFinalResult(
				request.Context,
				request.Evidence,
				request.Result,
				request.BeforeTreeDigest,
				request.AfterTreeDigest,
			)
		},
		ValidateExplanation: func(
			_ context.Context,
			request cli.ValidateExplanationRequest,
		) (cli.ValidateExplanationResponse, error) {
			if contract.MustGenerationDigest(request.Generation) !=
				contract.MustGenerationDigest(request.Result.Generation) {
				return cli.ValidateExplanationResponse{}, errors.New(
					"Review explanation generation is stale",
				)
			}
			digest, err := contract.ExplanationResultDigest(request.Result)
			if err != nil {
				return cli.ValidateExplanationResponse{}, err
			}
			return cli.ValidateExplanationResponse{
				Digest:        digest,
				ResponseBytes: uint64(len([]byte(request.Result.Reply))),
			}, nil
		},
		AppendState: func(
			ctx context.Context,
			request cli.AppendStateRequest,
		) (cli.AppendStateResponse, error) {
			return appendReviewState(ctx, config, request)
		},
		PublishReview: func(
			ctx context.Context,
			request cli.PublishReviewRequest,
		) (cli.PublishReviewResponse, error) {
			return publishReview(ctx, config, request)
		},
	}
}

func reconcileReviewGitHub(
	ctx context.Context,
	config ReviewAgentConfig,
	now func() time.Time,
	request cli.ReconcileGitHubRequest,
) (cli.ReconcileGitHubResponse, error) {
	if request.PullRequest <= 0 || request.RunID <= 0 ||
		!isGitSHA(config.ControlSHA) {
		return cli.ReconcileGitHubResponse{}, errors.New(
			"Review reconciliation configuration is incomplete",
		)
	}
	policy, _, err := LoadReviewAgentPolicy(config.PolicyPath)
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	client, err := newReviewGitHubClient(
		config,
		config.GitHubReadToken,
		policy.Governance.OwnerLogins,
		policy.ControlPlanePaths,
	)
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	snapshot, err := client.ReadPullRequestMetadata(ctx, request.PullRequest)
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	snapshot.Facts.StateParentSHA = config.ControlSHA

	reviewStore, err := github.NewReviewStateStore(
		config.Repository,
		policy.Apps.StateWriter.Login,
		client,
	)
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	loadedState, stateFound, err := reviewStore.Load(
		ctx,
		request.PullRequest,
	)
	if err != nil {
		bootstrapHead, bootstrapFound, headErr := client.StateRefHead(
			ctx,
			"review-state/pr-"+strconv.FormatInt(
				request.PullRequest,
				10,
			),
		)
		if headErr == nil && bootstrapFound &&
			bootstrapHead == config.ControlSHA {
			loadedState = github.LoadedReviewState{}
			stateFound = false
			err = nil
		}
	}
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	var currentState *contract.ReviewState
	if stateFound {
		state := loadedState.State
		currentState = &state
	}
	schedulerStore, err := github.NewSchedulerStore(
		policy.Apps.StateWriter.Login,
		client,
		reviewLifecyclePolicy(policy).Scheduler,
	)
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	loadedScheduler, schedulerFound, err := schedulerStore.Load(ctx)
	if err != nil {
		bootstrapHead, bootstrapFound, headErr := client.StateRefHead(
			ctx,
			"review-state/scheduler",
		)
		if headErr == nil && bootstrapFound &&
			bootstrapHead == config.ControlSHA {
			loadedScheduler = github.LoadedSchedulerState{}
			schedulerFound = false
			err = nil
		}
	}
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	reconcileTime := now().UTC()
	scheduler := loadedScheduler.State
	if !schedulerFound {
		scheduler = usecase.SchedulerState{
			SchemaVersion: 1,
			SourceSHA:     config.ControlSHA,
			Sequence:      1,
			UpdatedAt:     reconcileTime,
		}
	}
	signal := usecase.Signal{
		Kind: request.SignalKind, RunID: request.RunID,
		WorkerAttempt: request.WorkerAttempt,
	}
	if request.Completion != nil {
		if request.SignalKind != usecase.SignalCompletion {
			return cli.ReconcileGitHubResponse{}, errors.New(
				"Review completion signal is inconsistent",
			)
		}
		signal.Completion = request.Completion
	}
	if request.SignalKind == usecase.SignalCommand {
		command, found, commandErr := resolveReviewCommand(
			ctx,
			client,
			snapshot,
			request.CommentID,
		)
		if commandErr != nil {
			if snapshot.Facts.ContextFailureReason == "" {
				return cli.ReconcileGitHubResponse{}, commandErr
			}
			signal.Kind = usecase.SignalObserved
		} else if found {
			signal.Command = &command
		} else {
			signal.Kind = usecase.SignalObserved
		}
	}
	plan, err := usecase.ReconcilePullRequest(usecase.ReconcileInput{
		Facts: snapshot.Facts, State: currentState,
		Scheduler: scheduler, Signal: signal,
		Policy: reviewLifecyclePolicy(policy), Now: reconcileTime,
	})
	if err != nil {
		return cli.ReconcileGitHubResponse{}, err
	}
	if !schedulerFound {
		plan.NextScheduler.Sequence = 1
		plan.NextScheduler.PreviousStateDigest = ""
		plan.NextScheduler.SourceSHA = config.ControlSHA
		plan.NextScheduler.UpdatedAt = reconcileTime
	}
	response := cli.ReconcileGitHubResponse{
		Plan: plan, StateFound: stateFound,
		StateHeadSHA:     loadedState.HeadSHA,
		SchedulerFound:   schedulerFound,
		SchedulerHeadSHA: loadedScheduler.HeadSHA,
		SchedulerChanged: !schedulerFound ||
			schedulerDigestChanged(
				scheduler,
				plan.NextScheduler,
				reviewLifecyclePolicy(policy).Scheduler,
			),
	}
	switch plan.Action {
	case usecase.ActionNoop,
		usecase.ActionRepairProjection,
		usecase.ActionRespondStatus:
		response.NextState = currentState
		return response, nil
	default:
		next, buildErr := usecase.BuildNextState(
			currentState,
			plan,
			reconcileTime,
		)
		if buildErr != nil {
			return cli.ReconcileGitHubResponse{}, buildErr
		}
		response.NextState = &next
		response.StateChanged = true
		return response, nil
	}
}

func resolveReviewCommand(
	ctx context.Context,
	client *github.Client,
	snapshot github.PullRequestSnapshot,
	commentID int64,
) (usecase.Command, bool, error) {
	if commentID <= 0 {
		return usecase.Command{}, false, nil
	}
	var target *github.IssueComment
	for index := range snapshot.IssueComments {
		if snapshot.IssueComments[index].ID == commentID {
			target = &snapshot.IssueComments[index]
			break
		}
	}
	if target == nil {
		return usecase.Command{}, false, nil
	}
	if !strings.HasPrefix(target.Body, "@review-agent") {
		return usecase.Command{}, false, nil
	}
	permission := usecase.PermissionNone
	if target.Author != snapshot.Author {
		resolved, err := client.ActorPermission(ctx, target.Author)
		if err != nil {
			return usecase.Command{}, false, err
		}
		permission = usecase.Permission(resolved)
	}
	command, err := usecase.ParseCommand(usecase.CommandInput{
		Body: target.Body, Actor: target.Author,
		PRWriter: snapshot.Author, Permission: permission,
		Edited: !target.UpdatedAt.Equal(target.CreatedAt),
	})
	if err != nil {
		return usecase.Command{}, false, nil
	}
	return command, true, nil
}

func buildReviewContext(
	ctx context.Context,
	config ReviewAgentConfig,
	request cli.BuildContextRequest,
) (cli.BuildContextResponse, error) {
	policy, policyDigest, err := LoadReviewAgentPolicy(config.PolicyPath)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	client, err := newReviewGitHubClient(
		config,
		config.GitHubReadToken,
		policy.Governance.OwnerLogins,
		policy.ControlPlanePaths,
	)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	snapshot, err := client.ReadPullRequest(ctx, request.PullRequest)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	if request.PullRequest != request.Generation.PullRequest ||
		!sameContextGeneration(snapshot.Facts, request.Generation) {
		return cli.BuildContextResponse{}, errors.New(
			"Review context generation is stale",
		)
	}
	checks, err := verify.PlanChecks(
		snapshot.Inventory,
		policy.VerificationPolicy(),
		request.Risk,
	)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	paths := make([]string, 0, len(snapshot.Inventory.Files)*2)
	for _, file := range snapshot.Inventory.Files {
		paths = append(paths, file.Path)
		if file.PreviousPath != "" {
			paths = append(paths, file.PreviousPath)
		}
	}
	instructions, err := client.ReadBaseInstructions(
		ctx,
		request.Generation.BaseSHA,
		paths,
	)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	promptDigest, err := fileDigest(config.PromptPath, 2<<20)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	schemaDigest, err := fileDigest(config.ResultSchemaPath, 2<<20)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	threads := make([]contract.ReviewThreadContext, 0, len(snapshot.ReviewThreads))
	for _, thread := range snapshot.ReviewThreads {
		threads = append(threads, contract.ReviewThreadContext{
			ID: thread.ID, IsResolved: thread.IsResolved,
			Path: thread.Path, Line: thread.Line,
		})
	}
	discussion := reviewDiscussion(snapshot)
	contextDocument, err := verify.BuildContext(verify.ContextInput{
		Generation:   request.Generation,
		PolicyDigest: policyDigest, PromptDigest: promptDigest,
		OutputSchemaDigest: schemaDigest,
		ReviewReason:       request.ReviewReason,
		Title:              snapshot.Title, Body: snapshot.Body,
		LinkedIssues: snapshot.LinkedIssues, ReviewThreads: threads,
		Discussion:    discussion,
		PriorFindings: request.PriorFindings,
		Inventory:     snapshot.Inventory, Instructions: instructions,
		MandatoryChecks: checks,
	}, policy.Limits.MaxContextBytes)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	digest, err := contract.ReviewContextDigest(contextDocument)
	if err != nil {
		return cli.BuildContextResponse{}, err
	}
	return cli.BuildContextResponse{
		Context: contextDocument,
		Digest:  digest,
	}, nil
}

func reviewDiscussion(
	snapshot github.PullRequestSnapshot,
) []contract.DiscussionItem {
	discussion := make(
		[]contract.DiscussionItem,
		0,
		len(snapshot.Reviews)+
			len(snapshot.IssueComments)+
			len(snapshot.ReviewComments),
	)
	for _, review := range snapshot.Reviews {
		discussion = append(discussion, contract.DiscussionItem{
			Kind:       contract.DiscussionFormalReview,
			ID:         review.ID,
			Author:     review.Author,
			AuthorType: review.AuthorType,
			Body:       review.Body,
			State:      review.State,
			CommitSHA:  review.CommitID,
		})
	}
	for _, comment := range snapshot.IssueComments {
		discussion = append(discussion, contract.DiscussionItem{
			Kind:       contract.DiscussionIssueComment,
			ID:         comment.ID,
			Author:     comment.Author,
			AuthorType: comment.AuthorType,
			Body:       comment.Body,
		})
	}
	for _, comment := range snapshot.ReviewComments {
		discussion = append(discussion, contract.DiscussionItem{
			Kind:        contract.DiscussionReviewComment,
			ID:          comment.ID,
			Author:      comment.Author,
			AuthorType:  comment.AuthorType,
			Body:        comment.Body,
			Path:        comment.Path,
			Line:        comment.Line,
			Side:        comment.Side,
			InReplyToID: comment.InReplyToID,
		})
	}
	return discussion
}

func verifyReviewBaseline(
	ctx context.Context,
	config ReviewAgentConfig,
	now func() time.Time,
	request cli.VerifyBaselineRequest,
) (contract.ReviewEvidence, error) {
	if err := contract.ValidateReviewContext(request.Context); err != nil {
		return contract.ReviewEvidence{}, err
	}
	policy, _, err := LoadReviewAgentPolicy(config.PolicyPath)
	if err != nil {
		return contract.ReviewEvidence{}, err
	}
	ledger, err := verify.NewFileLedger(
		config.EvidenceLedgerPath,
		config.WorkspaceDirectory,
	)
	if err != nil {
		return contract.ReviewEvidence{}, err
	}
	executor, err := verify.NewOSExecutor(verify.OSExecutorConfig{
		HomeDir: config.ExecutorHome, Path: config.ExecutablePath,
		TempDir:       config.TemporaryDirectory,
		WorkspaceRoot: config.WorkspaceDirectory,
		SandboxBinary: config.ProcessSandboxPath,
		HelperBinary:  config.ProcessHelperPath,
	})
	if err != nil {
		return contract.ReviewEvidence{}, err
	}
	runner, err := verify.NewRunner(verify.RunnerConfig{
		WorkspaceRoot: config.WorkspaceDirectory,
		Policy:        policy.VerificationPolicy(),
		Executor:      executor,
		Ledger:        ledger,
		Now:           now,
	})
	if err != nil {
		return contract.ReviewEvidence{}, err
	}
	if request.CollectOnly {
		return runner.CollectEvidence(
			request.Context.Generation,
			request.Context.MandatoryChecks,
		)
	}
	checks := make([]contract.CheckEvidence, 0, len(request.Context.MandatoryChecks))
	for _, name := range request.Context.MandatoryChecks {
		evidence, runErr := runner.Run(
			ctx,
			request.Context.Generation,
			name,
		)
		if runErr != nil {
			return contract.ReviewEvidence{}, runErr
		}
		checks = append(checks, evidence)
	}
	evidence := contract.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    request.Context.Generation,
		Complete:      true,
		Checks:        checks,
		CreatedAt:     now().UTC(),
	}
	if err := contract.ValidateReviewEvidence(evidence); err != nil {
		return contract.ReviewEvidence{}, err
	}
	return evidence, nil
}

func appendReviewState(
	ctx context.Context,
	config ReviewAgentConfig,
	request cli.AppendStateRequest,
) (cli.AppendStateResponse, error) {
	policy, _, err := LoadReviewAgentPolicy(config.PolicyPath)
	if err != nil {
		return cli.AppendStateResponse{}, err
	}
	token, err := mintReviewAppToken(
		ctx,
		config,
		config.StateWriterApp,
		github.AppRoleStateWriter,
	)
	if err != nil {
		return cli.AppendStateResponse{}, err
	}
	client, err := newReviewGitHubClient(config, token.Token, nil, nil)
	if err != nil {
		return cli.AppendStateResponse{}, err
	}
	var head string
	switch request.Kind {
	case "pull_request":
		if request.ReviewState == nil || request.SchedulerState != nil {
			return cli.AppendStateResponse{}, errors.New(
				"Review state append kind is inconsistent",
			)
		}
		store, storeErr := github.NewReviewStateStore(
			config.Repository,
			policy.Apps.StateWriter.Login,
			client,
		)
		if storeErr != nil {
			return cli.AppendStateResponse{}, storeErr
		}
		head, err = store.Advance(
			ctx,
			*request.ReviewState,
			request.ExpectedParentSHA,
			request.ExistingBranch,
		)
	case "scheduler":
		if request.SchedulerState == nil || request.ReviewState != nil {
			return cli.AppendStateResponse{}, errors.New(
				"Review scheduler append kind is inconsistent",
			)
		}
		store, storeErr := github.NewSchedulerStore(
			policy.Apps.StateWriter.Login,
			client,
			reviewLifecyclePolicy(policy).Scheduler,
		)
		if storeErr != nil {
			return cli.AppendStateResponse{}, storeErr
		}
		head, err = store.Advance(
			ctx,
			*request.SchedulerState,
			request.ExpectedParentSHA,
			request.ExistingBranch,
		)
	default:
		return cli.AppendStateResponse{}, errors.New(
			"Review state append kind is invalid",
		)
	}
	if err != nil {
		return cli.AppendStateResponse{}, err
	}
	return cli.AppendStateResponse{HeadSHA: head}, nil
}

func reviewLifecyclePolicy(
	document ReviewAgentPolicy,
) usecase.Policy {
	return usecase.Policy{
		SupportedBaseBranches: append(
			[]string(nil),
			document.SupportedBaseBranches...,
		),
		MaxChangedFiles: document.Limits.MaxChangedFiles,
		MaxChangedBytes: document.Limits.MaxChangedBytes,
		MaxChangedLines: document.Limits.MaxChangedLines,
		MaxGenerationDuration: time.Duration(
			document.Reviewer.WallTimeSeconds,
		) * time.Second,
		MaxAutomaticReviewsPerHead:    document.Attempts.AutomaticPerHead,
		MaxReconsiderationsPerHead:    document.Attempts.MaxReconsiderationsPerHead,
		MaxInfrastructureRetries:      document.Attempts.MaxInfrastructureRetries,
		MaxExplanationSessionsPerHead: document.Interaction.MaxExplanationSessionsPerHead,
		MaxExplanationResponseBytes:   document.Interaction.MaxResponseBytesPerHead,
		Scheduler: usecase.SchedulerLimits{
			MaxActive:         document.Concurrency.RepositorySessions,
			MaxPerPullRequest: document.Concurrency.SessionsPerPullRequest,
			MaxFirstTimeExternal: document.Concurrency.
				FirstTimeExternalSessions,
		},
	}
}

func publishReview(
	ctx context.Context,
	config ReviewAgentConfig,
	request cli.PublishReviewRequest,
) (cli.PublishReviewResponse, error) {
	policy, _, err := LoadReviewAgentPolicy(config.PolicyPath)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	reader, err := newReviewGitHubClient(
		config,
		config.GitHubReadToken,
		policy.Governance.OwnerLogins,
		policy.ControlPlanePaths,
	)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	token, err := mintReviewAppToken(
		ctx,
		config,
		config.ReviewApp,
		github.AppRoleReviewPublisher,
	)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	writer, err := newReviewGitHubClient(config, token.Token, nil, nil)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	state, err := github.NewReviewStateStore(
		config.Repository,
		policy.Apps.StateWriter.Login,
		reader,
	)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	publisher, err := github.NewReviewPublisher(
		config.Repository,
		policy.Apps.Review.Slug,
		policy.Apps.Review.Login,
		state,
		reader,
		writer,
	)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	_, err = publisher.PublishDecision(
		ctx,
		github.ReviewPublicationRequest{
			ExpectedStateHead: request.ExpectedStateHead,
			State:             request.State,
			Result:            request.Result,
			Explanation:       request.Explanation,
		},
	)
	if err != nil {
		return cli.PublishReviewResponse{}, err
	}
	return cli.PublishReviewResponse{}, nil
}

func newReviewGitHubClient(
	config ReviewAgentConfig,
	token string,
	owners []string,
	controlPaths []string,
) (*github.Client, error) {
	if config.HTTPClient == nil {
		return nil, errors.New("Review GitHub HTTP client is unavailable")
	}
	return github.NewClient(github.ClientConfig{
		BaseURL: config.APIBaseURL, GraphQLURL: config.GraphQLURL,
		Repository: config.Repository, Token: token,
		MaxPages: 100, MaxBodyBytes: 16 << 20,
		ControlOwnerLogins: append([]string(nil), owners...),
		ControlPlanePaths:  append([]string(nil), controlPaths...),
	}, config.HTTPClient)
}

func mintReviewAppToken(
	ctx context.Context,
	config ReviewAgentConfig,
	app *ReviewAgentAppConfig,
	role github.AppRole,
) (github.InstallationToken, error) {
	if app == nil {
		return github.InstallationToken{}, errors.New(
			"Review Agent role configuration is unavailable",
		)
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	minter, err := github.NewAppTokenMinter(github.AppTokenConfig{
		BaseURL: config.APIBaseURL, AppID: app.AppID,
		InstallationID: app.InstallationID,
		RepositoryID:   app.RepositoryID,
		Repository:     config.Repository,
		PrivateKeyPEM:  append([]byte(nil), app.PrivateKeyPEM...),
		Role:           role,
	}, config.HTTPClient, now)
	if err != nil {
		return github.InstallationToken{}, err
	}
	return minter.Mint(ctx)
}

func schedulerDigestChanged(
	left usecase.SchedulerState,
	right usecase.SchedulerState,
	limits usecase.SchedulerLimits,
) bool {
	leftDigest, leftErr := usecase.SchedulerStateDigest(left, limits)
	rightDigest, rightErr := usecase.SchedulerStateDigest(right, limits)
	return leftErr != nil || rightErr != nil || leftDigest != rightDigest
}

func sameContextGeneration(
	facts usecase.PullRequestFacts,
	generation contract.GenerationIdentity,
) bool {
	return facts.Repository == generation.Repository &&
		facts.PullRequest == generation.PullRequest &&
		facts.HeadSHA == generation.HeadSHA &&
		facts.BaseSHA == generation.BaseSHA &&
		usecase.NormalizeTestMergeSHA(facts.TestMergeSHA) ==
			generation.TestMergeSHA &&
		facts.IntentDigest == generation.IntentDigest
}

func fileDigest(path string, maxBytes int64) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil || int64(len(body)) > maxBytes {
		return "", errors.New("read Review Agent control artifact")
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func isGitSHA(value string) bool {
	return len(value) == 40 &&
		strings.IndexFunc(value, func(character rune) bool {
			return !strings.ContainsRune("0123456789abcdef", character)
		}) == -1
}
