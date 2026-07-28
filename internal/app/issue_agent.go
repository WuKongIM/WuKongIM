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
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

// IssueAgentDependencies are write-capable operations supplied only to the
// standalone Issue Agent command. They are never attached to App.New.
type IssueAgentDependencies struct {
	PublishLease     func(context.Context, issueagentcli.DocumentRequest) (any, error)
	PublishResult    func(context.Context, issueagentcli.DocumentRequest) (any, error)
	VerifyCheckpoint func(context.Context, issueagentcli.DocumentRequest) (any, error)
	MintAppToken     func(context.Context, issueagentcli.DocumentRequest) (any, error)
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
		VerifyCheckpoint: dependencies.VerifyCheckpoint,
		MintAppToken:     dependencies.MintAppToken,
	}
}
