package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	issueagentmodel "github.com/WuKongIM/WuKongIM/internal/infra/issueagentmodel"
	issueagentworker "github.com/WuKongIM/WuKongIM/internal/runtime/issueagentworker"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

// IssueAgentDependencies are write-capable operations supplied only to the
// standalone Issue Agent command. They are never attached to App.New.
type IssueAgentDependencies struct {
	PublishLease     func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishResult    func(context.Context, issueagentcli.DocumentRequest) (any, error)
	RunWorker        func(context.Context, issueagentcli.DocumentRequest) (any, error)
	VerifyCheckpoint func(context.Context, issueagentcli.DocumentRequest) (any, error)
	MintAppToken     func(context.Context, issueagentcli.DocumentRequest) (any, error)
}

// IssueAgentWorkerConfig contains Supervisor-only provider inputs.
type IssueAgentWorkerConfig struct {
	HTTPClient             *http.Client
	DeepSeekAPIKey         string
	CodexAPIKey            string
	CodexBinary            string
	CodexMinimumVersion    string
	SandboxImage           string
	ForbiddenPublisherData bool
}

type runWorkerPayload struct {
	Task             issueagentcontract.TaskEnvelope `json:"task"`
	PromptBase64     string                          `json:"prompt_base64"`
	PolicyBase64     string                          `json:"policy_base64"`
	Workspace        string                          `json:"workspace"`
	MaxArtifactBytes int64                           `json:"max_artifact_bytes"`
}

// IssueAgentGitHubConfig contains Publisher-only process dependencies.
type IssueAgentGitHubConfig struct {
	HTTPClient                 *http.Client
	GitHubToken                string
	CheckpointKeyID            string
	CheckpointPrivateKeyBase64 string
	AppPrivateKeyPEM           []byte
	Now                        func() time.Time
}

type checkpointPayload struct {
	BaseURL     string                          `json:"base_url"`
	Repository  string                          `json:"repository"`
	AppLogin    string                          `json:"app_login"`
	IssueNumber int64                           `json:"issue_number"`
	Now         time.Time                       `json:"now"`
	KeySet      issueagentgithub.KeySet         `json:"key_set"`
	Comments    []issueagentgithub.IssueComment `json:"comments"`
	Checkpoint  issueagentcontract.Checkpoint   `json:"checkpoint"`
	Summary     string                          `json:"summary"`
	Labels      []string                        `json:"labels"`
}

type publishResultPayload struct {
	checkpointPayload
	Task       issueagentcontract.TaskEnvelope    `json:"task"`
	Result     issueagentcontract.AgentResult     `json:"result"`
	Validation issueagentgithub.PublishValidation `json:"validation"`
	Commit     *issueagentgithub.CommitPlan       `json:"commit,omitempty"`
	DraftPR    *issueagentgithub.DraftPullRequest `json:"draft_pr,omitempty"`
	ReadyPR    int64                              `json:"ready_pr,omitempty"`
}

type verifyCheckpointPayload struct {
	Repository  string                          `json:"repository"`
	AppLogin    string                          `json:"app_login"`
	IssueNumber int64                           `json:"issue_number"`
	Now         time.Time                       `json:"now"`
	KeySet      issueagentgithub.KeySet         `json:"key_set"`
	Comments    []issueagentgithub.IssueComment `json:"comments"`
}

type mintTokenPayload struct {
	BaseURL        string `json:"base_url"`
	AppID          int64  `json:"app_id"`
	InstallationID int64  `json:"installation_id"`
	RepositoryID   int64  `json:"repository_id"`
	Repository     string `json:"repository"`
}

// NewIssueAgentGitHubDependencies composes real GitHub operations from
// Publisher-only environment material.
func NewIssueAgentGitHubDependencies(
	config IssueAgentGitHubConfig,
) IssueAgentDependencies {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return IssueAgentDependencies{
		PublishLease: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload checkpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishCheckpoint(ctx, config, payload)
		},
		PublishResult: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishResultPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			if err := issueagentcontract.ValidateAgentResult(
				payload.Result, payload.Task,
			); err != nil {
				return nil, err
			}
			if payload.Checkpoint.State != payload.Result.RequestedState ||
				payload.Checkpoint.NextAction != payload.Result.RequestedAction ||
				!reflect.DeepEqual(
					payload.Validation.ChangeSet,
					payload.Result.ChangeSet,
				) {
				return nil, errors.New("Worker result does not match publication checkpoint")
			}
			if err := issueagentgithub.ValidatePublish(payload.Validation); err != nil {
				return nil, err
			}
			client, store, previous, err := prepareCheckpointPublication(
				config, payload.checkpointPayload,
			)
			if err != nil {
				return nil, err
			}
			if payload.Commit != nil {
				if !reflect.DeepEqual(payload.Commit.ChangeSet, payload.Result.ChangeSet) {
					return nil, errors.New("commit plan does not match Worker ChangeSet")
				}
				if _, err := client.PublishCommit(ctx, *payload.Commit); err != nil {
					return nil, err
				}
			} else if len(payload.Result.ChangeSet.Files) != 0 {
				return nil, errors.New("Worker file changes require a commit plan")
			}
			if payload.DraftPR != nil {
				if _, err := client.CreateDraftPullRequest(ctx, *payload.DraftPR); err != nil {
					return nil, err
				}
			}
			if payload.ReadyPR != 0 {
				if _, err := client.MarkPullRequestReady(ctx, payload.ReadyPR); err != nil {
					return nil, err
				}
			}
			return appendCheckpointProjection(
				ctx, client, store, previous, payload.checkpointPayload,
			)
		},
		VerifyCheckpoint: func(
			_ context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload verifyCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			store, err := issueagentgithub.NewCheckpointStore(
				payload.Repository, payload.AppLogin, payload.KeySet,
				issueagentgithub.Signer{},
			)
			if err != nil {
				return nil, err
			}
			return store.VerifyChain(
				payload.Comments, payload.IssueNumber, payload.Now,
			)
		},
		MintAppToken: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload mintTokenPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			minter, err := issueagentgithub.NewAppTokenMinter(
				issueagentgithub.AppTokenConfig{
					BaseURL: payload.BaseURL, AppID: payload.AppID,
					InstallationID: payload.InstallationID,
					RepositoryID:   payload.RepositoryID,
					Repository:     payload.Repository,
					PrivateKeyPEM:  config.AppPrivateKeyPEM,
				},
				config.HTTPClient,
				config.Now,
			)
			if err != nil {
				return nil, err
			}
			return minter.Mint(ctx)
		},
	}
}

// NewIssueAgentWorkerDependency composes the credential-separated Supervisor.
func NewIssueAgentWorkerDependency(
	config IssueAgentWorkerConfig,
) func(context.Context, issueagentcli.DocumentRequest) (any, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return func(
		ctx context.Context,
		document issueagentcli.DocumentRequest,
	) (any, error) {
		if config.ForbiddenPublisherData {
			return nil, errors.New("Worker process contains Publisher credentials")
		}
		var payload runWorkerPayload
		if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
			return nil, err
		}
		prompt, err := base64.StdEncoding.Strict().DecodeString(payload.PromptBase64)
		if err != nil || base64.StdEncoding.EncodeToString(prompt) != payload.PromptBase64 {
			return nil, errors.New("Worker prompt encoding is invalid")
		}
		policy, err := base64.StdEncoding.Strict().DecodeString(payload.PolicyBase64)
		if err != nil || base64.StdEncoding.EncodeToString(policy) != payload.PolicyBase64 {
			return nil, errors.New("Worker policy encoding is invalid")
		}
		sandbox, err := issueagentworker.NewDockerSandboxRunner(
			issueagentworker.DockerSandboxConfig{
				Image: config.SandboxImage, Workspace: payload.Workspace,
				CPUs: 2, MemoryBytes: 4 << 30, PIDs: 256, TempBytes: 2 << 30,
			},
		)
		if err != nil {
			return nil, err
		}
		modelRunner, err := composeModelRunner(config, payload.Task)
		if err != nil {
			return nil, err
		}
		worker, err := issueagentworker.NewWorker(issueagentworker.WorkerConfig{
			Task: payload.Task, Prompt: prompt, Policy: policy,
			Workspace: payload.Workspace, Runner: sandbox,
			Model: modelRunner, MaxArtifactBytes: payload.MaxArtifactBytes,
		})
		if err != nil {
			return nil, err
		}
		return worker.Run(ctx)
	}
}

func composeModelRunner(
	config IssueAgentWorkerConfig,
	task issueagentcontract.TaskEnvelope,
) (issueagentworker.ModelRunner, error) {
	var adapter issueagentmodel.Adapter
	switch task.Provider {
	case issueagentcontract.ProviderDeepSeek:
		selected, err := issueagentmodel.NewDeepSeekAdapter(
			"https://api.deepseek.com", config.DeepSeekAPIKey, config.HTTPClient,
		)
		if err != nil {
			return nil, err
		}
		adapter = selected
	case issueagentcontract.ProviderCodex:
		runner, err := issueagentmodel.NewCodexCLIRunner(
			issueagentmodel.CodexCLIConfig{
				Binary: config.CodexBinary, APIKey: config.CodexAPIKey,
				MinVersion: config.CodexMinimumVersion,
			},
		)
		if err != nil {
			return nil, err
		}
		selected, err := issueagentmodel.NewCodexAdapter(runner)
		if err != nil {
			return nil, err
		}
		adapter = selected
	default:
		return nil, errors.New("Worker task selects an unsupported provider")
	}
	return func(
		ctx context.Context,
		task issueagentcontract.TaskEnvelope,
		prompt []byte,
		broker *issueagentworker.Broker,
	) (issueagentworker.ModelOutput, error) {
		outcome, err := adapter.Run(ctx, issueagentmodel.Request{
			Task: task, SystemPrompt: string(prompt),
			PromptSHA256: task.PromptDigest, MaxRounds: 16,
			MaxBytes: task.Limits.MaxOutputBytes,
		}, brokerToolExecutor{broker: broker})
		if err != nil {
			return issueagentworker.ModelOutput{}, err
		}
		return issueagentworker.ModelOutput{
			Result: outcome.Result, Usage: outcome.Usage,
		}, nil
	}, nil
}

type brokerToolExecutor struct {
	broker *issueagentworker.Broker
}

func (executor brokerToolExecutor) ExecuteTool(
	ctx context.Context,
	call issueagentmodel.ToolCall,
) (issueagentmodel.ToolResult, error) {
	if executor.broker == nil {
		return issueagentmodel.ToolResult{}, errors.New("tool broker is unavailable")
	}
	var result any
	switch call.Name {
	case "workspace_list":
		var input struct {
			Path       string `json:"path"`
			MaxEntries int    `json:"max_entries"`
		}
		if err := decodeIssueAgentDocument(call.Arguments, &input); err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		value, err := executor.broker.List(ctx, input.Path, input.MaxEntries)
		if err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		result = value
	case "workspace_read":
		var input struct {
			Path string `json:"path"`
		}
		if err := decodeIssueAgentDocument(call.Arguments, &input); err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		value, err := executor.broker.Read(ctx, input.Path)
		if err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		result = value
	case "workspace_search":
		var input struct {
			Literal    string `json:"literal"`
			Path       string `json:"path"`
			MaxMatches int    `json:"max_matches"`
		}
		if err := decodeIssueAgentDocument(call.Arguments, &input); err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		value, err := executor.broker.Search(
			ctx, input.Literal, input.Path, input.MaxMatches,
		)
		if err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		result = value
	case "workspace_apply_patch":
		var input issueagentworker.ApplyRequest
		if err := decodeIssueAgentDocument(call.Arguments, &input); err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		value, err := executor.broker.Apply(ctx, input)
		if err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		result = value
	case "command_run":
		var input struct {
			Argv        []string `json:"argv"`
			WorkingDir  string   `json:"working_dir"`
			TimeoutMS   int64    `json:"timeout_ms"`
			OutputLimit int64    `json:"output_limit"`
		}
		if err := decodeIssueAgentDocument(call.Arguments, &input); err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		value, err := executor.broker.RunCommand(
			ctx,
			issueagentworker.CommandRequest{
				Argv: input.Argv, WorkingDir: input.WorkingDir,
				Timeout:     time.Duration(input.TimeoutMS) * time.Millisecond,
				OutputLimit: input.OutputLimit,
			},
		)
		if err != nil {
			return issueagentmodel.ToolResult{}, err
		}
		result = value
	default:
		return issueagentmodel.ToolResult{}, errors.New("unknown broker tool")
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return issueagentmodel.ToolResult{}, errors.New("tool result is oversized")
	}
	return issueagentmodel.ToolResult{
		ID: call.ID, Content: json.RawMessage(encoded),
	}, nil
}

func publishCheckpoint(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload checkpointPayload,
) (any, error) {
	client, store, previous, err := prepareCheckpointPublication(config, payload)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(ctx, client, store, previous, payload)
}

func prepareCheckpointPublication(
	config IssueAgentGitHubConfig,
	payload checkpointPayload,
) (
	*issueagentgithub.Client,
	*issueagentgithub.CheckpointStore,
	issueagentgithub.VerifiedCheckpoint,
	error,
) {
	if payload.Now.IsZero() ||
		config.Now().Sub(payload.Now) > 5*time.Minute ||
		payload.Now.Sub(config.Now()) > time.Minute {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{},
			errors.New("checkpoint publication clock is stale")
	}
	privateKey, err := base64.StdEncoding.DecodeString(
		config.CheckpointPrivateKeyBase64,
	)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{},
			errors.New("checkpoint private key is unavailable")
	}
	store, err := issueagentgithub.NewCheckpointStore(
		payload.Repository, payload.AppLogin, payload.KeySet,
		issueagentgithub.Signer{
			KeyID:      config.CheckpointKeyID,
			PrivateKey: ed25519.PrivateKey(privateKey),
		},
	)
	if err != nil {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{}, err
	}
	previous, err := store.VerifyChain(
		payload.Comments, payload.IssueNumber, payload.Now,
	)
	if err != nil {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{}, err
	}
	if payload.Checkpoint.Repository != payload.Repository ||
		payload.Checkpoint.IssueNumber != payload.IssueNumber ||
		payload.Checkpoint.Sequence != previous.Checkpoint.Sequence+1 ||
		payload.Checkpoint.ExpectedPreviousCheckpointID == nil ||
		*payload.Checkpoint.ExpectedPreviousCheckpointID != previous.CommentID ||
		payload.Checkpoint.PreviousCheckpointSHA256 == nil ||
		*payload.Checkpoint.PreviousCheckpointSHA256 != previous.Digest {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{},
			errors.New("checkpoint publication predecessor is stale")
	}
	if err := issueagentusecase.ValidateTransition(
		previous.Checkpoint.State, payload.Checkpoint.State,
	); err != nil {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{}, err
	}
	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		Token: config.GitHubToken, MaxPages: 20, MaxBodyBytes: 16 << 20,
	}, config.HTTPClient)
	if err != nil {
		return nil, nil, issueagentgithub.VerifiedCheckpoint{}, err
	}
	return client, store, previous, nil
}

func appendCheckpointProjection(
	ctx context.Context,
	client *issueagentgithub.Client,
	store *issueagentgithub.CheckpointStore,
	_ issueagentgithub.VerifiedCheckpoint,
	payload checkpointPayload,
) (any, error) {
	body, digest, err := store.SignComment(payload.Checkpoint, payload.Summary)
	if err != nil {
		return nil, err
	}
	comment, err := client.CreateIssueComment(ctx, payload.IssueNumber, body)
	if err != nil {
		return nil, err
	}
	if err := client.SetIssueLabels(ctx, payload.IssueNumber, payload.Labels); err != nil {
		return nil, err
	}
	return struct {
		CommentID int64  `json:"comment_id"`
		Digest    string `json:"digest"`
	}{
		CommentID: comment.ID,
		Digest:    digest,
	}, nil
}

func decodeIssueAgentDocument(encoded []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("Issue Agent document contains trailing JSON")
	}
	return nil
}

// NewIssueAgentOperations composes the standalone CLI's narrow operation set.
func NewIssueAgentOperations(dependencies IssueAgentDependencies) issueagentcli.Operations {
	unavailable := func(context.Context, issueagentcli.DocumentRequest) (any, error) {
		return nil, errors.New("Issue Agent operation is not configured")
	}
	if dependencies.PublishLease == nil {
		dependencies.PublishLease = unavailable
	}
	if dependencies.PublishResult == nil {
		dependencies.PublishResult = unavailable
	}
	if dependencies.RunWorker == nil {
		dependencies.RunWorker = unavailable
	}
	if dependencies.VerifyCheckpoint == nil {
		dependencies.VerifyCheckpoint = unavailable
	}
	if dependencies.MintAppToken == nil {
		dependencies.MintAppToken = unavailable
	}
	return issueagentcli.Operations{
		PlanEvent: func(
			_ context.Context,
			request issueagentcli.PlanEventRequest,
		) (any, error) {
			return issueagentusecase.Reconcile(issueagentusecase.ReconcileInput{
				Now: request.Now, ChainStatus: request.ChainStatus,
				Checkpoint:          request.Checkpoint,
				CheckpointCommentID: request.CheckpointCommentID,
				CheckpointDigest:    request.CheckpointDigest,
				Lease:               request.Lease, Artifacts: request.Artifacts,
			}, issueagentusecase.ReconcilePolicy{
				Enabled: request.Enabled, RolloutMode: request.RolloutMode,
			})
		},
		PlanSweep: func(
			_ context.Context,
			request issueagentcli.PlanSweepRequest,
		) (any, error) {
			return issueagentusecase.Schedule(
				request.Now, request.Candidates, request.Active, request.Starts,
				request.Budget, request.LeaseMargin,
			)
		},
		PublishLease:     dependencies.PublishLease,
		PublishResult:    dependencies.PublishResult,
		RunWorker:        dependencies.RunWorker,
		VerifyCheckpoint: dependencies.VerifyCheckpoint,
		MintAppToken:     dependencies.MintAppToken,
	}
}
