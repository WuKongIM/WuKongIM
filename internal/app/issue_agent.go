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
	"fmt"
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
	PublishBranchDrift       func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishWorkDrift         func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishAuditAlert        func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishProjectionRepair  func(context.Context, issueagentcli.DocumentRequest) (any, error)
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
	BaseURL                string                       `json:"base_url"`
	Repository             string                       `json:"repository"`
	AppLogin               string                       `json:"app_login"`
	IssueNumber            int64                        `json:"issue_number"`
	KeySet                 issueagentgithub.KeySet      `json:"key_set"`
	MechanicalMainSHA      string                       `json:"mechanical_main_sha"`
	MechanicalMergeTreeSHA string                       `json:"mechanical_merge_tree_sha"`
	MechanicalChangeSet    issueagentcontract.ChangeSet `json:"mechanical_change_set"`
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
	CheckpointCommentID int64                            `json:"checkpoint_comment_id"`
	CheckpointDigest    string                           `json:"checkpoint_digest"`
	Checkpoint          issueagentcontract.Checkpoint    `json:"checkpoint"`
	CurrentWorkHeadSHA  string                           `json:"current_work_head_sha,omitempty"`
	CurrentWork         *issueagentusecase.WorkHeadFacts `json:"current_work,omitempty"`
	ChainInvalid        bool                             `json:"chain_invalid,omitempty"`
	IssueBodyChanged    bool                             `json:"issue_body_changed"`
	Plan                *issueagentusecase.Plan          `json:"plan,omitempty"`
}

type readCurrentTaskPayload struct {
	readCurrentCheckpointPayload
	OperationID string `json:"operation_id"`
}

var validationRunTitlePattern = regexp.MustCompile(
	`^Agent PR #([1-9][0-9]*) validation head ([0-9a-f]{40}) merge ` +
		`([0-9a-f]{40}) gate ([1-9][0-9]*) request ([1-9][0-9]*)$`,
)

var movingMainStatusDescriptionPattern = regexp.MustCompile(
	`^main=([0-9a-f]{40});binary=([0-9a-f]{64});runs=3$`,
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
		PublishBranchDrift: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishBranchDrift(ctx, config, payload)
		},
		PublishWorkDrift: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishWorkDrift(ctx, config, payload)
		},
		PublishAuditAlert: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishAuditAlert(ctx, config, payload)
		},
		PublishProjectionRepair: func(
			ctx context.Context,
			document issueagentcli.DocumentRequest,
		) (any, error) {
			var payload readCurrentCheckpointPayload
			if err := decodeIssueAgentDocument(document.Payload, &payload); err != nil {
				return nil, err
			}
			return publishProjectionRepair(ctx, config, payload)
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
	if err != nil || issue.State != "open" && issue.State != "closed" ||
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
		if errors.Is(err, issueagentgithub.ErrNoCheckpoint) ||
			payload.PolicyBase64 == "" {
			return nil, err
		}
		policyBytes, decodeErr := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
		if decodeErr != nil {
			return nil, errors.New("current-checkpoint policy is invalid")
		}
		policy, decodeErr := issueagentusecase.DecodePolicy(
			bytes.NewReader(policyBytes), int64(len(policyBytes)),
		)
		if decodeErr != nil {
			return nil, decodeErr
		}
		plan, reconcileErr := issueagentusecase.Reconcile(
			issueagentusecase.ReconcileInput{
				Now:         config.Now().UTC(),
				ChainStatus: issueagentusecase.ChainInvalid,
			},
			issueagentusecase.ReconcilePolicy{
				Enabled: policy.Enabled, RolloutMode: policy.RolloutMode,
			},
		)
		if reconcileErr != nil {
			return nil, reconcileErr
		}
		return currentCheckpointResult{
			ChainInvalid: true,
			Plan:         &plan,
		}, nil
	}
	if issue.State == "closed" &&
		verified.Checkpoint.State != issueagentcontract.StateReadyForReview &&
		!issueagentusecase.IsTerminalLifecycleState(
			verified.Checkpoint.State,
		) {
		return nil, errors.New("closed Issue is not awaiting an exact merge observation")
	}
	result := currentCheckpointResult{
		CheckpointCommentID: verified.CommentID,
		CheckpointDigest:    verified.Digest,
		Checkpoint:          verified.Checkpoint,
		IssueBodyChanged: verified.Checkpoint.FrozenInput.IssueBodySHA256 !=
			digestIssueBody(issue.Body),
	}
	workHeadFacts, mergeFacts, err := readActiveWorkFacts(
		ctx, client, verified.Checkpoint,
	)
	workObjectMissing := errors.Is(err, issueagentgithub.ErrNotFound)
	if err != nil && (!workObjectMissing || payload.PolicyBase64 == "") {
		return nil, err
	}
	if workHeadFacts != nil {
		result.CurrentWorkHeadSHA = workHeadFacts.HeadSHA
		result.CurrentWork = workHeadFacts
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
		artifacts, err := readCurrentLeaseArtifacts(
			ctx, client, verified.Checkpoint, config.Now().UTC(),
		)
		if err != nil {
			return nil, err
		}
		plan, err := issueagentusecase.Reconcile(
			issueagentusecase.ReconcileInput{
				Now:                 config.Now().UTC(),
				ChainStatus:         issueagentusecase.ChainValid,
				Checkpoint:          &verified.Checkpoint,
				CheckpointCommentID: verified.CommentID,
				CheckpointDigest:    verified.Digest,
				Lease:               leaseFacts,
				Artifacts:           artifacts,
				WorkHead:            workHeadFacts,
				WorkObjectMissing:   workObjectMissing,
				Merge:               mergeFacts,
				IssueLabels:         issue.Labels,
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

func readActiveWorkFacts(
	ctx context.Context,
	client *issueagentgithub.Client,
	checkpoint issueagentcontract.Checkpoint,
) (*issueagentusecase.WorkHeadFacts, *issueagentusecase.MergeFacts, error) {
	if !issueagentcontract.IsActiveWorkState(checkpoint.State) ||
		checkpoint.Work == nil {
		return nil, nil, nil
	}
	work := checkpoint.Work
	if work.PRNumber == 0 {
		ref, err := client.Ref(ctx, work.Branch)
		if err != nil {
			return nil, nil, fmt.Errorf("active Agent branch facts are stale: %w", err)
		}
		return &issueagentusecase.WorkHeadFacts{HeadSHA: ref.SHA}, nil, nil
	}
	pull, err := client.PullRequest(ctx, work.PRNumber)
	if err != nil {
		return nil, nil,
			fmt.Errorf("active Agent pull request facts are stale: %w", err)
	}
	head := &issueagentusecase.WorkHeadFacts{
		PRNumber: pull.Number, HeadSHA: pull.HeadSHA,
		PRState: pull.State, Draft: pull.Draft, BaseRef: pull.BaseRef,
		HeadRef: pull.HeadRef,
	}
	if checkpoint.State != issueagentcontract.StateReadyForReview {
		return head, nil, nil
	}
	return head, &issueagentusecase.MergeFacts{
		PRNumber: pull.Number, HeadSHA: pull.HeadSHA,
		Merged: pull.State == "closed" && pull.Merged,
	}, nil
}

func readCurrentLeaseArtifacts(
	ctx context.Context,
	client *issueagentgithub.Client,
	checkpoint issueagentcontract.Checkpoint,
	now time.Time,
) ([]issueagentusecase.WorkerArtifact, error) {
	lease := checkpoint.Lease
	if lease == nil || !lease.ExpiresAt.After(now) {
		return nil, nil
	}
	runs, err := client.CompletedWorkflowRunsSince(
		ctx, lease.Workflow, lease.IssuedAt,
	)
	if err != nil {
		return nil, err
	}
	title := "Issue Agent worker Issue " +
		strconv.FormatInt(checkpoint.IssueNumber, 10) +
		" operation " + lease.OperationID
	artifactName := "issue-agent-result-" + lease.OperationID[7:23]
	result := make([]issueagentusecase.WorkerArtifact, 0, 1)
	for _, run := range runs {
		workflowPath := ".github/workflows/" + lease.Workflow
		if run.Name != "Agent Tool - Issue Worker" ||
			run.HeadBranch != "main" ||
			run.Path != workflowPath &&
				run.Path != workflowPath+"@main" &&
				run.Path != workflowPath+"@refs/heads/main" {
			return nil, errors.New("Worker run has an unexpected workflow identity")
		}
		if run.DisplayTitle != title {
			continue
		}
		artifacts, err := client.RunArtifacts(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		matches := 0
		for _, artifact := range artifacts {
			if artifact.Name == artifactName && !artifact.Expired {
				matches++
			}
		}
		if matches > 1 {
			return nil, errors.New("Worker run contains duplicate lease-bound Artifacts")
		}
		if matches == 1 {
			result = append(result, issueagentusecase.WorkerArtifact{
				RunID: run.ID, OperationID: lease.OperationID,
				TaskDigest: lease.TaskSHA256,
				Generation: checkpoint.Generation,
			})
		}
	}
	return result, nil
}

const auditFailureMarker = "<!-- wukongim-issue-agent:audit-failure:v1 -->"

func publishAuditAlert(
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
	if err != nil || issue.State != "open" && issue.State != "closed" ||
		!slices.Contains(issue.Labels, "ready-for-agent") {
		return nil, errors.New("audit-alert Issue is unavailable")
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
	if _, verifyErr := store.VerifyChain(
		comments, payload.IssueNumber, config.Now().UTC(),
	); verifyErr == nil || errors.Is(verifyErr, issueagentgithub.ErrNoCheckpoint) {
		return nil, errors.New("audit alert requires an invalid signed checkpoint chain")
	}
	body := auditFailureMarker + "\n\n" +
		"## Issue Agent audit failure\n\n" +
		"The signed checkpoint chain is invalid. Automatic lifecycle writes are fenced, " +
		"and this Issue requires human attention (`ready_for_human`).\n\n" +
		"An administrator must inspect the checkpoint history and use the exact " +
		"`/agent recover-chain <comment-id> <checkpoint-sha256> <quarantine-sha256>` " +
		"command before automation can continue."
	created := false
	for _, comment := range comments {
		if comment.Author == payload.AppLogin &&
			comment.AuthorType == "Bot" &&
			comment.Body == body &&
			comment.CreatedAt.Equal(comment.UpdatedAt) {
			goto labels
		}
	}
	if _, err := client.CreateIssueComment(
		ctx, payload.IssueNumber, body,
	); err != nil {
		return nil, err
	}
	created = true

labels:
	labels := append([]string(nil), issue.Labels...)
	labels = append(labels, "ready-for-human")
	slices.Sort(labels)
	labels = slices.Compact(labels)
	if !slices.Equal(labels, issue.Labels) {
		if err := client.SetIssueLabels(ctx, payload.IssueNumber, labels); err != nil {
			return nil, err
		}
	}
	return struct {
		Complete bool `json:"complete"`
		Created  bool `json:"created"`
	}{
		Complete: true,
		Created:  created,
	}, nil
}

func publishProjectionRepair(
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
		return nil, errors.New("projection-repair Issue is unavailable")
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
	checkpoint := verified.Checkpoint
	if issueagentcontract.IsActiveWorkState(checkpoint.State) &&
		checkpoint.Work != nil && checkpoint.Work.PRNumber > 0 {
		work := checkpoint.Work
		pull, readErr := client.PullRequest(ctx, work.PRNumber)
		mergedReview := checkpoint.State ==
			issueagentcontract.StateReadyForReview &&
			pull.State == "closed" && pull.Merged
		if readErr != nil ||
			pull.State != "open" && !mergedReview ||
			pull.BaseRef != "main" || pull.HeadRef != work.Branch ||
			pull.HeadSHA != work.HeadSHA {
			return nil, errors.New("pull request projection cannot be safely repaired")
		}
		expectedDraft := checkpoint.State !=
			issueagentcontract.StateReadyForReview
		if pull.State == "open" && pull.Draft != expectedDraft {
			if expectedDraft {
				pull, readErr = client.EnsurePullRequestDraft(
					ctx, work.PRNumber, work.HeadSHA,
				)
			} else {
				pull, readErr = client.EnsurePullRequestReady(
					ctx, work.PRNumber, work.HeadSHA,
				)
			}
			if readErr != nil || pull.Draft != expectedDraft {
				return nil, errors.New("pull request projection repair failed")
			}
		}
	}
	labels := issueagentusecase.ProjectLifecycleLabels(
		checkpoint.State, issue.Labels,
	)
	if !slices.Equal(labels, issue.Labels) {
		if err := client.SetIssueLabels(
			ctx, payload.IssueNumber, labels,
		); err != nil {
			return nil, err
		}
	}
	return struct {
		State  issueagentcontract.State `json:"state"`
		Labels []string                 `json:"labels"`
	}{
		State: checkpoint.State, Labels: labels,
	}, nil
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
	if work := verified.Checkpoint.Work; work != nil {
		if verified.CurrentWork == nil ||
			verified.CurrentWork.HeadSHA != work.HeadSHA {
			return nil, errors.New("current Agent branch head differs from its signed task")
		}
		if work.PRNumber > 0 &&
			(verified.CurrentWork.PRState != "open" ||
				!verified.CurrentWork.Draft ||
				verified.CurrentWork.BaseRef != "main" ||
				verified.CurrentWork.HeadRef != work.Branch) {
			return nil, errors.New("current Agent pull request differs from its signed task")
		}
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
	transition, err := issueagentusecase.PlanExpiredLeaseTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		policy.IssueBudget.MaxInfrastructureRetries,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
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
		pull, err := client.PullRequest(
			ctx, previous.Checkpoint.Work.PRNumber,
		)
		if errors.Is(err, issueagentgithub.ErrNotFound) ||
			err == nil && (pull.State != "open" || pull.BaseRef != "main" ||
				pull.HeadRef != previous.Checkpoint.Work.Branch) {
			return publishWorkDrift(
				ctx, config, readCurrentCheckpointPayload{
					BaseURL: payload.BaseURL, Repository: payload.Repository,
					AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
					KeySet: payload.KeySet,
				},
			)
		}
		if err != nil {
			return nil, err
		}
		if pull.HeadSHA != previous.Checkpoint.Work.HeadSHA {
			return publishBranchDrift(
				ctx, config, readCurrentCheckpointPayload{
					BaseURL: payload.BaseURL, Repository: payload.Repository,
					AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
					KeySet: payload.KeySet,
				},
			)
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
		if previous.Checkpoint.Work.PRNumber > 0 {
			pull, err := client.PullRequest(
				ctx, previous.Checkpoint.Work.PRNumber,
			)
			if errors.Is(err, issueagentgithub.ErrNotFound) ||
				err == nil && (pull.State != "open" ||
					pull.BaseRef != "main" ||
					pull.HeadRef != previous.Checkpoint.Work.Branch) {
				return publishWorkDrift(
					ctx, config, readCurrentCheckpointPayload{
						BaseURL: payload.BaseURL, Repository: payload.Repository,
						AppLogin:    payload.AppLogin,
						IssueNumber: payload.IssueNumber, KeySet: payload.KeySet,
					},
				)
			}
			if err != nil {
				return nil, err
			}
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
	var task issueagentcontract.TaskEnvelope
	var taskInput *issueagentcontract.TaskEnvelope
	var childIssueNumber int64
	switch intent.Kind {
	case issueagentusecase.CommandRevise, issueagentusecase.CommandCancel:
	case issueagentusecase.CommandAdoptHead:
		if previous.Checkpoint.Work == nil {
			return nil, errors.New("adopt-head work disappeared")
		}
		if previous.Checkpoint.Work.PRNumber > 0 {
			pull, err := client.PullRequest(
				ctx, previous.Checkpoint.Work.PRNumber,
			)
			if err != nil || pull.State != "open" ||
				pull.BaseRef != "main" ||
				pull.HeadRef != previous.Checkpoint.Work.Branch ||
				pull.HeadSHA != plan.AdoptedHeadSHA {
				return nil, errors.New("adopt-head Draft PR is not exact")
			}
			pull, err = client.EnsurePullRequestDraft(
				ctx, pull.Number, plan.AdoptedHeadSHA,
			)
			if err != nil || pull.State != "open" || !pull.Draft ||
				pull.BaseRef != "main" ||
				pull.HeadRef != previous.Checkpoint.Work.Branch ||
				pull.HeadSHA != plan.AdoptedHeadSHA {
				return nil, errors.New("adopt-head PR could not return to Draft")
			}
		}
	case issueagentusecase.CommandAddressReview:
		if !issueagentusecase.AllowsAutomatedRemediation(
			policy, payload.IssueNumber,
		) ||
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
		if errors.Is(err, issueagentgithub.ErrNotFound) ||
			err == nil && (pull.State != "open" || pull.BaseRef != "main" ||
				pull.HeadRef != previous.Checkpoint.Work.Branch) {
			return publishWorkDrift(
				ctx, config, readCurrentCheckpointPayload{
					BaseURL: payload.BaseURL, Repository: payload.Repository,
					AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
					KeySet: payload.KeySet,
				},
			)
		}
		if err != nil || !pull.Draft ||
			pull.HeadSHA != previous.Checkpoint.Work.HeadSHA {
			return nil, errors.New("address-review PR could not return to exact Draft")
		}
		operationID := issueagentusecase.OperationID(
			payload.Repository, payload.IssueNumber, plan.NewGeneration,
			previous.Checkpoint.Sequence+1, issueagentcontract.PhaseAddressReview,
		)
		task, err = issueagentusecase.BuildAddressReviewTask(
			issueagentusecase.PhaseTaskInput{
				Repository: payload.Repository, IssueNumber: payload.IssueNumber,
				Generation:  plan.NewGeneration,
				Sequence:    previous.Checkpoint.Sequence + 1,
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
		if err := issueagentusecase.CheckIssueWorkerBudget(
			previous.Checkpoint, policy, issueagentcontract.PhaseAddressReview,
		); err != nil {
			return nil, err
		}
		reservation, err := issueagentusecase.WorkerReservationForPhase(
			issueagentcontract.PhaseAddressReview,
		)
		if err != nil {
			return nil, err
		}
		if err := ensureRepositoryWorkerCapacity(
			ctx, client, store, now, reservation.Duration, reservation.Heavy,
		); err != nil {
			return nil, err
		}
		taskInput = &task
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
		childIssueNumber = child
	}
	transition, err := issueagentusecase.FinalizeCommandTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		intent, plan, eventID, comment.Author, comment.ID,
		taskInput, childIssueNumber, now,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
		Labels: slices.Compact(labels),
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
	transition, err := issueagentusecase.PlanChainRecoveryTransition(
		anchor.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: anchor.CommentID, Digest: anchor.Digest,
		},
		issueagentcontract.ControlAudit{
			Kind:    string(issueagentusecase.CommandRecoverChain),
			EventID: eventID, Actor: command.Author, CommentID: command.ID,
			RecoveryAnchorCommentID: plan.Recovery.AnchorCommentID,
			RecoveryAnchorDigest:    plan.Recovery.AnchorDigest,
			QuarantinedCommentIDs: append(
				[]int64(nil), plan.Recovery.QuarantinedCommentIDs...,
			),
			QuarantineDigest: plan.Recovery.QuarantineDigest,
		},
	)
	if err != nil {
		return nil, err
	}
	body, digest, err := store.SignComment(
		transition.Checkpoint, transition.Summary,
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
	if err == nil && pull.BaseRef != "main" {
		return publishWorkDrift(ctx, config, payload)
	}
	if err != nil || pull.State != "closed" || !pull.Merged ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != previous.Checkpoint.Work.HeadSHA ||
		pull.MergeCommit == "" {
		return nil, errors.New("GitHub does not report the exact Agent PR as merged")
	}
	transition, err := issueagentusecase.PlanMergeObservedTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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

func publishBranchDrift(
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
		return nil, errors.New("branch-drift Issue is unavailable")
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
	if !issueagentcontract.IsActiveWorkState(previous.Checkpoint.State) ||
		previous.Checkpoint.Work == nil ||
		previous.Checkpoint.Work.ExternalHeadSHA != nil {
		return nil, errors.New("branch drift requires exact active Agent work")
	}
	work := previous.Checkpoint.Work
	var currentHeadSHA string
	if work.PRNumber == 0 {
		ref, readErr := client.Ref(ctx, work.Branch)
		if readErr != nil {
			return nil, errors.New("GitHub Agent branch is unavailable")
		}
		currentHeadSHA = ref.SHA
	} else {
		pull, readErr := client.PullRequest(ctx, work.PRNumber)
		if readErr != nil || pull.HeadRef != work.Branch {
			return nil, errors.New("GitHub Agent pull request is unavailable")
		}
		currentHeadSHA = pull.HeadSHA
	}
	if currentHeadSHA == work.HeadSHA {
		return nil, errors.New("GitHub does not report an external Agent branch update")
	}
	transition, err := issueagentusecase.PlanExternalBranchUpdateTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		currentHeadSHA,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
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

func publishWorkDrift(
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
		return nil, errors.New("work-drift Issue is unavailable")
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
	checkpoint := previous.Checkpoint
	if !issueagentcontract.IsActiveWorkState(checkpoint.State) ||
		checkpoint.Work == nil {
		return nil, errors.New("work drift requires exact active Agent work")
	}
	work := checkpoint.Work
	mismatch := false
	if work.PRNumber == 0 {
		ref, readErr := client.Ref(ctx, work.Branch)
		if errors.Is(readErr, issueagentgithub.ErrNotFound) {
			mismatch = true
		} else if readErr != nil {
			return nil, readErr
		} else if ref.SHA != work.HeadSHA {
			return nil, errors.New("work drift is an external branch-head update")
		}
	} else {
		pull, readErr := client.PullRequest(ctx, work.PRNumber)
		if errors.Is(readErr, issueagentgithub.ErrNotFound) {
			mismatch = true
		} else if readErr != nil {
			return nil, readErr
		} else {
			if pull.HeadRef != work.Branch || pull.BaseRef != "main" ||
				pull.State != "open" &&
					!(checkpoint.State == issueagentcontract.StateReadyForReview &&
						pull.State == "closed" && pull.Merged) {
				mismatch = true
			} else if pull.HeadSHA != work.HeadSHA {
				return nil, errors.New("work drift is an external branch-head update")
			}
		}
	}
	if !mismatch {
		return nil, errors.New("GitHub does not report a missing or changed work object")
	}
	transition, err := issueagentusecase.PlanWorkObjectDriftTransition(
		checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
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
	transition, err := issueagentusecase.PlanVersionPinnedTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		versions,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	if err != nil || !issueagentusecase.AllowsReproduction(policy) {
		return nil, errors.New("reproduction is outside the current rollout policy")
	}
	if err := issueagentusecase.CheckIssueWorkerBudget(
		previous.Checkpoint, policy, issueagentcontract.PhaseReproduce,
	); err != nil {
		return publishIssueWorkerBudgetStop(
			ctx, config, payload.BaseURL, payload.Repository,
			payload.AppLogin, payload.IssueNumber, payload.KeySet,
			issue.Labels, comments, previous,
		)
	}
	reservation, err := issueagentusecase.WorkerReservationForPhase(
		issueagentcontract.PhaseReproduce,
	)
	if err != nil {
		return nil, err
	}
	if err := ensureRepositoryWorkerCapacity(
		ctx, client, store, now, reservation.Duration, reservation.Heavy,
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
	transition, err := issueagentusecase.PlanWorkerLeaseTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		task, now,
	)
	if err != nil {
		return nil, err
	}
	taskDigest := transition.Checkpoint.Lease.TaskSHA256
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	if work := previous.Checkpoint.Work; work != nil {
		head, _, readErr := readActiveWorkFacts(
			ctx, client, previous.Checkpoint,
		)
		readPayload := readCurrentCheckpointPayload{
			BaseURL: payload.BaseURL, Repository: payload.Repository,
			AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
			KeySet: payload.KeySet,
		}
		if errors.Is(readErr, issueagentgithub.ErrNotFound) {
			return publishWorkDrift(ctx, config, readPayload)
		}
		if readErr != nil {
			return nil, readErr
		}
		if head == nil {
			return nil, errors.New("active Worker work head is unavailable")
		}
		pendingCommitPhase := payload.Artifact.Result.Status ==
			issueagentcontract.ResultStatusSuccess &&
			len(payload.Artifact.Result.ChangeSet.Files) > 0 &&
			(payload.Artifact.Task.Phase == issueagentcontract.PhaseFix ||
				payload.Artifact.Task.Phase ==
					issueagentcontract.PhaseAddressReview)
		boundary, err := issueagentusecase.PlanArtifactWorkBoundary(
			*work, *head, pendingCommitPhase,
		)
		if err != nil {
			return nil, err
		}
		switch boundary {
		case issueagentusecase.ArtifactWorkContinue,
			issueagentusecase.ArtifactWorkVerifyPendingEffect:
		case issueagentusecase.ArtifactWorkRecordObjectDrift:
			return publishWorkDrift(ctx, config, readPayload)
		case issueagentusecase.ArtifactWorkRecordBranchDrift:
			return publishBranchDrift(ctx, config, readPayload)
		case issueagentusecase.ArtifactWorkRepairProjection:
			return publishProjectionRepair(ctx, config, readPayload)
		default:
			return nil, errors.New("Artifact work boundary plan is invalid")
		}
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

	var work *issueagentcontract.Work
	if evaluation.Decision == issueagentusecase.ReproductionConfirmed {
		template, err := decodeCanonicalBase64(
			payload.ScenarioInstructionTemplateBase64, 64<<10,
		)
		if err != nil {
			return nil, errors.New("scenario instruction template is invalid")
		}
		publishedChangeSet, err := issueagentgithub.InjectScenarioInstructions(
			payload.Artifact.Result.ChangeSet, payload.IssueNumber, template,
		)
		if err != nil {
			return nil, err
		}
		parent, err := client.Commit(ctx, previous.Checkpoint.Versions.DiagnosisBaseSHA)
		if err != nil {
			return nil, err
		}
		existingPaths := make(map[string]bool, len(publishedChangeSet.Files))
		for _, file := range publishedChangeSet.Files {
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
			ChangeSet:         publishedChangeSet,
			Limits: issueagentcontract.ChangeSetLimits{
				MaxFiles:      payload.Artifact.Task.Limits.MaxFiles + 1,
				MaxFileBytes:  int(payload.Artifact.Task.Limits.MaxFileBytes),
				MaxTotalBytes: int(payload.Artifact.Task.Limits.MaxTotalBytes) + len(template),
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
			payload.AppLogin, publishedChangeSet, existingPaths, false,
		)
		if err != nil {
			if errors.Is(err, errExternalAgentHead) {
				return publishWorkerPublicationCollision(
					ctx, config, payload, issue.Labels, comments,
					previous, now,
				)
			}
			return nil, err
		}
		work = &issueagentcontract.Work{
			Branch: branch, HeadSHA: published.CommitSHA,
		}
	}
	transition, err := issueagentusecase.PlanReproductionResultTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		evaluation, work,
		workerAttemptFacts(payload.Artifact, string(evaluation.Decision)),
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	transition, err := issueagentusecase.PlanWorkerFailureTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		workerAttemptFacts(payload.Artifact, string(result.Failure.Class)),
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issueLabels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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

func publishWorkerPublicationCollision(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishWorkerArtifactPayload,
	issueLabels []string,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
	now time.Time,
) (any, error) {
	transition, err :=
		issueagentusecase.PlanWorkerPublicationCollisionTransition(
			previous.Checkpoint,
			issueagentusecase.TransitionAnchor{
				CommentID: previous.CommentID, Digest: previous.Digest,
			},
			workerAttemptFacts(payload.Artifact, "publication_collision"),
		)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issueLabels...)
	labels = append(labels, "ready-for-human")
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
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
	transition, err := issueagentusecase.PlanDiagnosisResultTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		diagnosis, workerAttemptFacts(payload.Artifact, "diagnosed"),
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issueLabels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	risk, err := issueagentusecase.ClassifyRisk(
		issueagentusecase.RiskInputFromChangeSet(result.ChangeSet),
	)
	if err != nil {
		return nil, err
	}
	if risk.HumanOnly ||
		!issueagentusecase.RiskClassesAuthorized(
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
		payload.AppLogin, result.ChangeSet, existingPaths, true,
	)
	if err != nil {
		if errors.Is(err, errExternalAgentHead) {
			return publishBranchDrift(
				ctx, config,
				readCurrentCheckpointPayload{
					BaseURL: payload.BaseURL, Repository: payload.Repository,
					AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
					KeySet: payload.KeySet,
				},
			)
		}
		return nil, err
	}
	pull, err := client.PullRequest(ctx, previous.Checkpoint.Work.PRNumber)
	if err == nil && pull.State == "open" && !pull.Draft &&
		pull.BaseRef == "main" &&
		pull.HeadRef == previous.Checkpoint.Work.Branch &&
		pull.HeadSHA == published.CommitSHA {
		pull, err = client.EnsurePullRequestDraft(
			ctx, pull.Number, published.CommitSHA,
		)
	}
	if err != nil || pull.State != "open" || !pull.Draft ||
		pull.BaseRef != "main" ||
		pull.HeadRef != previous.Checkpoint.Work.Branch ||
		pull.HeadSHA != published.CommitSHA {
		return nil, errors.New("Draft PR did not advance to the exact fix candidate")
	}
	transition, err := issueagentusecase.PlanFixResultTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		published.CommitSHA, workerAttemptFacts(payload.Artifact, "fixed"),
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issueLabels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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

func workerAttemptFacts(
	artifact issueagentworker.Artifact,
	terminal string,
) issueagentusecase.WorkerAttemptFacts {
	elapsedMS := workerElapsedMilliseconds(artifact.Tools)
	return issueagentusecase.WorkerAttemptFacts{
		Provider:            artifact.Result.Usage.Provider,
		Model:               artifact.Result.Usage.Model,
		InputTokens:         artifact.Result.Usage.InputTokens,
		OutputTokens:        artifact.Result.Usage.OutputTokens,
		ElapsedMilliseconds: elapsedMS, TerminalResult: terminal,
	}
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
		"Fixes #" + strconv.FormatInt(payload.IssueNumber, 10) + "\n\n" +
		"It initially contains only the frozen black-box E2E reproduction.\n\n" +
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
	transition, err := issueagentusecase.PlanDraftPROpenTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		pull.Number,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	readPayload := readCurrentCheckpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		KeySet: payload.KeySet,
	}
	if errors.Is(err, issueagentgithub.ErrNotFound) ||
		err == nil && (pull.State != "open" || pull.BaseRef != "main" ||
			pull.HeadRef != previous.Checkpoint.Work.Branch) {
		return publishWorkDrift(ctx, config, readPayload)
	}
	if err != nil {
		return nil, err
	}
	if pull.HeadSHA != previous.Checkpoint.Work.HeadSHA {
		return publishBranchDrift(ctx, config, readPayload)
	}
	if !pull.Draft {
		return publishProjectionRepair(ctx, config, readPayload)
	}
	policyBytes, err := decodeCanonicalBase64(payload.PolicyBase64, 1<<20)
	if err != nil {
		return nil, errors.New("phase policy is invalid")
	}
	policy, err := issueagentusecase.DecodePolicy(
		bytes.NewReader(policyBytes), int64(len(policyBytes)),
	)
	if err != nil ||
		!issueagentusecase.AllowsAutomatedRemediation(
			policy, payload.IssueNumber,
		) {
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
	switch payload.Phase {
	case issueagentcontract.PhaseDiagnose:
		if previous.Checkpoint.State != issueagentcontract.StateDraftPROpen ||
			previous.Checkpoint.NextAction != issueagentcontract.ActionDiagnose {
			return nil, errors.New("diagnosis lease is outside Draft-PR state")
		}
		task, err = issueagentusecase.BuildDiagnosisTask(input, payload.AllowedCommands)
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
	default:
		return nil, errors.New("phase lease selects an unsupported phase")
	}
	if err != nil {
		return nil, err
	}
	if err := issueagentusecase.CheckIssueWorkerBudget(
		previous.Checkpoint, policy, payload.Phase,
	); err != nil {
		return publishIssueWorkerBudgetStop(
			ctx, config, payload.BaseURL, payload.Repository,
			payload.AppLogin, payload.IssueNumber, payload.KeySet,
			issue.Labels, comments, previous,
		)
	}
	reservation, err := issueagentusecase.WorkerReservationForPhase(payload.Phase)
	if err != nil {
		return nil, err
	}
	if err := ensureRepositoryWorkerCapacity(
		ctx, client, store, now, reservation.Duration, reservation.Heavy,
	); err != nil {
		return nil, err
	}
	transition, err := issueagentusecase.PlanWorkerLeaseTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		task, now,
	)
	if err != nil {
		return nil, err
	}
	taskDigest := transition.Checkpoint.Lease.TaskSHA256
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	transition, err := issueagentusecase.PlanRiskAuthorizationTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		eventID,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	if err != nil || pull.State != "open" || pull.BaseRef != "main" ||
		pull.HeadRef != previous.Checkpoint.Work.Branch {
		return nil, errors.New("validation-request Draft PR is stale")
	}
	if !pull.Draft {
		pull, err = client.EnsurePullRequestDraft(
			ctx, pull.Number, pull.HeadSHA,
		)
		if err != nil || !pull.Draft {
			return nil, errors.New("validation-request PR could not return to Draft")
		}
	}
	if pull.HeadSHA != previous.Checkpoint.Work.HeadSHA {
		commit, commitErr := client.Commit(ctx, pull.HeadSHA)
		attribution, attributionErr := client.CommitAttribution(
			ctx, pull.HeadSHA,
		)
		recovered := commitErr == nil &&
			attributionErr == nil &&
			previous.Checkpoint.Work.MechanicalRebaseAttempts == 0 &&
			payload.MechanicalMainSHA == pull.BaseSHA &&
			issueagentgithub.ExactRebasedIntegration(
				commit,
				attribution,
				payload.MechanicalMainSHA,
				payload.MechanicalMergeTreeSHA,
				issueagentusecase.MechanicalRebaseMessage(payload.IssueNumber),
				payload.AppLogin,
			)
		return publishValidationDrift(
			ctx, config, payload, issue, comments, previous, client, pull, recovered,
		)
	}
	if pull.Mergeable == nil {
		return nil, errors.New("validation-request mergeability is not resolved")
	}
	if !*pull.Mergeable {
		if payload.MechanicalMainSHA != pull.BaseSHA {
			return nil, errors.New("mechanical merge baseline is stale")
		}
		return publishValidationDrift(
			ctx, config, payload, issue, comments, previous, client, pull, false,
		)
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

func publishValidationDrift(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishValidationRequestPayload,
	issue issueagentgithub.IssueFacts,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
	client *issueagentgithub.Client,
	pull issueagentgithub.PullRequestFacts,
	recovered bool,
) (any, error) {
	facts := issueagentusecase.DriftFacts{
		ExpectedAgentHead: previous.Checkpoint.Work.HeadSHA,
		CurrentAgentHead:  pull.HeadSHA,
		CurrentMainSHA:    pull.BaseSHA,
		MechanicalTreeSHA: payload.MechanicalMergeTreeSHA,
		Conflict:          "semantic",
		ConflictAttempts: int(
			previous.Checkpoint.Work.MechanicalRebaseAttempts,
		),
	}
	if recovered {
		facts.CurrentAgentHead = previous.Checkpoint.Work.HeadSHA
	}
	if payload.MechanicalMergeTreeSHA != "" {
		facts.Conflict = "mechanical"
	}
	plan, err := issueagentusecase.PlanValidationDriftTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		facts,
	)
	if err != nil {
		return nil, err
	}
	var transition issueagentusecase.PlannedTransition
	if plan.Immediate != nil {
		transition = *plan.Immediate
	} else if recovered {
		transition, err = issueagentusecase.BindMechanicalRebaseSuccess(
			previous.Checkpoint, plan, pull.HeadSHA,
		)
	} else {
		published, rebaseErr := client.PublishRebasedCommit(
			ctx, issueagentgithub.RebasePlan{
				Branch:                plan.Effect.Branch,
				ExpectedOldHeadSHA:    plan.Effect.ExpectedHead,
				CurrentMainSHA:        plan.Effect.MainSHA,
				ExpectedResultTreeSHA: plan.Effect.ExpectedTree,
				Message:               plan.Effect.Message,
				ExpectedAuthorLogin:   payload.AppLogin,
				ChangeSet:             payload.MechanicalChangeSet,
			},
		)
		if rebaseErr != nil {
			return nil, rebaseErr
		}
		transition, err = issueagentusecase.BindMechanicalRebaseSuccess(
			previous.Checkpoint, plan, published.CommitSHA,
		)
		if err != nil {
			return nil, err
		}
	}
	labels := append([]string(nil), issue.Labels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: config.Now().UTC(), KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
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
	if err != nil || pull.State != "open" && !(pull.State == "closed" && !pull.Merged) ||
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
	expectedGateConclusion := run.Conclusion
	if err != nil || gate.Path != ".github/workflows/agent-pr-merge-gate.yml" ||
		gate.Event != "pull_request" || gate.Status != "completed" ||
		gate.RunAttempt != 2 || gate.Conclusion != expectedGateConclusion ||
		gate.HeadSHA != matches[2] ||
		!expectedGateTitle.MatchString(gate.DisplayTitle) {
		return nil, errors.New("Agent Validation Gate did not confirm the exact validation conclusion")
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
	driftFacts, driftDecision, err := movingMainDecision(
		ctx, client, payload.WorkflowRunID, gateRunID, prNumber,
		previous.Checkpoint, pull,
	)
	if err != nil {
		return nil, err
	}
	if driftDecision == issueagentusecase.DriftAlreadyFixedOnMain {
		return publishAlreadyFixedOnMain(
			ctx, config, payload, issue, comments, previous, client, now,
			validationRequest, driftFacts, gateRunID, requestRunID,
		)
	}
	if pull.State != "open" {
		return nil, errors.New("closed pull request lacks moving-main evidence")
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
	validation := issueagentcontract.Validation{
		HeadSHA: matches[2], TestMergeSHA: matches[3],
		GateGeneration: uint64(gateRunID), RequestRunID: requestRunID,
		EvidenceRunID:  payload.WorkflowRunID,
		RequiredSuites: validationRequest.Suites,
		LocalPasses:    3, Conclusion: "success",
	}
	transition, err := issueagentusecase.PlanValidationSuccessTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		validation,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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

func movingMainDecision(
	ctx context.Context,
	client *issueagentgithub.Client,
	workflowRunID int64,
	gateRunID int64,
	prNumber int64,
	checkpoint issueagentcontract.Checkpoint,
	pull issueagentgithub.PullRequestFacts,
) (issueagentusecase.DriftFacts, issueagentusecase.DriftDecision, error) {
	if checkpoint.Reproduction == nil || checkpoint.Work == nil {
		return issueagentusecase.DriftFacts{}, issueagentusecase.DriftNone,
			errors.New("moving-main check lacks frozen reproduction")
	}
	statuses, err := client.ListCommitStatuses(ctx, pull.HeadSHA)
	if err != nil {
		return issueagentusecase.DriftFacts{}, issueagentusecase.DriftNone, err
	}
	contextName := "Agent Moving Main / PR #" + strconv.FormatInt(prNumber, 10) +
		" / Gate #" + strconv.FormatInt(gateRunID, 10)
	runSuffix := "/actions/runs/" + strconv.FormatInt(workflowRunID, 10)
	var selected *issueagentgithub.CommitStatusFacts
	for index := range statuses {
		status := &statuses[index]
		if status.Context != contextName ||
			!strings.HasSuffix(status.TargetURL, runSuffix) ||
			status.CreatorType != "Bot" {
			continue
		}
		if selected == nil || status.ID > selected.ID {
			selected = status
		}
	}
	if selected == nil ||
		(selected.State != "success" && selected.State != "failure") {
		return issueagentusecase.DriftFacts{}, issueagentusecase.DriftNone,
			errors.New("moving-main status is missing or incomplete")
	}
	matches := movingMainStatusDescriptionPattern.FindStringSubmatch(
		selected.Description,
	)
	if matches == nil {
		return issueagentusecase.DriftFacts{}, issueagentusecase.DriftNone,
			errors.New("moving-main status evidence is malformed")
	}
	mainRef, err := client.DefaultBranchHead(ctx, "main")
	if err != nil || mainRef.SHA != matches[1] ||
		pull.BaseRef != "main" || pull.BaseSHA != matches[1] {
		return issueagentusecase.DriftFacts{}, issueagentusecase.DriftNone,
			errors.New("moving-main evidence is stale")
	}
	facts := issueagentusecase.DriftFacts{
		ExpectedAgentHead: checkpoint.Work.HeadSHA,
		CurrentAgentHead:  pull.HeadSHA,
		CurrentMainSHA:    matches[1],
		AssertionSHA256:   checkpoint.Reproduction.AssertionSHA256,
		Topology:          checkpoint.Reproduction.Topology,
	}
	if selected.State == "success" {
		commandDigest := digestPayload([]byte(
			movingMainCommandIdentity(checkpoint.IssueNumber),
		))
		for index := 0; index < 3; index++ {
			facts.MainRuns = append(facts.MainRuns, issueagentusecase.RunObservation{
				RunID: workflowRunID, SourceSHA: matches[1],
				BinarySHA256:    "sha256:" + matches[2],
				CommandSHA256:   commandDigest,
				Assertion:       checkpoint.Reproduction.Assertion,
				AssertionSHA256: checkpoint.Reproduction.AssertionSHA256,
				Topology:        checkpoint.Reproduction.Topology,
				Outcome:         issueagentusecase.RunPassed,
			})
		}
	}
	decision, err := issueagentusecase.PlanDriftRecovery(facts)
	return facts, decision, err
}

func movingMainCommandIdentity(issueNumber int64) string {
	return "WK_E2E_BINARY=current-main timeout --signal=TERM --kill-after=30s " +
		"50m go test -tags=e2e ./test/e2e/issue_agent/issue_" +
		strconv.FormatInt(issueNumber, 10) +
		" -count=3 -timeout=45m -p=1"
}

func publishAlreadyFixedOnMain(
	ctx context.Context,
	config IssueAgentGitHubConfig,
	payload publishValidationResultPayload,
	issue issueagentgithub.IssueFacts,
	comments []issueagentgithub.IssueComment,
	previous issueagentgithub.VerifiedCheckpoint,
	client *issueagentgithub.Client,
	now time.Time,
	validationRequest issueagentusecase.ValidationRequest,
	driftFacts issueagentusecase.DriftFacts,
	gateRunID int64,
	requestRunID int64,
) (any, error) {
	projection := issueagentusecase.ProjectAlreadyFixedOnMain()
	if !projection.CloseDraftPR || projection.CloseIssue ||
		projection.State != issueagentcontract.StateAlreadyFixed {
		return nil, errors.New("moving-main projection is unsafe")
	}
	closed, err := client.EnsurePullRequestClosed(
		ctx, previous.Checkpoint.Work.PRNumber,
		previous.Checkpoint.Work.HeadSHA,
	)
	if err != nil || closed.Merged || closed.State != "closed" {
		return nil, errors.New("already-fixed Draft PR did not close exactly")
	}
	validation := issueagentcontract.Validation{
		HeadSHA:        previous.Checkpoint.Work.HeadSHA,
		TestMergeSHA:   driftFacts.CurrentMainSHA,
		GateGeneration: uint64(gateRunID),
		RequestRunID:   requestRunID,
		EvidenceRunID:  payload.WorkflowRunID,
		RequiredSuites: validationRequest.Suites,
		LocalPasses:    3, Conclusion: "success",
	}
	transition, err := issueagentusecase.PlanAlreadyFixedOnMainTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		validation,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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
	validation := issueagentcontract.Validation{
		HeadSHA: headSHA, TestMergeSHA: testMergeSHA,
		GateGeneration: uint64(gateRunID), RequestRunID: requestRunID,
		EvidenceRunID:  payload.WorkflowRunID,
		RequiredSuites: validationRequest.Suites,
		LocalPasses:    0, Conclusion: "failure",
	}
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
	disposition := issueagentusecase.PlanCIRepairDisposition(
		previous.Checkpoint, policy,
	)
	if disposition.Repair {
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
			previous.Checkpoint.Generation, previous.Checkpoint.Sequence+1,
			issueagentcontract.PhaseFix,
		)
		task, err = issueagentusecase.BuildFixTask(
			issueagentusecase.PhaseTaskInput{
				Repository:  payload.Repository,
				IssueNumber: payload.IssueNumber,
				Generation:  previous.Checkpoint.Generation,
				Sequence:    previous.Checkpoint.Sequence + 1, OperationID: operationID,
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
	}
	var taskInput *issueagentcontract.TaskEnvelope
	if disposition.Repair {
		taskInput = &task
	}
	transition, err := issueagentusecase.PlanValidationFailureTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
		validation, disposition, taskInput, now,
	)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), issue.Labels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: payload.BaseURL, Repository: payload.Repository,
		AppLogin: payload.AppLogin, IssueNumber: payload.IssueNumber,
		Now: now, KeySet: payload.KeySet, Comments: comments,
		Checkpoint: transition.Checkpoint, Summary: transition.Summary,
		Labels: slices.Compact(labels),
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
	transition, err := issueagentusecase.PlanWorkerBudgetStopTransition(
		previous.Checkpoint,
		issueagentusecase.TransitionAnchor{
			CommentID: previous.CommentID, Digest: previous.Digest,
		},
	)
	if err != nil {
		return nil, err
	}
	labels = append([]string(nil), labels...)
	if transition.RequireReadyHumanLabel {
		labels = append(labels, "ready-for-human")
	}
	slices.Sort(labels)
	publication := checkpointPayload{
		BaseURL: baseURL, Repository: repository,
		AppLogin: appLogin, IssueNumber: issueNumber,
		Now: config.Now().UTC(), KeySet: keySet, Comments: comments,
		Checkpoint: transition.Checkpoint,
		Summary:    transition.Summary,
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

var errExternalAgentHead = errors.New("external Agent branch head")

func publishOrReuseAgentCommit(
	ctx context.Context,
	client *issueagentgithub.Client,
	branch string,
	baseTreeSHA string,
	parentSHA string,
	message string,
	appLogin string,
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
				fmt.Errorf(
					"%w: Agent branch existed before first publication",
					errExternalAgentHead,
				)
		}
		return client.PublishCommit(ctx, issueagentgithub.CommitPlan{
			Branch: branch, ExpectedParentSHA: parentSHA, BaseTreeSHA: baseTreeSHA,
			Message: message, ExistingBranch: true, ChangeSet: changeSet,
		})
	}
	commit, err := client.Commit(ctx, ref.SHA)
	if err != nil {
		return issueagentgithub.PublishedCommit{}, err
	}
	attribution, err := client.CommitAttribution(ctx, ref.SHA)
	if err != nil {
		if errors.Is(err, issueagentgithub.ErrUntrustedCommit) {
			return issueagentgithub.PublishedCommit{},
				fmt.Errorf("%w: commit attribution is not reusable", errExternalAgentHead)
		}
		return issueagentgithub.PublishedCommit{}, err
	}
	if !issueagentgithub.ExactAppCommit(
		commit, attribution, parentSHA, message, appLogin,
	) {
		return issueagentgithub.PublishedCommit{},
			fmt.Errorf("%w: existing commit identity is not reusable", errExternalAgentHead)
	}
	files, err := client.CompareOneCommit(ctx, parentSHA, ref.SHA)
	if err != nil || len(files) != len(changeSet.Files) {
		if err != nil {
			return issueagentgithub.PublishedCommit{}, err
		}
		return issueagentgithub.PublishedCommit{},
			fmt.Errorf("%w: existing ChangeSet is inconsistent", errExternalAgentHead)
	}
	for index, change := range changeSet.Files {
		if files[index].Path != change.Path {
			return issueagentgithub.PublishedCommit{},
				fmt.Errorf("%w: existing paths are inconsistent", errExternalAgentHead)
		}
		if change.Operation == issueagentcontract.FileOperationDelete {
			if files[index].Status != "removed" {
				return issueagentgithub.PublishedCommit{},
					fmt.Errorf("%w: existing deletion is inconsistent", errExternalAgentHead)
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
				fmt.Errorf("%w: existing content is inconsistent", errExternalAgentHead)
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
	labels := issueagentusecase.ProjectLifecycleLabels(
		payload.Checkpoint.State, payload.Labels,
	)
	if err := client.SetIssueLabels(ctx, payload.IssueNumber, labels); err != nil {
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
	if dependencies.PublishCommand == nil {
		dependencies.PublishCommand = unavailable
	}
	if dependencies.PublishMerge == nil {
		dependencies.PublishMerge = unavailable
	}
	if dependencies.PublishBranchDrift == nil {
		dependencies.PublishBranchDrift = unavailable
	}
	if dependencies.PublishWorkDrift == nil {
		dependencies.PublishWorkDrift = unavailable
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
	if dependencies.PublishAuditAlert == nil {
		dependencies.PublishAuditAlert = unavailable
	}
	if dependencies.PublishProjectionRepair == nil {
		dependencies.PublishProjectionRepair = unavailable
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
				WorkHead:          request.WorkHead,
				WorkObjectMissing: request.WorkObjectMissing,
				Merge:             request.Merge,
				IssueLabels:       request.IssueLabels,
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
		PublishBranchDrift:       dependencies.PublishBranchDrift,
		PublishWorkDrift:         dependencies.PublishWorkDrift,
		PublishAuditAlert:        dependencies.PublishAuditAlert,
		PublishProjectionRepair:  dependencies.PublishProjectionRepair,
		ReadCurrentCheckpoint:    dependencies.ReadCurrentCheckpoint,
		ReadCurrentTask:          dependencies.ReadCurrentTask,
		RunWorker:                dependencies.RunWorker,
		VerifyCheckpoint:         dependencies.VerifyCheckpoint,
		MintAppToken:             dependencies.MintAppToken,
	}
}
