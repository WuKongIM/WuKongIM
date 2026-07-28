package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha1" // #nosec G505 -- Git object identity is SHA-1 by protocol.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	PublishLease             func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishResult            func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishDraft             func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishIntake            func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishAuthorization     func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishVersionPin        func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishReproductionLease func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishWorkerArtifact    func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishDraftPR           func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishPhaseLease        func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishRiskAuthorization func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishValidationRequest func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishValidationResult  func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishExpiredLease      func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishCommand           func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishMerge             func(context.Context, issueagentcli.DocumentRequest) (any, error)
	ReadCurrentCheckpoint    func(context.Context, issueagentcli.DocumentRequest) (any, error)
	ReadCurrentTask          func(context.Context, issueagentcli.DocumentRequest) (any, error)
	RunWorker                func(context.Context, issueagentcli.DocumentRequest) (any, error)
	VerifyCheckpoint         func(context.Context, issueagentcli.DocumentRequest) (any, error)
	MintAppToken             func(context.Context, issueagentcli.DocumentRequest) (any, error)
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
	ModuleCache      string                          `json:"module_cache"`
	MaxArtifactBytes int64                           `json:"max_artifact_bytes"`
	AffectedBinary   string                          `json:"affected_binary,omitempty"`
	DiagnosisBinary  string                          `json:"diagnosis_binary,omitempty"`
}

// IssueAgentGitHubConfig contains Publisher-only process dependencies.
type IssueAgentGitHubConfig struct {
	HTTPClient                 *http.Client
	GitHubToken                string
	CheckpointKeyID            string
	CheckpointPrivateKeyBase64 string
	AppPrivateKeyPEM           []byte
	ImageSourceLookup          issueagentgithub.ImageSourceLookup
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

type publishDraftPayload struct {
	checkpointPayload
	DraftPR issueagentgithub.DraftPullRequest `json:"draft_pr"`
}

type publishIntakePayload struct {
	BaseURL            string   `json:"base_url"`
	Repository         string   `json:"repository"`
	AppLogin           string   `json:"app_login"`
	IssueNumber        int64    `json:"issue_number"`
	PossibleDuplicates []string `json:"possible_duplicates"`
}

type publishAuthorizationPayload struct {
	BaseURL      string                  `json:"base_url"`
	Repository   string                  `json:"repository"`
	AppLogin     string                  `json:"app_login"`
	IssueNumber  int64                   `json:"issue_number"`
	KeySet       issueagentgithub.KeySet `json:"key_set"`
	EventID      string                  `json:"event_id"`
	EventAction  string                  `json:"event_action"`
	Label        string                  `json:"label"`
	BeforeLabels []string                `json:"before_labels"`
	Actor        string                  `json:"actor"`
	ActorType    string                  `json:"actor_type"`
	EventAt      time.Time               `json:"event_at"`
}

type publishVersionPinPayload struct {
	BaseURL     string                  `json:"base_url"`
	Repository  string                  `json:"repository"`
	AppLogin    string                  `json:"app_login"`
	IssueNumber int64                   `json:"issue_number"`
	KeySet      issueagentgithub.KeySet `json:"key_set"`
}

type publishReproductionLeasePayload struct {
	BaseURL            string                          `json:"base_url"`
	Repository         string                          `json:"repository"`
	AppLogin           string                          `json:"app_login"`
	IssueNumber        int64                           `json:"issue_number"`
	KeySet             issueagentgithub.KeySet         `json:"key_set"`
	PolicyBase64       string                          `json:"policy_base64"`
	PromptBase64       string                          `json:"prompt_base64"`
	InstructionDigests []issueagentcontract.FileDigest `json:"instruction_digests"`
	Topology           string                          `json:"topology"`
	HarnessPaths       []string                        `json:"harness_paths"`
	Provider           issueagentcontract.Provider     `json:"provider"`
	Model              string                          `json:"model"`
}

type publishWorkerArtifactPayload struct {
	BaseURL                           string                    `json:"base_url"`
	Repository                        string                    `json:"repository"`
	AppLogin                          string                    `json:"app_login"`
	IssueNumber                       int64                     `json:"issue_number"`
	KeySet                            issueagentgithub.KeySet   `json:"key_set"`
	ArtifactRunID                     int64                     `json:"artifact_run_id"`
	ArtifactName                      string                    `json:"artifact_name"`
	Artifact                          issueagentworker.Artifact `json:"artifact"`
	ProtectedPaths                    []string                  `json:"protected_paths"`
	ScenarioInstructionTemplateBase64 string                    `json:"scenario_instruction_template_base64"`
}

type publishDraftPRPayload struct {
	BaseURL     string                  `json:"base_url"`
	Repository  string                  `json:"repository"`
	AppLogin    string                  `json:"app_login"`
	IssueNumber int64                   `json:"issue_number"`
	KeySet      issueagentgithub.KeySet `json:"key_set"`
}

type publishPhaseLeasePayload struct {
	BaseURL            string                           `json:"base_url"`
	Repository         string                           `json:"repository"`
	AppLogin           string                           `json:"app_login"`
	IssueNumber        int64                            `json:"issue_number"`
	KeySet             issueagentgithub.KeySet          `json:"key_set"`
	Phase              issueagentcontract.Phase         `json:"phase"`
	PolicyBase64       string                           `json:"policy_base64"`
	PromptBase64       string                           `json:"prompt_base64"`
	InstructionDigests []issueagentcontract.FileDigest  `json:"instruction_digests"`
	AllowedCommands    []issueagentcontract.CommandRule `json:"allowed_commands"`
	Provider           issueagentcontract.Provider      `json:"provider"`
	Model              string                           `json:"model"`
}

type publishRiskAuthorizationPayload struct {
	BaseURL     string                  `json:"base_url"`
	Repository  string                  `json:"repository"`
	AppLogin    string                  `json:"app_login"`
	IssueNumber int64                   `json:"issue_number"`
	CommentID   int64                   `json:"comment_id"`
	KeySet      issueagentgithub.KeySet `json:"key_set"`
}

type publishValidationRequestPayload struct {
	BaseURL     string                  `json:"base_url"`
	Repository  string                  `json:"repository"`
	AppLogin    string                  `json:"app_login"`
	IssueNumber int64                   `json:"issue_number"`
	KeySet      issueagentgithub.KeySet `json:"key_set"`
}

type publishValidationResultPayload struct {
	BaseURL            string                           `json:"base_url"`
	Repository         string                           `json:"repository"`
	AppLogin           string                           `json:"app_login"`
	IssueNumber        int64                            `json:"issue_number"`
	WorkflowRunID      int64                            `json:"workflow_run_id"`
	KeySet             issueagentgithub.KeySet          `json:"key_set"`
	PolicyBase64       string                           `json:"policy_base64"`
	PromptBase64       string                           `json:"prompt_base64"`
	InstructionDigests []issueagentcontract.FileDigest  `json:"instruction_digests"`
	AllowedCommands    []issueagentcontract.CommandRule `json:"allowed_commands"`
	Provider           issueagentcontract.Provider      `json:"provider"`
	Model              string                           `json:"model"`
}

type publishCommandPayload struct {
	BaseURL            string                           `json:"base_url"`
	Repository         string                           `json:"repository"`
	AppLogin           string                           `json:"app_login"`
	IssueNumber        int64                            `json:"issue_number"`
	CommentID          int64                            `json:"comment_id"`
	KeySet             issueagentgithub.KeySet          `json:"key_set"`
	PolicyBase64       string                           `json:"policy_base64"`
	PromptBase64       string                           `json:"prompt_base64"`
	InstructionDigests []issueagentcontract.FileDigest  `json:"instruction_digests"`
	AllowedCommands    []issueagentcontract.CommandRule `json:"allowed_commands"`
	Provider           issueagentcontract.Provider      `json:"provider"`
	Model              string                           `json:"model"`
}

type readCurrentCheckpointPayload struct {
	BaseURL      string                  `json:"base_url"`
	Repository   string                  `json:"repository"`
	AppLogin     string                  `json:"app_login"`
	IssueNumber  int64                   `json:"issue_number"`
	KeySet       issueagentgithub.KeySet `json:"key_set"`
	PolicyBase64 string                  `json:"policy_base64,omitempty"`
}

type currentCheckpointResult struct {
	CheckpointCommentID int64                         `json:"checkpoint_comment_id"`
	CheckpointDigest    string                        `json:"checkpoint_digest"`
	Checkpoint          issueagentcontract.Checkpoint `json:"checkpoint"`
	IssueBodyChanged    bool                          `json:"issue_body_changed"`
	Plan                *issueagentusecase.Plan       `json:"plan,omitempty"`
}

type readCurrentTaskPayload struct {
	readCurrentCheckpointPayload
	OperationID string `json:"operation_id"`
}

var validationRunTitlePattern = regexp.MustCompile(
	`^Agent PR #([1-9][0-9]*) validation head ([0-9a-f]{40}) merge ` +
		`([0-9a-f]{40}) gate ([1-9][0-9]*) request ([1-9][0-9]*)$`,
)

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
		PublishIntake: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishIntakePayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishIntake(ctx, config, payload)
		},
		PublishAuthorization: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishAuthorizationPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishAuthorization(ctx, config, payload)
		},
		PublishVersionPin: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishVersionPinPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishVersionPin(ctx, config, payload)
		},
		PublishReproductionLease: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishReproductionLeasePayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishReproductionLease(ctx, config, payload)
		},
		PublishWorkerArtifact: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishWorkerArtifactPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishWorkerArtifact(ctx, config, payload)
		},
		PublishDraftPR: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishDraftPRPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishDraftPR(ctx, config, payload)
		},
		PublishPhaseLease: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishPhaseLeasePayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishPhaseLease(ctx, config, payload)
		},
		PublishRiskAuthorization: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishRiskAuthorizationPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishRiskAuthorization(ctx, config, payload)
		},
		PublishValidationRequest: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishValidationRequestPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishValidationRequest(ctx, config, payload)
		},
		PublishValidationResult: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishValidationResultPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishValidationResult(ctx, config, payload)
		},
		PublishExpiredLease: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishExpiredLease(ctx, config, payload)
		},
		PublishCommand: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishCommandPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishCommand(ctx, config, payload)
		},
		PublishMerge: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishMerge(ctx, config, payload)
		},
		ReadCurrentCheckpoint: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return readCurrentCheckpoint(ctx, config, payload)
		},
		ReadCurrentTask: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentTaskPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return readCurrentTask(ctx, config, payload)
		},
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
			switch payload.Result.Phase {
			case issueagentcontract.PhaseReproduce:
				if payload.Result.Reproduction == nil ||
					payload.Checkpoint.Reproduction == nil ||
					payload.Checkpoint.Reproduction.Assertion !=
						payload.Result.Reproduction.Assertion ||
					payload.Checkpoint.Reproduction.AssertionSHA256 !=
						payload.Result.Reproduction.AssertionSHA256 ||
					payload.Checkpoint.Reproduction.Topology !=
						payload.Result.Reproduction.Topology {
					return nil, errors.New("reproduction checkpoint does not match Worker evidence")
				}
			case issueagentcontract.PhaseDiagnose:
				if !reflect.DeepEqual(
					payload.Checkpoint.Diagnosis,
					payload.Result.Diagnosis,
				) {
					return nil, errors.New("diagnosis checkpoint does not match Worker evidence")
				}
			}
			if payload.Validation.AllowReproductionReset &&
				payload.Checkpoint.State != issueagentcontract.StateReproducing {
				return nil, errors.New("reproduction reset is outside a reproducing checkpoint")
			}
			if err := issueagentgithub.ValidatePublish(payload.Validation); err != nil {
				return nil, err
			}
			if payload.DraftPR != nil || payload.ReadyPR != 0 {
				return nil, errors.New(
					"Draft and Ready pull request transitions require dedicated Publisher operations",
				)
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
			return appendCheckpointProjection(
				ctx, client, store, previous, payload.checkpointPayload,
			)
		},
		PublishDraft: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload publishDraftPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			if payload.Checkpoint.State != issueagentcontract.StateDraftPROpen ||
				payload.Checkpoint.NextAction != issueagentcontract.ActionDiagnose ||
				payload.Checkpoint.Work == nil ||
				payload.Checkpoint.Work.PRNumber != 0 ||
				payload.Checkpoint.Work.Branch != payload.DraftPR.Head {
				return nil, errors.New("Draft PR checkpoint is inconsistent")
			}
			client, store, previous, err := prepareCheckpointPublication(
				config, payload.checkpointPayload,
			)
			if err != nil {
				return nil, err
			}
			pull, err := client.EnsureDraftPullRequest(ctx, payload.DraftPR)
			if err != nil {
				return nil, err
			}
			if pull.HeadSHA != payload.Checkpoint.Work.HeadSHA {
				return nil, errors.New("Draft PR head does not match reproduced commit")
			}
			payload.Checkpoint.Work.PRNumber = pull.Number
			payload.checkpointPayload.Checkpoint = payload.Checkpoint
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

func readCurrentCheckpoint(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload readCurrentCheckpointPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("current Issue is unavailable or unauthorized")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := issueagentgithub.NewCheckpointStore(
		payload.Repository, payload.AppLogin, payload.KeySet,
		issueagentgithub.Signer{},
	)
	if err != nil {
		return nil, err
	}
	verified, err := store.VerifyChain(
		comments, payload.IssueNumber, config.Now().UTC(),
	)
	if err != nil {
		return nil, err
	}
	result := currentCheckpointResult{
		CheckpointCommentID: verified.CommentID,
		CheckpointDigest:    verified.Digest,
		Checkpoint:          verified.Checkpoint,
		IssueBodyChanged: verified.Checkpoint.FrozenInput.IssueBodySHA256 !=
			digestIssueBody(issue.Body),
	}
	if payload.PolicyBase64 != "" {
		policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
		if err != nil {
			return nil, errors.New("current-checkpoint policy is invalid")
		}
		policy, err := issueagentusecase.DecodePolicy(
			bytes.NewReader(policyBytes), int64(len(policyBytes)),
		)
		if err != nil {
			return nil, err
		}
		var leaseFacts *issueagentusecase.LeaseFacts
		if lease := verified.Checkpoint.Lease; lease != nil {
			leaseFacts = &issueagentusecase.LeaseFacts{
				OperationID: lease.OperationID,
				TaskDigest:  lease.TaskSHA256,
				Generation:  verified.Checkpoint.Generation,
				ExpiresAt:   lease.ExpiresAt,
			}
		}
		plan, err := issueagentusecase.Reconcile(
			issueagentusecase.ReconcileInput{
				Now:                 config.Now().UTC(),
				ChainStatus:         issueagentusecase.ChainValid,
				Checkpoint:          &verified.Checkpoint,
				CheckpointCommentID: verified.CommentID,
				CheckpointDigest:    verified.Digest,
				Lease:               leaseFacts,
			},
			issueagentusecase.ReconcilePolicy{
				Enabled: policy.Enabled, RolloutMode: policy.RolloutMode,
			},
		)
		if err != nil {
			return nil, err
		}
		result.Plan = &plan
	}
	return result, nil
}

func readCurrentTask(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload readCurrentTaskPayload,
) (any, error) {
	current, err := readCurrentCheckpoint(
		ctx, config, payload.readCurrentCheckpointPayload,
	)
	if err != nil {
		return nil, err
	}
	verified, ok := current.(currentCheckpointResult)
	if !ok {
		return nil, errors.New("current checkpoint projection is invalid")
	}
	if verified.IssueBodyChanged {
		return nil, errors.New("current Issue body differs from its signed task")
	}
	lease := verified.Checkpoint.Lease
	now := config.Now().UTC()
	if lease == nil || lease.OperationID != payload.OperationID ||
		!lease.ExpiresAt.After(now) ||
		verified.Checkpoint.State != issueagentcontract.StateReproducing &&
			verified.Checkpoint.State != issueagentcontract.StateDiagnosing &&
			verified.Checkpoint.State != issueagentcontract.StateFixing {
		return nil, errors.New("current Worker lease is absent, stale, or mismatched")
	}
	taskDigest, err := issueagentcontract.TaskDigest(lease.Task)
	if err != nil || taskDigest != lease.TaskSHA256 {
		return nil, errors.New("current Worker task does not match its signed lease")
	}
	return struct {
		CheckpointCommentID int64                           `json:"checkpoint_comment_id"`
		CheckpointDigest    string                          `json:"checkpoint_digest"`
		Task                issueagentcontract.TaskEnvelope `json:"task"`
		TaskSHA256          string                          `json:"task_sha256"`
		ExpiresAt           time.Time                       `json:"expires_at"`
	}{
		CheckpointCommentID: verified.CheckpointCommentID,
		CheckpointDigest:    verified.CheckpointDigest,
		Task:                lease.Task,
		TaskSHA256:          lease.TaskSHA256,
		ExpiresAt:           lease.ExpiresAt,
	}, nil
}

func publishExpiredLease(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload readCurrentCheckpointPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("expired-lease Issue is unavailable")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) ||
		previous.Checkpoint.Lease == nil ||
		previous.Checkpoint.Lease.ExpiresAt.After(now) {
		return nil, errors.New("current signed Worker lease is not expired")
	}
	policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
	if err != nil {
		return nil, errors.New("expired-lease policy is invalid")
	}
	policy, err := issueagentusecase.DecodePolicy(
		bytes.NewReader(policyBytes), int64(len(policyBytes)),
	)
	if err != nil {
		return nil, errors.New("expired-lease policy is invalid")
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.Lease = nil
	next.Model = nil
	summary := "Recovered an expired Worker lease without accepting any untrusted output."
	if int(next.Budget.InfrastructureRetries) >=
		policy.IssueBudget.MaxInfrastructureRetries {
		next.State = issueagentcontract.StateReadyForHuman
		next.NextAction = issueagentcontract.ActionWaitForHuman
		summary = "Stopped automatic recovery after the bounded infrastructure retry budget."
	} else {
		next.Budget.InfrastructureRetries++
		switch previous.Checkpoint.State {
		case issueagentcontract.StateReproducing:
			next.State = issueagentcontract.StateVersionPinned
			next.NextAction = issueagentcontract.ActionReproduce
		case issueagentcontract.StateDiagnosing:
			next.State = issueagentcontract.StateDraftPROpen
			next.NextAction = issueagentcontract.ActionDiagnose
		case issueagentcontract.StateFixing:
			next.State = issueagentcontract.StateDiagnosed
			next.NextAction = issueagentcontract.ActionImplementFix
		default:
			return nil, errors.New("expired lease is outside a recoverable state")
		}
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next, Summary: summary, Labels: slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishCommand(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishCommandPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" && issue.State != "closed" {
		return nil, errors.New("maintainer-command Issue is unavailable")
	}
	comment, err := client.IssueComment(
		ctx, payload.CommentID, payload.IssueNumber,
	)
	if err != nil || !comment.CreatedAt.Equal(comment.UpdatedAt) {
		return nil, errors.New("maintainer command is stale or edited")
	}
	permission, err := client.ActorPermission(ctx, comment.Author)
	if err != nil {
		return nil, err
	}
	policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
	if err != nil {
		return nil, errors.New("maintainer-command policy is invalid")
	}
	policy, err := issueagentusecase.DecodePolicy(
		bytes.NewReader(policyBytes), int64(len(policyBytes)),
	)
	if err != nil {
		return nil, err
	}
	intent, err := issueagentusecase.ParseCommand(
		comment.Body,
		issueagentusecase.CommandActor{
			Login: comment.Author, Type: comment.AuthorType,
			Permission: issueagentusecase.Permission(permission),
		},
		issueagentusecase.CommandPolicy{
			AllowedBackportBranches: policy.AllowedBackportBranches,
		},
	)
	if err != nil || intent.Kind == issueagentusecase.CommandApproveRisk {
		return nil, errors.New("maintainer command uses a different trusted route")
	}
	if issue.State != "open" &&
		intent.Kind != issueagentusecase.CommandBackport &&
		intent.Kind != issueagentusecase.CommandRecoverChain {
		return nil, errors.New("maintainer command requires an open Issue")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	if intent.Kind == issueagentusecase.CommandRecoverChain {
		return publishChainRecovery(
			ctx, payload, issue, comment, comments, store, client, intent, now,
		)
	}
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if intent.Kind != issueagentusecase.CommandRevise &&
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("edited Issue text requires /agent revise first")
	}
	eventID := "comment-" + strconv.FormatInt(comment.ID, 10)
	facts := issueagentusecase.CommandFacts{
		CommandEventID:   eventID,
		CurrentCommentID: previous.CommentID,
		CurrentDigest:    previous.Digest,
	}
	switch intent.Kind {
	case issueagentusecase.CommandRevise:
		intake, err := issueagentusecase.PlanIntake(issue.Body, nil)
		if err != nil || !intake.Complete {
			return nil, errors.New("revision requires a complete current Bug form")
		}
		main, err := client.DefaultBranchHead(ctx, "main")
		if err != nil {
			return nil, err
		}
		facts.IssueBodySHA256 = digestIssueBody(issue.Body)
		facts.AffectedVersion = intake.Form.AffectedVersion
		facts.AcceptedCommentIDs = []int64{comment.ID}
		facts.DiagnosisBaseSHA = main.SHA
	case issueagentusecase.CommandAddressReview:
		if previous.Checkpoint.State != issueagentcontract.StateReadyForReview ||
			previous.Checkpoint.Work == nil {
			return nil, errors.New("address-review requires reviewed Agent work")
		}
		facts.UnresolvedThreadIDs, err = client.UnresolvedReviewThreadIDs(
			ctx, previous.Checkpoint.Work.PRNumber,
		)
		if err != nil {
			return nil, err
		}
	case issueagentusecase.CommandAdoptHead:
		if previous.Checkpoint.Work == nil {
			return nil, errors.New("adopt-head requires Agent work")
		}
		ref, err := client.Ref(ctx, previous.Checkpoint.Work.Branch)
		if err != nil {
			return nil, err
		}
		facts.CurrentExternalHead = ref.SHA
	case issueagentusecase.CommandBackport:
		if previous.Checkpoint.Work == nil {
			return nil, errors.New("backport requires merged Agent work")
		}
		target, err := client.BranchHead(ctx, intent.BackportBranch)
		if err != nil {
			return nil, err
		}
		facts.MergedPRNumber = previous.Checkpoint.Work.PRNumber
		facts.TargetBranch = target.Name
		facts.TargetHeadSHA = target.SHA
	case issueagentusecase.CommandCancel:
	default:
		return nil, errors.New("maintainer command is unsupported")
	}
	plan, err := issueagentusecase.PlanCommand(
		previous.Checkpoint, intent, facts,
	)
	if err != nil {
		return nil, err
	}
	control := &issueagentcontract.ControlAudit{
		Kind: string(intent.Kind), EventID: eventID, Actor: comment.Author,
		CommentID: comment.ID,
	}
	next := previous.Checkpoint
	next.Generation = plan.NewGeneration
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.Lease = nil
	next.Control = control
	var task issueagentcontract.TaskEnvelope
	summary := "Applied freshly authorized maintainer command /agent " +
		strings.ReplaceAll(string(intent.Kind), "_", "-") + "."
	switch intent.Kind {
	case issueagentusecase.CommandRevise:
		next = *plan.RevisedCheckpoint
		next.Control = control
	case issueagentusecase.CommandCancel:
		next.State = issueagentcontract.StateCancelled
		next.NextAction = issueagentcontract.ActionNone
	case issueagentusecase.CommandAdoptHead:
		if previous.Checkpoint.Work == nil {
			return nil, errors.New("adopt-head work disappeared")
		}
		pull, err := client.PullRequest(
			ctx, previous.Checkpoint.Work.PRNumber,
		)
		if err != nil || pull.State != "open" ||
			pull.HeadRef != previous.Checkpoint.Work.Branch ||
			pull.HeadSHA != plan.AdoptedHeadSHA {
			return nil, errors.New("adopt-head Draft PR is not exact")
		}
		pull, err = client.EnsurePullRequestDraft(
			ctx, pull.Number, plan.AdoptedHeadSHA,
		)
		if err != nil || !pull.Draft {
			return nil, errors.New("adopt-head PR could not return to Draft")
		}
		control.AdoptedHeadSHA = plan.AdoptedHeadSHA
		next.State = issueagentcontract.StateValidating
		next.Work = &issueagentcontract.Work{
			Branch:   previous.Checkpoint.Work.Branch,
			HeadSHA:  plan.AdoptedHeadSHA,
			PRNumber: previous.Checkpoint.Work.PRNumber,
		}
		next.Validation = nil
		next.NextAction = issueagentcontract.ActionValidate
	case issueagentusecase.CommandAddressReview:
		if !policyAllowsAutomatedRemediation(policy, payload.IssueNumber) ||
			previous.Checkpoint.Reproduction == nil ||
			previous.Checkpoint.Diagnosis == nil ||
			previous.Checkpoint.Work == nil {
			return nil, errors.New("address-review is outside rollout or evidence policy")
		}
		prompt, err := decodeCanonicalBase64(payload.PromptBase64, 128<<10)
		if err != nil {
			return nil, errors.New("address-review prompt is invalid")
		}
		pull, err := client.EnsurePullRequestDraft(
			ctx, previous.Checkpoint.Work.PRNumber,
			previous.Checkpoint.Work.HeadSHA,
		)
		if err != nil || !pull.Draft ||
			pull.HeadRef != previous.Checkpoint.Work.Branch {
			return nil, errors.New("address-review PR could not return to exact Draft")
		}
		operationID := issueagentusecase.OperationID(
			payload.Repository, payload.IssueNumber, next.Generation,
			next.Sequence, issueagentcontract.PhaseAddressReview,
		)
		task, err = issueagentusecase.BuildAddressReviewTask(
			issueagentusecase.PhaseTaskInput{
				Repository: payload.Repository, IssueNumber: payload.IssueNumber,
				Generation: next.Generation, Sequence: next.Sequence,
				OperationID: operationID, CheckpointDigest: previous.Digest,
				PolicyDigest:       digestPayload(policyBytes),
				PromptDigest:       digestPayload(prompt),
				Versions:           previous.Checkpoint.Versions,
				CandidateSHA:       previous.Checkpoint.Work.HeadSHA,
				FrozenIssue:        issue.Body,
				AcceptedCommentIDs: previous.Checkpoint.FrozenInput.AcceptedCommentIDs,
				InstructionDigests: payload.InstructionDigests,
				Provider:           payload.Provider, Model: payload.Model,
			},
			*previous.Checkpoint.Diagnosis,
			*previous.Checkpoint.Reproduction,
			plan.ReviewThreadIDs,
			payload.AllowedCommands,
		)
		if err != nil {
			return nil, err
		}
		if err := ensureIssueWorkerBudget(
			previous.Checkpoint, policy, 95*time.Minute,
			previous.Checkpoint.Budget.RemediationAttempts,
			policy.IssueBudget.MaxRemediationAttempts,
		); err != nil {
			return nil, err
		}
		if err := ensureRepositoryWorkerCapacity(
			ctx, client, store, now, 95*time.Minute, true,
		); err != nil {
			return nil, err
		}
		taskDigest, err := issueagentcontract.TaskDigest(task)
		if err != nil {
			return nil, err
		}
		control.ReviewThreadIDs = append(
			[]string(nil), plan.ReviewThreadIDs...,
		)
		next.State = issueagentcontract.StateFixing
		next.Validation = nil
		next.Lease = &issueagentcontract.Lease{
			OperationID: operationID, Workflow: "issue-agent-run.yml",
			DispatchRequestID: operationID,
			Phase:             issueagentcontract.PhaseAddressReview,
			IssuedAt:          now, ExpiresAt: now.Add(95 * time.Minute),
			TaskSHA256: taskDigest, Task: task,
			ReservedSeconds: uint64((95 * time.Minute).Seconds()), Heavy: true,
		}
		next.Budget.RemediationAttempts++
		next.NextAction = issueagentcontract.ActionImplementFix
	case issueagentusecase.CommandBackport:
		title := "[Backport] Issue #" +
			strconv.FormatInt(payload.IssueNumber, 10) + " to " +
			plan.Backport.TargetBranch
		body := "Backport the merged main fix from #" +
			strconv.FormatInt(payload.IssueNumber, 10) + " / PR #" +
			strconv.FormatInt(plan.Backport.SourcePR, 10) + " onto `" +
			plan.Backport.TargetBranch + "` at `" +
			plan.Backport.TargetHeadSHA + "`.\n\n" +
			"This is an independent human-owned tracking Issue."
		child, err := client.EnsureTrackingIssue(ctx, title, body)
		if err != nil {
			return nil, err
		}
		control.BackportBranch = plan.Backport.TargetBranch
		control.ChildIssueNumber = child
		next.State = issueagentcontract.StateMerged
		next.NextAction = issueagentcontract.ActionNone
		summary += " Created independent backport Issue #" +
			strconv.FormatInt(child, 10) + "."
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next, Summary: summary, Labels: slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	projection, err := appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
	if err != nil {
		return nil, err
	}
	return struct {
		Projection any                             `json:"projection"`
		Task       issueagentcontract.TaskEnvelope `json:"task,omitempty"`
	}{
		Projection: projection, Task: task,
	}, nil
}

func publishChainRecovery(
	ctx context.Context,
	payload publishCommandPayload,
	issue issueagentgithub.IssueFacts,
	command issueagentgithub.IssueComment,
	comments []issueagentgithub.IssueComment,
	store *issueagentgithub.CheckpointStore,
	client *issueagentgithub.Client,
	intent issueagentusecase.CommandIntent,
	now time.Time,
) (any, error) {
	prefix := make([]issueagentgithub.IssueComment, 0)
	quarantine := make([]issueagentgithub.IssueComment, 0)
	for _, candidate := range comments {
		switch {
		case candidate.ID <= intent.CheckpointCommentID:
			prefix = append(prefix, candidate)
		case candidate.ID < command.ID &&
			candidate.Author == payload.AppLogin &&
			candidate.AuthorType == "Bot" &&
			strings.Contains(
				candidate.Body,
				"<!-- wukongim-issue-agent-checkpoint:v1\n",
			):
			quarantine = append(quarantine, candidate)
		}
	}
	anchor, err := store.VerifyChain(prefix, payload.IssueNumber, now)
	if err != nil || anchor.CommentID != intent.CheckpointCommentID ||
		anchor.Digest != intent.CheckpointDigest {
		return nil, errors.New("recovery anchor is not the exact last valid checkpoint")
	}
	if anchor.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("recovery requires the frozen Issue body")
	}
	slices.SortFunc(quarantine, func(left, right issueagentgithub.IssueComment) int {
		switch {
		case left.ID < right.ID:
			return -1
		case left.ID > right.ID:
			return 1
		default:
			return 0
		}
	})
	quarantineIDs := make([]int64, 0, len(quarantine))
	for _, candidate := range quarantine {
		quarantineIDs = append(quarantineIDs, candidate.ID)
	}
	quarantineDigest, err := issueagentgithub.QuarantineDigest(quarantine)
	if err != nil {
		return nil, err
	}
	eventID := "comment-" + strconv.FormatInt(command.ID, 10)
	plan, err := issueagentusecase.PlanCommand(
		anchor.Checkpoint, intent, issueagentusecase.CommandFacts{
			CommandEventID:        eventID,
			LastValidCommentID:    anchor.CommentID,
			LastValidDigest:       anchor.Digest,
			QuarantinedCommentIDs: quarantineIDs,
			QuarantineDigest:      quarantineDigest,
		},
	)
	if err != nil {
		return nil, err
	}
	next := anchor.Checkpoint
	next.Generation++
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &anchor.CommentID
	next.PreviousCheckpointSHA256 = &anchor.Digest
	next.Lease = nil
	next.Model = nil
	next.Control = &issueagentcontract.ControlAudit{
		Kind:    string(issueagentusecase.CommandRecoverChain),
		EventID: eventID, Actor: command.Author, CommentID: command.ID,
		RecoveryAnchorCommentID: plan.Recovery.AnchorCommentID,
		RecoveryAnchorDigest:    plan.Recovery.AnchorDigest,
		QuarantinedCommentIDs: append(
			[]int64(nil), plan.Recovery.QuarantinedCommentIDs...,
		),
		QuarantineDigest: plan.Recovery.QuarantineDigest,
	}
	switch anchor.Checkpoint.State {
	case issueagentcontract.StateReproducing:
		next.State = issueagentcontract.StateVersionPinned
		next.NextAction = issueagentcontract.ActionReproduce
	case issueagentcontract.StateDiagnosing:
		next.State = issueagentcontract.StateDraftPROpen
		next.NextAction = issueagentcontract.ActionDiagnose
	case issueagentcontract.StateFixing:
		next.State = issueagentcontract.StateDiagnosed
		next.NextAction = issueagentcontract.ActionImplementFix
	}
	body, digest, err := store.SignComment(
		next,
		"Admin recovered the signed chain from exact anchor #"+
			strconv.FormatInt(anchor.CommentID, 10)+
			" and quarantined "+strconv.Itoa(len(quarantine))+" App marker(s).",
	)
	if err != nil {
		return nil, err
	}
	created, err := client.CreateIssueComment(ctx, payload.IssueNumber, body)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	if err := client.SetIssueLabels(
		ctx, payload.IssueNumber, slices.Compact(labels),
	); err != nil {
		return nil, err
	}
	return struct {
		CommentID int64  `json:"comment_id"`
		Digest    string `json:"digest"`
	}{
		CommentID: created.ID, Digest: digest,
	}, nil
}

func publishMerge(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload readCurrentCheckpointPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" && issue.State != "closed" {
		return nil, errors.New("merge-observation Issue is unavailable")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.State != issueagentcontract.StateReadyForReview ||
		previous.Checkpoint.Work == nil ||
		previous.Checkpoint.Validation == nil {
		return nil, errors.New("merge observation requires validated review-ready work")
	}
	pull, err := client.PullRequest(
		ctx, previous.Checkpoint.Work.PRNumber,
	)
	if err != nil || pull.State != "closed" || !pull.Merged ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != previous.Checkpoint.Work.HeadSHA ||
		pull.MergeCommit == "" {
		return nil, errors.New("GitHub does not report the exact Agent PR as merged")
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateMerged
	next.Lease = nil
	next.NextAction = issueagentcontract.ActionNone
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Observed the exact human-merged Agent PR and recorded terminal merged state.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishVersionPin(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishVersionPinPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("version pinning Issue is unavailable or unauthorized")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.State != issueagentcontract.StateAuthorized ||
		previous.Checkpoint.NextAction != issueagentcontract.ActionPinVersions ||
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("version pinning checkpoint or frozen Issue body is stale")
	}
	imageLookup := config.ImageSourceLookup
	if imageLookup == nil {
		imageLookup = func(
			context.Context, string,
		) (issueagentusecase.ImageSource, error) {
			return issueagentusecase.ImageSource{},
				errors.New("verified image metadata lookup is unavailable")
		}
	}
	resolver, err := issueagentgithub.NewVersionSourceResolver(client, imageLookup)
	if err != nil {
		return nil, err
	}
	versions, err := issueagentusecase.ResolveVersions(
		ctx, resolver, previous.Checkpoint.Versions.ReportedRef,
		previous.Checkpoint.Versions.DiagnosisBaseSHA,
	)
	if err != nil {
		return nil, err
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateVersionPinned
	next.Versions = versions
	next.NextAction = issueagentcontract.ActionReproduce
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Pinned the reported version and authorization-time diagnosis baseline to immutable commits.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishReproductionLease(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishReproductionLeasePayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("reproduction Issue is unavailable or unauthorized")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.State != issueagentcontract.StateVersionPinned ||
		previous.Checkpoint.NextAction != issueagentcontract.ActionReproduce ||
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("reproduction lease checkpoint or frozen Issue body is stale")
	}
	policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
	if err != nil {
		return nil, errors.New("reproduction policy is invalid")
	}
	policy, err := issueagentusecase.DecodePolicy(
		bytes.NewReader(policyBytes), int64(len(policyBytes)),
	)
	if err != nil || !policyAllowsReproduction(policy) {
		return nil, errors.New("reproduction is outside the current rollout policy")
	}
	if err := ensureIssueWorkerBudget(
		previous.Checkpoint, policy, 95*time.Minute,
		previous.Checkpoint.Budget.ReproductionAttempts,
		policy.IssueBudget.MaxReproductionAttempts,
	); err != nil {
		return publishIssueWorkerBudgetStop(
			ctx, config, payload.BaseURL, payload.Repository,
			payload.AppLogin, payload.IssueNumber, payload.KeySet,
			issue.Labels, comments, previous,
		)
	}
	if err := ensureRepositoryWorkerCapacity(
		ctx, client, store, now, 95*time.Minute, true,
	); err != nil {
		return nil, err
	}
	prompt, err := decodeCanonicalBase64(payload.PromptBase64, 128<<10)
	if err != nil {
		return nil, errors.New("reproduction prompt is invalid")
	}
	nextSequence := previous.Checkpoint.Sequence + 1
	operationID := issueagentusecase.OperationID(
		payload.Repository, payload.IssueNumber,
		previous.Checkpoint.Generation, nextSequence,
		issueagentcontract.PhaseReproduce,
	)
	task, err := issueagentusecase.BuildReproductionTask(
		issueagentusecase.ReproductionTaskInput{
			Repository: payload.Repository, IssueNumber: payload.IssueNumber,
			Generation: previous.Checkpoint.Generation, Sequence: nextSequence,
			OperationID: operationID, CheckpointDigest: previous.Digest,
			PolicyDigest: digestPayload(policyBytes), PromptDigest: digestPayload(prompt),
			Versions: previous.Checkpoint.Versions, FrozenIssue: issue.Body,
			AcceptedCommentIDs: previous.Checkpoint.FrozenInput.AcceptedCommentIDs,
			InstructionDigests: payload.InstructionDigests,
			Topology:           payload.Topology, HarnessPaths: payload.HarnessPaths,
			Provider: payload.Provider, Model: payload.Model,
		},
	)
	if err != nil {
		return nil, err
	}
	taskDigest, err := issueagentcontract.TaskDigest(task)
	if err != nil {
		return nil, err
	}
	next := previous.Checkpoint
	next.Sequence = nextSequence
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateReproducing
	next.Lease = &issueagentcontract.Lease{
		OperationID: operationID, Workflow: "issue-agent-run.yml",
		DispatchRequestID: operationID, Phase: issueagentcontract.PhaseReproduce,
		IssuedAt: now, ExpiresAt: now.Add(95 * time.Minute),
		TaskSHA256: taskDigest, Task: task,
		ReservedSeconds: uint64((95 * time.Minute).Seconds()), Heavy: true,
	}
	next.Budget.ReproductionAttempts++
	next.NextAction = issueagentcontract.ActionReproduce
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Leased one bounded, credential-free E2E reproduction Worker.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	projection, err := appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
	if err != nil {
		return nil, err
	}
	return struct {
		Projection any                             `json:"projection"`
		Task       issueagentcontract.TaskEnvelope `json:"task"`
		TaskSHA256 string                          `json:"task_sha256"`
	}{
		Projection: projection, Task: task, TaskSHA256: taskDigest,
	}, nil
}

func publishWorkerArtifact(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishWorkerArtifactPayload,
) (any, error) {
	if err := issueagentworker.ValidateArtifact(payload.Artifact); err != nil {
		return nil, err
	}
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("Worker-result Issue is unavailable or unauthorized")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	lease := previous.Checkpoint.Lease
	taskDigest, err := issueagentcontract.TaskDigest(payload.Artifact.Task)
	if err != nil || lease == nil ||
		!lease.ExpiresAt.After(now) || lease.TaskSHA256 != taskDigest ||
		!reflect.DeepEqual(lease.Task, payload.Artifact.Task) ||
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("Worker Artifact does not match the current signed lease")
	}
	if payload.ArtifactRunID <= 0 ||
		payload.ArtifactName != "issue-agent-result-"+lease.OperationID[7:23] {
		return nil, errors.New("Worker Artifact run or name is not lease-bound")
	}
	if payload.Artifact.Result.Status == issueagentcontract.ResultStatusFailed &&
		payload.Artifact.Result.Failure != nil &&
		(payload.Artifact.Result.Failure.Class == issueagentcontract.FailureProvider ||
			payload.Artifact.Result.Failure.Class ==
				issueagentcontract.FailureWorkerInfrastructure) {
		return publishFailedWorkerArtifact(
			ctx, config, payload, issue.Labels, comments, previous, now,
		)
	}
	switch payload.Artifact.Task.Phase {
	case issueagentcontract.PhaseDiagnose:
		if previous.Checkpoint.State != issueagentcontract.StateDiagnosing ||
			lease.Phase != issueagentcontract.PhaseDiagnose {
			return nil, errors.New("diagnosis Artifact is outside a diagnosis lease")
		}
		return publishDiagnosisArtifact(
			ctx, config, payload, issue.Labels, comments, previous, now,
		)
	case issueagentcontract.PhaseFix:
		if previous.Checkpoint.State != issueagentcontract.StateFixing ||
			lease.Phase != issueagentcontract.PhaseFix {
			return nil, errors.New("fix Artifact is outside a remediation lease")
		}
		return publishFixArtifact(
			ctx, config, payload, issue.Labels, comments, previous, client, now,
		)
	case issueagentcontract.PhaseAddressReview:
		if previous.Checkpoint.State != issueagentcontract.StateFixing ||
			lease.Phase != issueagentcontract.PhaseAddressReview {
			return nil, errors.New("review Artifact is outside its exact lease")
		}
		return publishFixArtifact(
			ctx, config, payload, issue.Labels, comments, previous, client, now,
		)
	case issueagentcontract.PhaseReproduce:
		if previous.Checkpoint.State != issueagentcontract.StateReproducing ||
			lease.Phase != issueagentcontract.PhaseReproduce {
			return nil, errors.New("reproduction Artifact is outside a reproduction lease")
		}
	default:
		return nil, errors.New("Worker Artifact phase is not publishable")
	}
	testFiles, err := reproductionTestFiles(payload.Artifact.Result.ChangeSet)
	if err != nil {
		return nil, err
	}
	affected, diagnosisBase, err := reproductionObservations(payload.Artifact)
	if err != nil {
		return nil, err
	}
	evaluation, err := issueagentusecase.EvaluateReproduction(
		previous.Checkpoint.Versions, payload.Artifact.Task.RequiredTopology,
		affected, diagnosisBase, payload.ArtifactRunID, payload.ArtifactName,
		payload.Artifact.SHA256, testFiles,
	)
	if err != nil {
		return nil, err
	}
	if evaluation.Decision != issueagentusecase.ReproductionConfirmed &&
		evaluation.Decision != issueagentusecase.ReproductionAlreadyFixed {
		return nil, errors.New("Worker Artifact is not a publishable reproduction result")
	}

	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.Lease = nil
	next.Reproduction = evaluation.Evidence
	next.Model = workerModelAttempt(
		payload.Artifact, string(evaluation.Decision), &next.Budget,
	)

	if evaluation.Decision == issueagentusecase.ReproductionConfirmed {
		template, err := decodeCanonicalBase64(
			payload.ScenarioInstructionTemplateBase64, 64<<10,
		)
		if err != nil {
			return nil, errors.New("scenario instruction template is invalid")
		}
		parent, err := client.Commit(ctx, previous.Checkpoint.Versions.DiagnosisBaseSHA)
		if err != nil {
			return nil, err
		}
		existingPaths := make(map[string]bool, len(payload.Artifact.Result.ChangeSet.Files))
		for _, file := range payload.Artifact.Result.ChangeSet.Files {
			entry, exists, err := client.ResolveTreePath(ctx, parent.TreeSHA, file.Path)
			if err != nil {
				return nil, err
			}
			if exists && entry.Type != "blob" {
				return nil, errors.New("Worker change targets a non-file Git path")
			}
			existingPaths[file.Path] = exists
		}
		branch := "agent/issue-" + strconv.FormatInt(payload.IssueNumber, 10)
		validation := issueagentgithub.PublishValidation{
			IssueNumber: payload.IssueNumber, Branch: branch, BaseBranch: "main",
			ExpectedParentSHA: previous.Checkpoint.Versions.DiagnosisBaseSHA,
			ChangeSet:         payload.Artifact.Result.ChangeSet,
			Limits: issueagentcontract.ChangeSetLimits{
				MaxFiles:      payload.Artifact.Task.Limits.MaxFiles,
				MaxFileBytes:  int(payload.Artifact.Task.Limits.MaxFileBytes),
				MaxTotalBytes: int(payload.Artifact.Task.Limits.MaxTotalBytes),
				MaxDeletions:  0,
			},
			ProtectedPaths: payload.ProtectedPaths,
			AllowedPaths:   payload.Artifact.Task.AllowedPaths,
			ExistingPaths:  existingPaths, FrozenFileSHA256: map[string]string{},
			ScenarioInstructionTemplate: template,
		}
		if err := issueagentgithub.ValidatePublish(validation); err != nil {
			return nil, err
		}
		published, err := publishOrReuseAgentCommit(
			ctx, client, branch, parent.TreeSHA,
			previous.Checkpoint.Versions.DiagnosisBaseSHA,
			"test(e2e): reproduce issue #"+strconv.FormatInt(payload.IssueNumber, 10),
			payload.Artifact.Result.ChangeSet, existingPaths, true,
		)
		if err != nil {
			return nil, err
		}
		next.State = issueagentcontract.StateReproduced
		next.Work = &issueagentcontract.Work{
			Branch: branch, HeadSHA: published.CommitSHA,
		}
		next.NextAction = issueagentcontract.ActionOpenDraftPR
	} else {
		next.State = issueagentcontract.StateAlreadyFixed
		next.Work = nil
		next.NextAction = issueagentcontract.ActionNone
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Published an exact two-baseline, three-run E2E reproduction decision.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishFailedWorkerArtifact(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishWorkerArtifactPayload,
	issueLabels []string,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
	now time.Time,
) (any, error) {
	result := payload.Artifact.Result
	if result.Failure == nil || len(result.ChangeSet.Files) != 0 ||
		result.RequestedState != issueagentcontract.StateReadyForHuman ||
		result.RequestedAction != issueagentcontract.ActionWaitForHuman {
		return nil, errors.New("failed Worker Artifact is not safely publishable")
	}
	if err := issueagentcontract.ValidateTransition(
		previous.Checkpoint.State, issueagentcontract.StateReadyForHuman,
	); err != nil {
		return nil, err
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateReadyForHuman
	next.Lease = nil
	next.Model = workerModelAttempt(
		payload.Artifact, string(result.Failure.Class), &next.Budget,
	)
	next.NextAction = issueagentcontract.ActionWaitForHuman
	labels := append([]string(nil), issueLabels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary: "Recorded a classified " + string(result.Failure.Class) +
			" Worker failure without treating it as an infrastructure lease expiry.",
		Labels: slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishDiagnosisArtifact(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishWorkerArtifactPayload,
	issueLabels []string,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
	now time.Time,
) (any, error) {
	result := payload.Artifact.Result
	if result.Status != issueagentcontract.ResultStatusSuccess ||
		result.RequestedState != issueagentcontract.StateDiagnosed ||
		result.RequestedAction != issueagentcontract.ActionImplementFix ||
		result.Diagnosis == nil || len(result.ChangeSet.Files) != 0 {
		return nil, errors.New("diagnosis Worker did not return a publishable causal checkpoint")
	}
	diagnosis := *result.Diagnosis
	pathRisk, err := issueagentusecase.ClassifyRisk(issueagentusecase.RiskInput{
		Paths: diagnosis.IntendedPaths,
	})
	if err != nil {
		return nil, err
	}
	diagnosis.RiskClasses = append(diagnosis.RiskClasses, pathRisk.Classes...)
	slices.Sort(diagnosis.RiskClasses)
	diagnosis.RiskClasses = slices.Compact(diagnosis.RiskClasses)
	if diagnosis.AuthorizationEvent != "" {
		return nil, errors.New("Worker cannot supply a risk authorization event")
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateDiagnosed
	next.Lease = nil
	next.Diagnosis = &diagnosis
	next.Model = workerModelAttempt(payload.Artifact, "diagnosed", &next.Budget)
	next.NextAction = issueagentcontract.ActionImplementFix
	labels := append([]string(nil), issueLabels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Published the mandatory causal diagnosis and deterministic risk classification.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishFixArtifact(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishWorkerArtifactPayload,
	issueLabels []string,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
	client *issueagentgithub.Client,
	now time.Time,
) (any, error) {
	result := payload.Artifact.Result
	if result.Status != issueagentcontract.ResultStatusSuccess ||
		result.RequestedState != issueagentcontract.StateValidating ||
		result.RequestedAction != issueagentcontract.ActionValidate ||
		previous.Checkpoint.Diagnosis == nil ||
		previous.Checkpoint.Work == nil || len(result.ChangeSet.Files) == 0 {
		return nil, errors.New("fix Worker did not return a publishable candidate")
	}
	risk, err := issueagentusecase.ClassifyRisk(riskInputFromChangeSet(result.ChangeSet))
	if err != nil {
		return nil, err
	}
	if risk.HumanOnly ||
		!allRiskClassesAuthorized(
			risk.Classes, previous.Checkpoint.Diagnosis.RiskClasses,
			previous.Checkpoint.Diagnosis.AuthorizationEvent,
		) {
		return nil, errors.New("fix ChangeSet exceeds the signed risk authorization")
	}
	parentSHA := previous.Checkpoint.Work.HeadSHA
	parent, err := client.Commit(ctx, parentSHA)
	if err != nil {
		return nil, err
	}
	existingPaths := make(map[string]bool, len(result.ChangeSet.Files))
	for _, file := range result.ChangeSet.Files {
		entry, exists, err := client.ResolveTreePath(ctx, parent.TreeSHA, file.Path)
		if err != nil {
			return nil, err
		}
		if exists && entry.Type != "blob" {
			return nil, errors.New("fix targets a non-file Git path")
		}
		existingPaths[file.Path] = exists
	}
	validation := issueagentgithub.PublishValidation{
		IssueNumber: payload.IssueNumber,
		Branch:      previous.Checkpoint.Work.Branch, BaseBranch: "main",
		ExpectedParentSHA: parentSHA, ChangeSet: result.ChangeSet,
		Limits: issueagentcontract.ChangeSetLimits{
			MaxFiles:      payload.Artifact.Task.Limits.MaxFiles,
			MaxFileBytes:  int(payload.Artifact.Task.Limits.MaxFileBytes),
			MaxTotalBytes: int(payload.Artifact.Task.Limits.MaxTotalBytes),
			MaxDeletions:  payload.Artifact.Task.Limits.MaxFiles,
		},
		ProtectedPaths: payload.ProtectedPaths,
		AllowedPaths:   payload.Artifact.Task.AllowedPaths,
		ExistingPaths:  existingPaths,
		ImmutablePaths: reproductionPaths(
			previous.Checkpoint.Reproduction.TestFiles,
		),
	}
	if err := issueagentgithub.ValidatePublish(validation); err != nil {
		return nil, err
	}
	published, err := publishOrReuseAgentCommit(
		ctx, client, previous.Checkpoint.Work.Branch, parent.TreeSHA, parentSHA,
		"fix: resolve issue #"+strconv.FormatInt(payload.IssueNumber, 10),
		result.ChangeSet, existingPaths, true,
	)
	if err != nil {
		return nil, err
	}
	pull, err := client.PullRequest(ctx, previous.Checkpoint.Work.PRNumber)
	if err != nil || pull.State != "open" || !pull.Draft ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != published.CommitSHA {
		return nil, errors.New("Draft PR did not advance to the exact fix candidate")
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateValidating
	next.Lease = nil
	next.Work = &issueagentcontract.Work{
		Branch: previous.Checkpoint.Work.Branch, HeadSHA: published.CommitSHA,
		PRNumber: previous.Checkpoint.Work.PRNumber,
	}
	next.Model = workerModelAttempt(payload.Artifact, "fixed", &next.Budget)
	next.NextAction = issueagentcontract.ActionValidate
	labels := append([]string(nil), issueLabels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Published the bounded fix candidate after exact local build, related tests, and three E2E passes.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func workerModelAttempt(
	artifact issueagentworker.Artifact,
	terminal string,
	budget *issueagentcontract.Budget,
) *issueagentcontract.ModelAttempt {
	elapsedMS := workerElapsedMilliseconds(artifact.Tools)
	if elapsedMS == 0 {
		elapsedMS = 1
	}
	if budget != nil {
		budget.WorkerSeconds += (elapsedMS + 999) / 1000
	}
	return &issueagentcontract.ModelAttempt{
		Provider:       artifact.Result.Usage.Provider,
		Model:          artifact.Result.Usage.Model,
		AdapterVersion: "v1", PromptPolicyVersion: "v1",
		InputTokens:         artifact.Result.Usage.InputTokens,
		OutputTokens:        artifact.Result.Usage.OutputTokens,
		ElapsedMilliseconds: elapsedMS, TerminalResult: terminal,
	}
}

func riskInputFromChangeSet(
	changeSet issueagentcontract.ChangeSet,
) issueagentusecase.RiskInput {
	input := issueagentusecase.RiskInput{
		Paths: make([]string, 0, len(changeSet.Files)),
	}
	for _, file := range changeSet.Files {
		filePath := strings.ToLower(file.Path)
		input.Paths = append(input.Paths, file.Path)
		input.PublicProtocolChanged = input.PublicProtocolChanged ||
			strings.HasPrefix(filePath, "pkg/wkproto/") ||
			strings.HasPrefix(filePath, "pkg/wknet/")
		input.PersistentFormatChanged = input.PersistentFormatChanged ||
			strings.HasPrefix(filePath, "pkg/wkdb/") ||
			strings.Contains(filePath, "migration")
		input.ConsensusChanged = input.ConsensusChanged ||
			strings.Contains(filePath, "/raft") ||
			strings.HasPrefix(filePath, "pkg/cluster/") ||
			strings.HasPrefix(filePath, "internal/infra/cluster/")
		input.SecurityChanged = input.SecurityChanged ||
			strings.Contains(filePath, "auth") ||
			strings.Contains(filePath, "crypto") ||
			strings.Contains(filePath, "tls")
		input.DependencyAdded = input.DependencyAdded ||
			filePath == "go.mod" || filePath == "go.sum"
		input.ConfigDefaultChanged = input.ConfigDefaultChanged ||
			strings.HasPrefix(filePath, "internal/config/") ||
			filePath == "wukongim.toml" ||
			filePath == "wukongim.toml.example"
	}
	slices.Sort(input.Paths)
	return input
}

func allRiskClassesAuthorized(
	actual []string,
	diagnosed []string,
	authorizationEvent string,
) bool {
	for _, class := range actual {
		if !slices.Contains(diagnosed, class) {
			return false
		}
	}
	return len(actual) == 0 || authorizationEvent != ""
}

func publishDraftPR(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishDraftPRPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("Draft-PR Issue is unavailable or unauthorized")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.State != issueagentcontract.StateReproduced ||
		previous.Checkpoint.NextAction != issueagentcontract.ActionOpenDraftPR ||
		previous.Checkpoint.Work == nil ||
		previous.Checkpoint.Work.PRNumber != 0 ||
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("Draft-PR checkpoint or frozen Issue body is stale")
	}
	ref, err := client.Ref(ctx, previous.Checkpoint.Work.Branch)
	if err != nil || ref.SHA != previous.Checkpoint.Work.HeadSHA {
		return nil, errors.New("Draft-PR branch head does not match signed work")
	}
	title := "[Agent] Issue #" + strconv.FormatInt(payload.IssueNumber, 10) + ": " +
		strings.Join(strings.Fields(issue.Title), " ")
	if len(title) > 256 {
		title = title[:256]
	}
	body := "## Issue Agent\n\n" +
		"This Draft PR is related to #" + strconv.FormatInt(payload.IssueNumber, 10) +
		". It initially contains only the frozen black-box E2E reproduction.\n\n" +
		"- Affected SHA: `" + previous.Checkpoint.Versions.AffectedSHA + "`\n" +
		"- Diagnosis baseline: `" + previous.Checkpoint.Versions.DiagnosisBaseSHA + "`\n" +
		"- Reproduction assertion: `" +
		previous.Checkpoint.Reproduction.AssertionSHA256 + "`\n\n" +
		"Production changes are added only after a signed diagnosis checkpoint. " +
		"Humans retain merge and Issue-close authority."
	pull, err := client.EnsureDraftPullRequest(
		ctx, issueagentgithub.DraftPullRequest{
			Title: title, Body: body, Head: previous.Checkpoint.Work.Branch,
			Base: "main",
		},
	)
	if err != nil || pull.HeadSHA != previous.Checkpoint.Work.HeadSHA ||
		pull.State != "open" || !pull.Draft {
		return nil, errors.New("Draft pull request is inconsistent")
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateDraftPROpen
	next.Work = &issueagentcontract.Work{
		Branch:  previous.Checkpoint.Work.Branch,
		HeadSHA: previous.Checkpoint.Work.HeadSHA, PRNumber: pull.Number,
	}
	next.NextAction = issueagentcontract.ActionDiagnose
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Opened or recovered the deterministic Draft PR for the frozen E2E reproduction.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishPhaseLease(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishPhaseLeasePayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("phase-lease Issue is unavailable or unauthorized")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.Work == nil ||
		previous.Checkpoint.Work.PRNumber <= 0 ||
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("phase-lease work or frozen Issue body is stale")
	}
	pull, err := client.PullRequest(ctx, previous.Checkpoint.Work.PRNumber)
	if err != nil || pull.State != "open" || !pull.Draft ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != previous.Checkpoint.Work.HeadSHA {
		return nil, errors.New("phase-lease Draft PR is inconsistent")
	}
	policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
	if err != nil {
		return nil, errors.New("phase policy is invalid")
	}
	policy, err := issueagentusecase.DecodePolicy(
		bytes.NewReader(policyBytes), int64(len(policyBytes)),
	)
	if err != nil ||
		!policyAllowsAutomatedRemediation(policy, payload.IssueNumber) {
		return nil, errors.New("phase is outside the current remediation policy")
	}
	prompt, err := decodeCanonicalBase64(payload.PromptBase64, 128<<10)
	if err != nil {
		return nil, errors.New("phase prompt is invalid")
	}
	nextSequence := previous.Checkpoint.Sequence + 1
	operationID := issueagentusecase.OperationID(
		payload.Repository, payload.IssueNumber,
		previous.Checkpoint.Generation, nextSequence, payload.Phase,
	)
	input := issueagentusecase.PhaseTaskInput{
		Repository: payload.Repository, IssueNumber: payload.IssueNumber,
		Generation: previous.Checkpoint.Generation, Sequence: nextSequence,
		OperationID: operationID, CheckpointDigest: previous.Digest,
		PolicyDigest: digestPayload(policyBytes), PromptDigest: digestPayload(prompt),
		Versions:           previous.Checkpoint.Versions,
		CandidateSHA:       previous.Checkpoint.Work.HeadSHA,
		FrozenIssue:        issue.Body,
		AcceptedCommentIDs: previous.Checkpoint.FrozenInput.AcceptedCommentIDs,
		InstructionDigests: payload.InstructionDigests,
		Provider:           payload.Provider, Model: payload.Model,
	}
	var task issueagentcontract.TaskEnvelope
	var nextState issueagentcontract.State
	var nextAction issueagentcontract.Action
	var leaseDuration time.Duration
	var heavy bool
	switch payload.Phase {
	case issueagentcontract.PhaseDiagnose:
		if previous.Checkpoint.State != issueagentcontract.StateDraftPROpen ||
			previous.Checkpoint.NextAction != issueagentcontract.ActionDiagnose {
			return nil, errors.New("diagnosis lease is outside Draft-PR state")
		}
		task, err = issueagentusecase.BuildDiagnosisTask(input, payload.AllowedCommands)
		nextState = issueagentcontract.StateDiagnosing
		nextAction = issueagentcontract.ActionDiagnose
		leaseDuration = 65 * time.Minute
	case issueagentcontract.PhaseFix:
		if previous.Checkpoint.State != issueagentcontract.StateDiagnosed ||
			previous.Checkpoint.NextAction != issueagentcontract.ActionImplementFix ||
			previous.Checkpoint.Diagnosis == nil ||
			previous.Checkpoint.Reproduction == nil {
			return nil, errors.New("fix lease lacks a signed diagnosis")
		}
		task, err = issueagentusecase.BuildFixTask(
			input, *previous.Checkpoint.Diagnosis,
			*previous.Checkpoint.Reproduction, payload.AllowedCommands,
		)
		nextState = issueagentcontract.StateFixing
		nextAction = issueagentcontract.ActionImplementFix
		leaseDuration = 95 * time.Minute
		heavy = true
	default:
		return nil, errors.New("phase lease selects an unsupported phase")
	}
	if err != nil {
		return nil, err
	}
	attempts := uint32(0)
	maxAttempts := 0
	if payload.Phase == issueagentcontract.PhaseFix {
		attempts = previous.Checkpoint.Budget.RemediationAttempts
		maxAttempts = policy.IssueBudget.MaxRemediationAttempts
	}
	if err := ensureIssueWorkerBudget(
		previous.Checkpoint, policy, leaseDuration, attempts, maxAttempts,
	); err != nil {
		return publishIssueWorkerBudgetStop(
			ctx, config, payload.BaseURL, payload.Repository,
			payload.AppLogin, payload.IssueNumber, payload.KeySet,
			issue.Labels, comments, previous,
		)
	}
	if err := ensureRepositoryWorkerCapacity(
		ctx, client, store, now, leaseDuration, heavy,
	); err != nil {
		return nil, err
	}
	taskDigest, err := issueagentcontract.TaskDigest(task)
	if err != nil {
		return nil, err
	}
	next := previous.Checkpoint
	next.Sequence = nextSequence
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = nextState
	next.Lease = &issueagentcontract.Lease{
		OperationID: operationID, Workflow: "issue-agent-run.yml",
		DispatchRequestID: operationID, Phase: payload.Phase,
		IssuedAt: now, ExpiresAt: now.Add(leaseDuration),
		TaskSHA256: taskDigest, Task: task,
		ReservedSeconds: uint64(leaseDuration.Seconds()), Heavy: heavy,
	}
	if payload.Phase == issueagentcontract.PhaseFix {
		next.Budget.RemediationAttempts++
	}
	next.NextAction = nextAction
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Leased one bounded, credential-free " + string(payload.Phase) + " Worker.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	projection, err := appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
	if err != nil {
		return nil, err
	}
	return struct {
		Projection any                             `json:"projection"`
		Task       issueagentcontract.TaskEnvelope `json:"task"`
		TaskSHA256 string                          `json:"task_sha256"`
	}{
		Projection: projection, Task: task, TaskSHA256: taskDigest,
	}, nil
}

func ensureRepositoryWorkerCapacity(
	ctx context.Context,
	client *issueagentgithub.Client,
	store *issueagentgithub.CheckpointStore,
	now time.Time,
	reserved time.Duration,
	heavy bool,
) error {
	if reserved <= 0 || reserved > 2*time.Hour {
		return errors.New("Worker reservation is outside repository policy")
	}
	issues, err := client.ListOpenIssueNumbersByLabel(ctx, "ready-for-agent")
	if err != nil {
		return err
	}
	active := 0
	activeHeavy := 0
	rolling := time.Duration(0)
	windowStart := now.Add(-24 * time.Hour)
	for _, issueNumber := range issues {
		comments, err := client.ListIssueComments(ctx, issueNumber)
		if err != nil {
			return err
		}
		history, err := store.VerifyHistory(comments, issueNumber, now)
		if errors.Is(err, issueagentgithub.ErrNoCheckpoint) {
			continue
		}
		if err != nil {
			return err
		}
		latest := history[len(history)-1].Checkpoint
		if latest.Lease != nil && latest.Lease.ExpiresAt.After(now) {
			active++
			if latest.Lease.Heavy {
				activeHeavy++
			}
		}
		for _, entry := range history {
			lease := entry.Checkpoint.Lease
			if lease == nil || lease.IssuedAt.Before(windowStart) {
				continue
			}
			rolling += time.Duration(lease.ReservedSeconds) * time.Second
			if rolling > 24*time.Hour {
				return errors.New("repository Worker-time budget is exhausted")
			}
		}
	}
	if active >= 3 {
		return errors.New("repository active-Worker capacity is exhausted")
	}
	if heavy && activeHeavy >= 1 {
		return errors.New("repository heavy-Worker capacity is exhausted")
	}
	if rolling+reserved > 24*time.Hour {
		return errors.New("repository rolling Worker-time budget is exhausted")
	}
	return nil
}

func publishRiskAuthorization(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishRiskAuthorizationPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("risk-authorization Issue is unavailable")
	}
	comment, err := client.IssueComment(
		ctx, payload.CommentID, payload.IssueNumber,
	)
	now := config.Now().UTC()
	if err != nil || comment.AuthorType != "User" ||
		!comment.CreatedAt.Equal(comment.UpdatedAt) ||
		comment.CreatedAt.After(now) || now.Sub(comment.CreatedAt) > 5*time.Minute {
		return nil, errors.New("risk-authorization comment is stale or edited")
	}
	permission, err := client.ActorPermission(ctx, comment.Author)
	if err != nil {
		return nil, err
	}
	intent, err := issueagentusecase.ParseCommand(
		comment.Body,
		issueagentusecase.CommandActor{
			Login: comment.Author, Type: comment.AuthorType,
			Permission: issueagentusecase.Permission(permission),
		},
		issueagentusecase.CommandPolicy{},
	)
	if err != nil || intent.Kind != issueagentusecase.CommandApproveRisk {
		return nil, errors.New("comment is not an authorized risk approval")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	eventID := "issue-comment:" + strconv.FormatInt(comment.ID, 10)
	if _, err := issueagentusecase.PlanCommand(
		previous.Checkpoint, intent,
		issueagentusecase.CommandFacts{CommandEventID: eventID},
	); err != nil {
		return nil, err
	}
	if previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) ||
		previous.Checkpoint.Diagnosis == nil {
		return nil, errors.New("risk-authorization frozen Issue is stale")
	}
	next := previous.Checkpoint
	next.Generation++
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	diagnosis := *previous.Checkpoint.Diagnosis
	diagnosis.AuthorizationEvent = eventID
	next.Diagnosis = &diagnosis
	next.Lease = nil
	next.Model = nil
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Recorded a fresh maintainer authorization for the exact high-risk diagnosis scope.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishValidationRequest(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishValidationRequestPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("validation-request Issue is unavailable")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	previous, err := store.VerifyChain(
		comments, payload.IssueNumber, config.Now().UTC(),
	)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.State != issueagentcontract.StateValidating ||
		previous.Checkpoint.NextAction != issueagentcontract.ActionValidate ||
		previous.Checkpoint.Work == nil ||
		previous.Checkpoint.Diagnosis == nil {
		return nil, errors.New("validation-request checkpoint is not ready")
	}
	pull, err := client.PullRequest(ctx, previous.Checkpoint.Work.PRNumber)
	if err != nil || pull.State != "open" || !pull.Draft ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != previous.Checkpoint.Work.HeadSHA {
		return nil, errors.New("validation-request Draft PR is stale")
	}
	request, err := issueagentusecase.BuildValidationRequest(
		pull.HeadSHA, previous.Checkpoint.Diagnosis.RiskClasses,
	)
	if err != nil {
		return nil, err
	}
	prComments, err := client.ListIssueComments(ctx, pull.Number)
	if err != nil {
		return nil, err
	}
	found := false
	for _, comment := range prComments {
		if comment.Author == payload.AppLogin && comment.AuthorType == "Bot" &&
			comment.Body == request.Body &&
			comment.CreatedAt.Equal(comment.UpdatedAt) {
			found = true
			break
		}
	}
	if !found {
		if _, err := client.CreateIssueComment(ctx, pull.Number, request.Body); err != nil {
			return nil, err
		}
	}
	currentLabels, err := client.IssueLabels(ctx, pull.Number)
	if err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(currentLabels)+len(request.Labels))
	for _, label := range currentLabels {
		if !strings.HasPrefix(label, "agent-ci/") {
			labels = append(labels, label)
		}
	}
	labels = append(labels, request.Labels...)
	slices.Sort(labels)
	labels = slices.Compact(labels)
	if err := client.SetIssueLabels(ctx, pull.Number, labels); err != nil {
		return nil, err
	}
	return request, nil
}

func publishValidationResult(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishValidationResultPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(
		config, payload.BaseURL, payload.Repository,
	)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("validation-result Issue is unavailable")
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	if err != nil {
		return nil, err
	}
	if previous.Checkpoint.State != issueagentcontract.StateValidating ||
		previous.Checkpoint.NextAction != issueagentcontract.ActionValidate ||
		previous.Checkpoint.Work == nil ||
		previous.Checkpoint.Diagnosis == nil ||
		previous.Checkpoint.FrozenInput.IssueBodySHA256 != digestIssueBody(issue.Body) {
		return nil, errors.New("validation-result checkpoint is stale")
	}
	run, err := client.WorkflowRun(ctx, payload.WorkflowRunID)
	if err != nil || run.Name != "Agent Tool - Validate PR" ||
		run.Path != ".github/workflows/agent-pr-validation.yml" ||
		run.Event != "repository_dispatch" || run.Status != "completed" ||
		(run.Conclusion != "success" && run.Conclusion != "failure") ||
		run.RunAttempt > 2 {
		return nil, errors.New("validation Worker run is not a bounded completion")
	}
	matches := validationRunTitlePattern.FindStringSubmatch(run.DisplayTitle)
	if matches == nil {
		return nil, errors.New("validation Worker title is not identity-bound")
	}
	prNumber, parseErr := strconv.ParseInt(matches[1], 10, 64)
	gateRunID, gateErr := strconv.ParseInt(matches[4], 10, 64)
	requestRunID, requestErr := strconv.ParseInt(matches[5], 10, 64)
	if parseErr != nil || gateErr != nil || requestErr != nil ||
		prNumber != previous.Checkpoint.Work.PRNumber ||
		matches[2] != previous.Checkpoint.Work.HeadSHA {
		return nil, errors.New("validation Worker identity does not match signed work")
	}
	pull, err := client.PullRequest(ctx, prNumber)
	if err != nil || pull.State != "open" ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != matches[2] || pull.MergeCommit != matches[3] {
		return nil, errors.New("validated pull request moved")
	}
	gate, err := client.WorkflowRun(ctx, gateRunID)
	expectedGateTitle := regexp.MustCompile(
		`^Agent PR #` + strconv.FormatInt(prNumber, 10) +
			` merge gate (edited|opened|reopened|synchronize) head ` +
			regexp.QuoteMeta(matches[2]) + ` merge ` +
			regexp.QuoteMeta(matches[3]) + `$`,
	)
	if err != nil || gate.Path != ".github/workflows/agent-pr-merge-gate.yml" ||
		gate.Event != "pull_request" || gate.Status != "completed" ||
		gate.Conclusion != "success" || gate.HeadSHA != matches[2] ||
		!expectedGateTitle.MatchString(gate.DisplayTitle) {
		return nil, errors.New("Agent Validation Gate did not pass the exact generation")
	}
	requestRun, err := client.WorkflowRun(ctx, requestRunID)
	expectedRequestTitle := "Agent PR #" + strconv.FormatInt(prNumber, 10) +
		" validation labeled head " + matches[2] + " merge " + matches[3]
	if err != nil ||
		requestRun.Path != ".github/workflows/agent-pr-validation-control.yml" ||
		requestRun.Event != "pull_request_target" ||
		requestRun.Status != "completed" || requestRun.Conclusion != "success" ||
		requestRun.DisplayTitle != expectedRequestTitle {
		return nil, errors.New("validation request run is not exact")
	}
	validationRequest, err := issueagentusecase.BuildValidationRequest(
		matches[2], previous.Checkpoint.Diagnosis.RiskClasses,
	)
	if err != nil {
		return nil, err
	}
	if run.Conclusion == "failure" {
		return publishValidationFailure(
			ctx, config, payload, issue, comments, store, previous, client, now,
			validationRequest, matches[2], matches[3], gateRunID, requestRunID,
		)
	}
	ready, err := client.EnsurePullRequestReady(
		ctx, prNumber, previous.Checkpoint.Work.HeadSHA,
	)
	if err != nil || ready.Draft {
		return nil, errors.New("validated pull request did not become Ready")
	}
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateReadyForReview
	next.Validation = &issueagentcontract.Validation{
		HeadSHA: matches[2], TestMergeSHA: matches[3],
		GateGeneration: uint64(gateRunID), RequestRunID: requestRunID,
		EvidenceRunID:  payload.WorkflowRunID,
		RequiredSuites: validationRequest.Suites,
		LocalPasses:    3, Conclusion: "success",
	}
	next.NextAction = issueagentcontract.ActionRequestReview
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Verified the exact Validation Gate generation and converted the Draft PR to Ready for human review.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func publishValidationFailure(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishValidationResultPayload,
	issue issueagentgithub.IssueFacts,
	comments []issueagentgithub.IssueComment,
	store *issueagentgithub.CheckpointStore,
	previous issueagentgithub.VerifiedCheckpoint,
	client *issueagentgithub.Client,
	now time.Time,
	validationRequest issueagentusecase.ValidationRequest,
	headSHA string,
	testMergeSHA string,
	gateRunID int64,
	requestRunID int64,
) (any, error) {
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.Lease = nil
	next.Validation = &issueagentcontract.Validation{
		HeadSHA: headSHA, TestMergeSHA: testMergeSHA,
		GateGeneration: uint64(gateRunID), RequestRunID: requestRunID,
		EvidenceRunID:  payload.WorkflowRunID,
		RequiredSuites: validationRequest.Suites,
		LocalPasses:    0, Conclusion: "failure",
	}
	summary := "Recorded an exact failed validation generation and returned it to bounded remediation."
	var task issueagentcontract.TaskEnvelope
	policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
	if err != nil {
		return nil, errors.New("CI-repair policy is invalid")
	}
	policy, err := issueagentusecase.DecodePolicy(
		bytes.NewReader(policyBytes), int64(len(policyBytes)),
	)
	if err != nil {
		return nil, errors.New("CI-repair policy is invalid")
	}
	repairAllowed := policyAllowsAutomatedRemediation(
		policy, payload.IssueNumber,
	)
	repairBudgetExhausted := int(next.Budget.CIRepairAttempts) >=
		policy.IssueBudget.MaxCIRepairAttempts
	issueBudgetErr := ensureIssueWorkerBudget(
		previous.Checkpoint, policy, 95*time.Minute,
		previous.Checkpoint.Budget.RemediationAttempts,
		policy.IssueBudget.MaxRemediationAttempts,
	)
	if repairBudgetExhausted || issueBudgetErr != nil || !repairAllowed {
		next.State = issueagentcontract.StateReadyForHuman
		next.NextAction = issueagentcontract.ActionWaitForHuman
		if repairBudgetExhausted {
			summary = "Validation failed after the configured bounded CI repair attempts; human review is required."
		} else if issueBudgetErr != nil {
			summary = "Validation failed after the per-Issue remediation budget was exhausted; human review is required."
		} else {
			summary = "Validation failed while automated remediation was outside the current rollout policy; human review is required."
		}
	} else {
		if previous.Checkpoint.Reproduction == nil ||
			previous.Checkpoint.Diagnosis == nil ||
			previous.Checkpoint.Work == nil {
			return nil, errors.New("CI repair lacks frozen remediation evidence")
		}
		prompt, err := decodeCanonicalBase64(payload.PromptBase64, 128<<10)
		if err != nil {
			return nil, errors.New("CI-repair prompt is invalid")
		}
		operationID := issueagentusecase.OperationID(
			payload.Repository, payload.IssueNumber,
			previous.Checkpoint.Generation, next.Sequence,
			issueagentcontract.PhaseFix,
		)
		task, err = issueagentusecase.BuildFixTask(
			issueagentusecase.PhaseTaskInput{
				Repository:  payload.Repository,
				IssueNumber: payload.IssueNumber,
				Generation:  previous.Checkpoint.Generation,
				Sequence:    next.Sequence, OperationID: operationID,
				CheckpointDigest:   previous.Digest,
				PolicyDigest:       digestPayload(policyBytes),
				PromptDigest:       digestPayload(prompt),
				Versions:           previous.Checkpoint.Versions,
				CandidateSHA:       previous.Checkpoint.Work.HeadSHA,
				FrozenIssue:        issue.Body,
				AcceptedCommentIDs: previous.Checkpoint.FrozenInput.AcceptedCommentIDs,
				InstructionDigests: payload.InstructionDigests,
				Provider:           payload.Provider, Model: payload.Model,
			},
			*previous.Checkpoint.Diagnosis,
			*previous.Checkpoint.Reproduction,
			payload.AllowedCommands,
		)
		if err != nil {
			return nil, err
		}
		if err := ensureRepositoryWorkerCapacity(
			ctx, client, store, now, 95*time.Minute, true,
		); err != nil {
			return nil, err
		}
		taskDigest, err := issueagentcontract.TaskDigest(task)
		if err != nil {
			return nil, err
		}
		next.State = issueagentcontract.StateFixing
		next.Lease = &issueagentcontract.Lease{
			OperationID: operationID, Workflow: "issue-agent-run.yml",
			DispatchRequestID: operationID, Phase: issueagentcontract.PhaseFix,
			IssuedAt: now, ExpiresAt: now.Add(95 * time.Minute),
			TaskSHA256: taskDigest, Task: task,
			ReservedSeconds: uint64((95 * time.Minute).Seconds()), Heavy: true,
		}
		next.Budget.CIRepairAttempts++
		next.Budget.RemediationAttempts++
		next.NextAction = issueagentcontract.ActionImplementFix
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: next, Summary: summary, Labels: slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	projection, err := appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
	if err != nil {
		return nil, err
	}
	return struct {
		Projection any                             `json:"projection"`
		Task       issueagentcontract.TaskEnvelope `json:"task,omitempty"`
	}{
		Projection: projection, Task: task,
	}, nil
}

func policyAllowsAutomatedRemediation(
	policy issueagentusecase.Policy,
	issueNumber int64,
) bool {
	return policy.Enabled &&
		(policy.RolloutMode == issueagentusecase.RolloutGeneral ||
			policy.RolloutMode == issueagentusecase.RolloutRemediation &&
				slices.Contains(policy.RemediationIssueAllowlist, issueNumber))
}

func policyAllowsReproduction(policy issueagentusecase.Policy) bool {
	if !policy.Enabled {
		return false
	}
	switch policy.RolloutMode {
	case issueagentusecase.RolloutReproduction,
		issueagentusecase.RolloutRemediation,
		issueagentusecase.RolloutGeneral:
		return true
	default:
		return false
	}
}

func ensureIssueWorkerBudget(
	checkpoint issueagentcontract.Checkpoint,
	policy issueagentusecase.Policy,
	reserved time.Duration,
	attempts uint32,
	maxAttempts int,
) error {
	if reserved <= 0 || maxAttempts < 0 {
		return errors.New("per-Issue Worker reservation is invalid")
	}
	if maxAttempts > 0 && int(attempts) >= maxAttempts {
		return errors.New("per-Issue Worker attempt budget is exhausted")
	}
	maxSeconds := uint64(policy.IssueBudget.MaxWorkerTime / time.Second)
	reservedSeconds := uint64(reserved / time.Second)
	if checkpoint.Budget.WorkerSeconds > maxSeconds ||
		reservedSeconds > maxSeconds-checkpoint.Budget.WorkerSeconds {
		return errors.New("per-Issue Worker-time budget is exhausted")
	}
	return nil
}

func publishIssueWorkerBudgetStop(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	baseURL string,
	repository string,
	appLogin string,
	issueNumber int64,
	keySet issueagentgithub.KeySet,
	labels []string,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
) (any, error) {
	next := previous.Checkpoint
	next.Sequence++
	next.ExpectedPreviousCheckpointID = &previous.CommentID
	next.PreviousCheckpointSHA256 = &previous.Digest
	next.State = issueagentcontract.StateReadyForHuman
	next.Lease = nil
	next.NextAction = issueagentcontract.ActionWaitForHuman
	labels = append([]string(nil), labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: baseURL, Repository: repository,
		AppLogin: appLogin, IssueNumber: issueNumber,
		Now: config.Now().UTC(), KeySet: keySet, Comments: comments,
		Checkpoint: next,
		Summary:    "Stopped automatic work at the configured per-Issue Worker budget.",
		Labels:     slices.Compact(labels),
	}
	preparedClient, preparedStore, preparedPrevious, err :=
		prepareCheckpointPublication(config, publication)
	if err != nil {
		return nil, err
	}
	return appendCheckpointProjection(
		ctx, preparedClient, preparedStore, preparedPrevious, publication,
	)
}

func reproductionObservations(
	artifact issueagentworker.Artifact,
) ([]issueagentusecase.RunObservation, []issueagentusecase.RunObservation, error) {
	if artifact.Result.Reproduction == nil || len(artifact.Task.AllowedCommands) != 2 {
		return nil, nil, errors.New("reproduction Artifact lacks an assertion or commands")
	}
	groups := [2][]issueagentusecase.RunObservation{}
	for _, evidence := range artifact.Tools {
		if evidence.Tool != "command_run" {
			continue
		}
		matched := -1
		for index, rule := range artifact.Task.AllowedCommands {
			if evidence.Executable == rule.Executable &&
				slices.Equal(evidence.Arguments, rule.ArgvPrefix) {
				matched = index
				break
			}
		}
		if matched < 0 || evidence.ID == 0 ||
			evidence.ID > uint64(^uint64(0)>>1) {
			return nil, nil, errors.New("reproduction command evidence is unclassified")
		}
		outcome := issueagentusecase.RunPassed
		if evidence.ExitCode != 0 && evidence.AssertionSHA256 != "" {
			outcome = issueagentusecase.RunAssertionFailed
		} else if evidence.ExitCode != 0 {
			outcome = issueagentusecase.RunSetupFailed
		}
		sourceSHA := artifact.Task.AffectedSHA
		binarySHA := artifact.Binaries.AffectedSHA256
		if matched == 1 {
			sourceSHA = artifact.Task.DiagnosisBaseSHA
			binarySHA = artifact.Binaries.DiagnosisBaseSHA256
		}
		groups[matched] = append(groups[matched], issueagentusecase.RunObservation{
			RunID: int64(evidence.ID), SourceSHA: sourceSHA,
			BinarySHA256:    binarySHA,
			CommandSHA256:   commandEvidenceDigest(evidence),
			Assertion:       artifact.Result.Reproduction.Assertion,
			AssertionSHA256: artifact.Result.Reproduction.AssertionSHA256,
			Topology:        artifact.Result.Reproduction.Topology, Outcome: outcome,
		})
	}
	return groups[0], groups[1], nil
}

func reproductionTestFiles(
	changeSet issueagentcontract.ChangeSet,
) ([]issueagentcontract.TestFile, error) {
	files := make([]issueagentcontract.TestFile, 0, len(changeSet.Files))
	for _, change := range changeSet.Files {
		if change.Operation != issueagentcontract.FileOperationUpsert {
			return nil, errors.New("reproduction ChangeSet contains a deletion")
		}
		content, err := issueagentcontract.DecodeFileContent(change)
		if err != nil {
			return nil, err
		}
		files = append(files, issueagentcontract.TestFile{
			Path: change.Path, BlobSHA: gitBlobSHA(content),
		})
	}
	return files, nil
}

func gitBlobSHA(content []byte) string {
	hasher := sha1.New() // #nosec G401 -- Git object identity is SHA-1 by protocol.
	_, _ = hasher.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}

func commandEvidenceDigest(evidence issueagentworker.ToolEvidence) string {
	hasher := sha256.New()
	for _, part := range append([]string{evidence.Executable}, evidence.Arguments...) {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func workerElapsedMilliseconds(evidence []issueagentworker.ToolEvidence) uint64 {
	var elapsed uint64
	for _, item := range evidence {
		if item.DurationMS <= 0 {
			continue
		}
		value := uint64(item.DurationMS)
		if ^uint64(0)-elapsed < value {
			return ^uint64(0)
		}
		elapsed += value
	}
	if elapsed == 0 {
		return 1
	}
	return elapsed
}

func publishOrReuseAgentCommit(
	ctx context.Context,
	client *issueagentgithub.Client,
	branch string,
	baseTreeSHA string,
	parentSHA string,
	message string,
	changeSet issueagentcontract.ChangeSet,
	existingPaths map[string]bool,
	allowExistingParent bool,
) (issueagentgithub.PublishedCommit, error) {
	ref, exists, err := client.RefIfExists(ctx, branch)
	if err != nil {
		return issueagentgithub.PublishedCommit{}, err
	}
	if !exists {
		return client.PublishCommit(ctx, issueagentgithub.CommitPlan{
			Branch: branch, ExpectedParentSHA: parentSHA, BaseTreeSHA: baseTreeSHA,
			Message: message, ExistingBranch: false, ChangeSet: changeSet,
		})
	}
	if ref.SHA == parentSHA {
		if !allowExistingParent {
			return issueagentgithub.PublishedCommit{},
				errors.New("Agent branch unexpectedly existed before first publication")
		}
		return client.PublishCommit(ctx, issueagentgithub.CommitPlan{
			Branch: branch, ExpectedParentSHA: parentSHA, BaseTreeSHA: baseTreeSHA,
			Message: message, ExistingBranch: true, ChangeSet: changeSet,
		})
	}
	commit, err := client.Commit(ctx, ref.SHA)
	if err != nil || len(commit.Parents) != 1 || commit.Parents[0] != parentSHA ||
		!commit.Verified || commit.VerificationReason != "valid" {
		return issueagentgithub.PublishedCommit{},
			errors.New("existing Agent branch is not a reusable published commit")
	}
	files, err := client.CompareOneCommit(ctx, parentSHA, ref.SHA)
	if err != nil || len(files) != len(changeSet.Files) {
		return issueagentgithub.PublishedCommit{},
			errors.New("existing Agent branch ChangeSet is inconsistent")
	}
	for index, change := range changeSet.Files {
		if files[index].Path != change.Path {
			return issueagentgithub.PublishedCommit{},
				errors.New("existing Agent branch paths are inconsistent")
		}
		if change.Operation == issueagentcontract.FileOperationDelete {
			if files[index].Status != "removed" {
				return issueagentgithub.PublishedCommit{},
					errors.New("existing Agent branch deletion is inconsistent")
			}
			continue
		}
		content, decodeErr := issueagentcontract.DecodeFileContent(change)
		expectedStatus := "added"
		if existingPaths[change.Path] {
			expectedStatus = "modified"
		}
		if decodeErr != nil || files[index].Status != expectedStatus ||
			files[index].SHA != gitBlobSHA(content) {
			return issueagentgithub.PublishedCommit{},
				errors.New("existing Agent branch content is inconsistent")
		}
	}
	return issueagentgithub.PublishedCommit{
		CommitSHA: commit.SHA, TreeSHA: commit.TreeSHA,
	}, nil
}

func decodeCanonicalBase64(encoded string, maxBytes int) ([]byte, error) {
	if encoded == "" || maxBytes <= 0 {
		return nil, errors.New("base64 input is empty")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > maxBytes ||
		base64.StdEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.New("base64 input is not canonical or is oversized")
	}
	return decoded, nil
}

func digestPayload(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func publishIntake(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishIntakePayload,
) (any, error) {
	client, err := issueAgentGitHubClient(config, payload.BaseURL, payload.Repository)
	if err != nil {
		return nil, err
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" {
		return nil, errors.New("intake Issue is unavailable")
	}
	plan, err := issueagentusecase.PlanIntake(issue.Body, payload.PossibleDuplicates)
	if err != nil {
		return nil, err
	}
	labels := reconcileIntakeLabels(issue.Labels, plan.Labels[0])
	if plan.Message != "" {
		comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
		if err != nil {
			return nil, err
		}
		found := false
		for _, comment := range comments {
			if comment.Author == payload.AppLogin && comment.AuthorType == "Bot" &&
				comment.Body == plan.Message &&
				comment.CreatedAt.Equal(comment.UpdatedAt) {
				found = true
				break
			}
		}
		if !found {
			if _, err := client.CreateIssueComment(
				ctx, payload.IssueNumber, plan.Message,
			); err != nil {
				return nil, err
			}
		}
	}
	if err := client.SetIssueLabels(ctx, payload.IssueNumber, labels); err != nil {
		return nil, err
	}
	return plan, nil
}

func publishAuthorization(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishAuthorizationPayload,
) (any, error) {
	client, err := issueAgentGitHubClient(config, payload.BaseURL, payload.Repository)
	if err != nil {
		return nil, err
	}
	repository, err := client.Repository(ctx)
	if err != nil || repository.DefaultBranch != "main" {
		return nil, errors.New("authorization repository is invalid")
	}
	issue, err := client.Issue(ctx, payload.IssueNumber)
	if err != nil || issue.State != "open" {
		return nil, errors.New("authorization Issue is unavailable")
	}
	intake, err := issueagentusecase.PlanIntake(issue.Body, nil)
	if err != nil || !intake.Complete {
		return nil, errors.New("authorization requires a complete Bug form")
	}
	permission, err := client.ActorPermission(ctx, payload.Actor)
	if err != nil {
		return nil, err
	}
	main, err := client.DefaultBranchHead(ctx, "main")
	if err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	checkpoint, err := issueagentusecase.Authorize(
		issueagentusecase.AuthorizationFacts{
			Repository: payload.Repository, IssueNumber: payload.IssueNumber,
			EventID: payload.EventID, EventAction: payload.EventAction,
			Label: payload.Label, BeforeLabels: payload.BeforeLabels,
			AfterLabels: issue.Labels, Actor: payload.Actor,
			ActorType:  payload.ActorType,
			Permission: issueagentusecase.Permission(permission),
			EventAt:    payload.EventAt, PermissionCheckedAt: now,
			IssueBodySHA256:    digestIssueBody(issue.Body),
			AffectedVersion:    intake.Form.AffectedVersion,
			AcceptedCommentIDs: []int64{},
			DiagnosisBaseSHA:   main.SHA,
		},
		now,
		time.Minute,
	)
	if err != nil {
		return nil, err
	}
	comments, err := client.ListIssueComments(ctx, payload.IssueNumber)
	if err != nil {
		return nil, err
	}
	store, err := checkpointStoreForPublisher(
		config, payload.Repository, payload.AppLogin, payload.KeySet,
	)
	if err != nil {
		return nil, err
	}
	previous, err := store.VerifyChain(comments, payload.IssueNumber, now)
	switch {
	case errors.Is(err, issueagentgithub.ErrNoCheckpoint):
	case err != nil:
		return nil, err
	default:
		if previous.Checkpoint.FrozenInput.IssueBodySHA256 !=
			checkpoint.FrozenInput.IssueBodySHA256 {
			return nil, errors.New("edited Issue text is supplemental until /agent revise")
		}
		return nil, errors.New("Issue generation is already authorized")
	}
	body, digest, err := store.SignComment(checkpoint, "Maintainer authorization accepted.")
	if err != nil {
		return nil, err
	}
	comment, err := client.CreateIssueComment(ctx, payload.IssueNumber, body)
	if err != nil {
		return nil, err
	}
	labels := reconcileIntakeLabels(issue.Labels, "ready-for-agent")
	if err := client.SetIssueLabels(ctx, payload.IssueNumber, labels); err != nil {
		return nil, err
	}
	return struct {
		CommentID int64  `json:"comment_id"`
		Digest    string `json:"digest"`
	}{
		CommentID: comment.ID, Digest: digest,
	}, nil
}

func issueAgentGitHubClient(
	config IssueAgentGitHubConfig,
	baseURL string,
	repository string,
) (*issueagentgithub.Client, error) {
	return issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: baseURL, Repository: repository, Token: config.GitHubToken,
		MaxPages: 20, MaxBodyBytes: 16 << 20,
	}, config.HTTPClient)
}

func checkpointStoreForPublisher(
	config IssueAgentGitHubConfig,
	repository string,
	appLogin string,
	keySet issueagentgithub.KeySet,
) (*issueagentgithub.CheckpointStore, error) {
	privateKey, err := base64.StdEncoding.DecodeString(
		config.CheckpointPrivateKeyBase64,
	)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("checkpoint private key is unavailable")
	}
	return issueagentgithub.NewCheckpointStore(
		repository, appLogin, keySet,
		issueagentgithub.Signer{
			KeyID:      config.CheckpointKeyID,
			PrivateKey: ed25519.PrivateKey(privateKey),
		},
	)
}

func reconcileIntakeLabels(existing []string, desired string) []string {
	labels := make([]string, 0, len(existing)+1)
	for _, label := range existing {
		if label != "needs-triage" && label != "needs-info" &&
			(label != "ready-for-agent" || desired != "ready-for-agent") &&
			label != desired {
			labels = append(labels, label)
		}
	}
	labels = append(labels, desired)
	slices.Sort(labels)
	return slices.Compact(labels)
}

func reproductionPaths(files []issueagentcontract.TestFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func digestIssueBody(body string) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(body, "\r\n", "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
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
				ReadOnlyFiles: reproductionBinaryMounts(
					payload.Task, payload.AffectedBinary, payload.DiagnosisBinary,
				),
				ModuleCache: payload.ModuleCache,
			},
		)
		if err != nil {
			return nil, err
		}
		defer sandbox.Close()
		binaries, err := reproductionBinaryEvidence(
			payload.Task, payload.AffectedBinary, payload.DiagnosisBinary,
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
			Binaries: binaries,
		})
		if err != nil {
			return nil, err
		}
		return worker.Run(ctx)
	}
}

func reproductionBinaryEvidence(
	task issueagentcontract.TaskEnvelope,
	affected string,
	diagnosis string,
) (issueagentworker.BinaryEvidence, error) {
	if task.Phase != issueagentcontract.PhaseReproduce {
		return issueagentworker.BinaryEvidence{}, nil
	}
	affectedDigest, err := digestFile(affected)
	if err != nil {
		return issueagentworker.BinaryEvidence{}, err
	}
	diagnosisDigest, err := digestFile(diagnosis)
	if err != nil {
		return issueagentworker.BinaryEvidence{}, err
	}
	return issueagentworker.BinaryEvidence{
		AffectedSHA256: affectedDigest, DiagnosisBaseSHA256: diagnosisDigest,
	}, nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open Worker binary evidence")
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, 1<<30)); err != nil {
		return "", errors.New("hash Worker binary evidence")
	}
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > 1<<30 {
		return "", errors.New("Worker binary evidence size is invalid")
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func reproductionBinaryMounts(
	task issueagentcontract.TaskEnvelope,
	affected string,
	diagnosis string,
) []issueagentworker.ReadOnlyFileMount {
	if task.Phase != issueagentcontract.PhaseReproduce {
		return nil
	}
	return []issueagentworker.ReadOnlyFileMount{
		{HostPath: affected, ContainerPath: "/issue-agent/bin/affected"},
		{HostPath: diagnosis, ContainerPath: "/issue-agent/bin/diagnosis-base"},
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
	if err := issueagentcontract.ValidateCheckpointSuccessor(
		previous.Checkpoint, payload.Checkpoint,
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
	if dependencies.PublishDraft == nil {
		dependencies.PublishDraft = unavailable
	}
	if dependencies.PublishIntake == nil {
		dependencies.PublishIntake = unavailable
	}
	if dependencies.PublishAuthorization == nil {
		dependencies.PublishAuthorization = unavailable
	}
	if dependencies.PublishVersionPin == nil {
		dependencies.PublishVersionPin = unavailable
	}
	if dependencies.PublishReproductionLease == nil {
		dependencies.PublishReproductionLease = unavailable
	}
	if dependencies.PublishWorkerArtifact == nil {
		dependencies.PublishWorkerArtifact = unavailable
	}
	if dependencies.PublishDraftPR == nil {
		dependencies.PublishDraftPR = unavailable
	}
	if dependencies.PublishPhaseLease == nil {
		dependencies.PublishPhaseLease = unavailable
	}
	if dependencies.PublishRiskAuthorization == nil {
		dependencies.PublishRiskAuthorization = unavailable
	}
	if dependencies.PublishValidationRequest == nil {
		dependencies.PublishValidationRequest = unavailable
	}
	if dependencies.PublishValidationResult == nil {
		dependencies.PublishValidationResult = unavailable
	}
	if dependencies.PublishExpiredLease == nil {
		dependencies.PublishExpiredLease = unavailable
	}
	if dependencies.ReadCurrentCheckpoint == nil {
		dependencies.ReadCurrentCheckpoint = unavailable
	}
	if dependencies.ReadCurrentTask == nil {
		dependencies.ReadCurrentTask = unavailable
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
		PublishLease:             dependencies.PublishLease,
		PublishResult:            dependencies.PublishResult,
		PublishDraft:             dependencies.PublishDraft,
		PublishIntake:            dependencies.PublishIntake,
		PublishAuthorization:     dependencies.PublishAuthorization,
		PublishVersionPin:        dependencies.PublishVersionPin,
		PublishReproductionLease: dependencies.PublishReproductionLease,
		PublishWorkerArtifact:    dependencies.PublishWorkerArtifact,
		PublishDraftPR:           dependencies.PublishDraftPR,
		PublishPhaseLease:        dependencies.PublishPhaseLease,
		PublishRiskAuthorization: dependencies.PublishRiskAuthorization,
		PublishValidationRequest: dependencies.PublishValidationRequest,
		PublishValidationResult:  dependencies.PublishValidationResult,
		PublishExpiredLease:      dependencies.PublishExpiredLease,
		PublishCommand:           dependencies.PublishCommand,
		PublishMerge:             dependencies.PublishMerge,
		ReadCurrentCheckpoint:    dependencies.ReadCurrentCheckpoint,
		ReadCurrentTask:          dependencies.ReadCurrentTask,
		RunWorker:                dependencies.RunWorker,
		VerifyCheckpoint:         dependencies.VerifyCheckpoint,
		MintAppToken:             dependencies.MintAppToken,
	}
}
