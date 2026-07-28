package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestPublisherPerformsDeterministicIntakeWithoutModelOrExecution(t *testing.T) {
	t.Parallel()

	body := renderedCompleteBugForm()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42":
			writeAppJSON(t, writer, map[string]any{
				"number": 42, "state": "open", "title": "bug", "body": body,
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels":             []map[string]any{{"name": "needs-triage"}},
			})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/labels":
			writeAppJSON(t, writer, []map[string]any{{"name": "needs-triage"}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	dependencies := NewIssueAgentGitHubDependencies(IssueAgentGitHubConfig{
		HTTPClient: server.Client(), GitHubToken: "app-token",
	})
	payload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": "WuKongIM/WuKongIM",
		"app_login": "agent[bot]", "issue_number": 42,
		"possible_duplicates": []string{},
	})
	require.NoError(t, err)
	result, err := dependencies.PublishIntake(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestPublisherCreatesFirstSignedCheckpointOnlyForFreshMaintainerLabel(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	body := renderedCompleteBugForm()
	var postedComment string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM":
			writeAppJSON(t, writer, map[string]any{
				"id": 1, "full_name": "WuKongIM/WuKongIM",
				"default_branch": "main",
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42":
			writeAppJSON(t, writer, map[string]any{
				"number": 42, "state": "open", "title": "bug", "body": body,
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels":             []map[string]any{{"name": "ready-for-agent"}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/collaborators/maintainer/permission":
			writeAppJSON(t, writer, map[string]any{
				"permission": "maintain",
				"user":       map[string]any{"login": "maintainer"},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/main":
			writeAppJSON(t, writer, map[string]any{
				"ref": "refs/heads/main",
				"object": map[string]any{
					"type": "commit",
					"sha":  "0123456789abcdef0123456789abcdef01234567",
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			writeAppJSON(t, writer, []any{})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			var requestBody struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&requestBody))
			postedComment = requestBody.Body
			writeAppJSON(t, writer, map[string]any{
				"id": 100, "body": postedComment,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/labels":
			writeAppJSON(t, writer, []map[string]any{{"name": "ready-for-agent"}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	dependencies := NewIssueAgentGitHubDependencies(IssueAgentGitHubConfig{
		HTTPClient: server.Client(), GitHubToken: "app-token",
		CheckpointKeyID:            "key-1",
		CheckpointPrivateKeyBase64: base64.StdEncoding.EncodeToString(privateKey),
		Now:                        func() time.Time { return now },
	})
	payload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": "WuKongIM/WuKongIM",
		"app_login": "agent[bot]", "issue_number": 42,
		"key_set": issueagentgithub.KeySet{
			SchemaVersion: 1,
			Keys: []issueagentgithub.PublicKey{{
				ID: "key-1", PublicKey: publicKey,
				NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			}},
		},
		"event_id": "issue-labeled-run-1", "event_action": "labeled",
		"label": "ready-for-agent", "before_labels": []string{"needs-triage"},
		"actor": "maintainer", "actor_type": "User", "event_at": now,
	})
	require.NoError(t, err)
	result, err := dependencies.PublishAuthorization(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, postedComment, "wukongim-issue-agent-checkpoint:v1")
}

func TestPublisherPinsReleaseAndBaselineFromFreshGitHubFacts(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	const (
		repository  = "WuKongIM/WuKongIM"
		affectedSHA = "89abcdef0123456789abcdef0123456789abcdef"
		baselineSHA = "0123456789abcdef0123456789abcdef01234567"
	)
	body := renderedCompleteBugForm()
	keySet := issueagentgithub.KeySet{
		SchemaVersion: 1,
		Keys: []issueagentgithub.PublicKey{{
			ID: "key-1", PublicKey: publicKey,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}},
	}
	authorized := issueagentcontract.Checkpoint{
		SchemaVersion: 1, Repository: repository, IssueNumber: 42,
		Generation: 1, Sequence: 1, State: issueagentcontract.StateAuthorized,
		FrozenInput: issueagentcontract.FrozenInput{
			IssueBodySHA256: digestIssueBody(body), AffectedVersion: "v2.1.0",
			AuthorizationEvent: "authorize-1", AuthorizedBy: "maintainer",
		},
		Versions: issueagentcontract.Versions{
			ReportedRef: "v2.1.0", DiagnosisBaseSHA: baselineSHA,
		},
		NextAction: issueagentcontract.ActionPinVersions,
	}
	store, err := issueagentgithub.NewCheckpointStore(
		repository, "agent[bot]", keySet,
		issueagentgithub.Signer{KeyID: "key-1", PrivateKey: privateKey},
	)
	require.NoError(t, err)
	authorizedBody, _, err := store.SignComment(authorized, "Authorized.")
	require.NoError(t, err)

	var pinnedBody string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/issues/42":
			writeAppJSON(t, writer, map[string]any{
				"number": 42, "state": "open", "title": "bug", "body": body,
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels":             []map[string]any{{"name": "ready-for-agent"}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/issues/42/comments":
			writeAppJSON(t, writer, []map[string]any{{
				"id": 100, "body": authorizedBody,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at": now.Add(-time.Minute).Format(time.RFC3339),
			}})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/git/commits/"+baselineSHA:
			writeAppJSON(t, writer, map[string]any{"sha": baselineSHA})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/git/ref/tags/v2.1.0":
			writeAppJSON(t, writer, map[string]any{
				"ref":    "refs/tags/v2.1.0",
				"object": map[string]any{"type": "commit", "sha": affectedSHA},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/"+repository+"/issues/42/comments":
			var requestBody struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&requestBody))
			pinnedBody = requestBody.Body
			writeAppJSON(t, writer, map[string]any{
				"id": 101, "body": pinnedBody,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/"+repository+"/issues/42/labels":
			writeAppJSON(t, writer, []map[string]any{{"name": "ready-for-agent"}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	dependencies := NewIssueAgentGitHubDependencies(IssueAgentGitHubConfig{
		HTTPClient: server.Client(), GitHubToken: "app-token",
		CheckpointKeyID:            "key-1",
		CheckpointPrivateKeyBase64: base64.StdEncoding.EncodeToString(privateKey),
		Now:                        func() time.Time { return now },
	})
	payload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": repository,
		"app_login": "agent[bot]", "issue_number": 42, "key_set": keySet,
	})
	require.NoError(t, err)
	result, err := dependencies.PublishVersionPin(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, pinnedBody, affectedSHA)
	require.Contains(t, pinnedBody, baselineSHA)
	require.Equal(t, 1, strings.Count(
		pinnedBody, "wukongim-issue-agent-checkpoint:v1",
	))
}

func TestPublisherProjectsClosedInvalidChainAsIdempotentHumanAlert(t *testing.T) {
	t.Parallel()

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	policy, err := os.ReadFile("../../.github/issue-agent/policy.json")
	require.NoError(t, err)
	policy = []byte(strings.Replace(
		string(policy), `"rollout_mode": "shadow"`, `"rollout_mode": "general"`, 1,
	))
	keySet := issueagentgithub.KeySet{
		SchemaVersion: 1,
		Keys: []issueagentgithub.PublicKey{{
			ID: "key-1", PublicKey: publicKey,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}},
	}
	invalidCheckpoint := "<!-- wukongim-issue-agent-checkpoint:v1\n" +
		"{\"malformed\":true}\n-->"
	var postedAlert string
	var requestedLabels []string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42":
			writeAppJSON(t, writer, map[string]any{
				"number": 42, "state": "closed", "title": "bug",
				"body":               renderedCompleteBugForm(),
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels":             []map[string]any{{"name": "ready-for-agent"}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			writeAppJSON(t, writer, []map[string]any{{
				"id": 100, "body": invalidCheckpoint,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at": now.Add(-time.Minute).Format(time.RFC3339),
			}})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			var input struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			postedAlert = input.Body
			writeAppJSON(t, writer, map[string]any{
				"id": 101, "body": input.Body,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/labels":
			var input struct {
				Labels []string `json:"labels"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			requestedLabels = input.Labels
			writeAppJSON(t, writer, []map[string]any{
				{"name": "ready-for-agent"}, {"name": "ready-for-human"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	dependencies := NewIssueAgentGitHubDependencies(IssueAgentGitHubConfig{
		HTTPClient: server.Client(), GitHubToken: "app-token",
		Now: func() time.Time { return now },
	})
	payload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": "WuKongIM/WuKongIM",
		"app_login": "agent[bot]", "issue_number": 42, "key_set": keySet,
		"policy_base64": base64.StdEncoding.EncodeToString(policy),
	})
	require.NoError(t, err)
	current, err := dependencies.ReadCurrentCheckpoint(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	projection, ok := current.(currentCheckpointResult)
	require.True(t, ok)
	require.True(t, projection.ChainInvalid)
	require.NotNil(t, projection.Plan)
	require.Equal(t, "alert_audit_failure", string(projection.Plan.Operation))

	alertPayload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": "WuKongIM/WuKongIM",
		"app_login": "agent[bot]", "issue_number": 42, "key_set": keySet,
	})
	require.NoError(t, err)
	result, err := dependencies.PublishAuditAlert(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: alertPayload},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, postedAlert, auditFailureMarker)
	require.Contains(t, postedAlert, "ready_for_human")
	require.Equal(t, []string{"ready-for-agent", "ready-for-human"}, requestedLabels)
}

func TestPublisherRepairsTerminalLabelsAfterCheckpointProjectionCrash(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	const repository = "WuKongIM/WuKongIM"
	keySet := issueagentgithub.KeySet{
		SchemaVersion: 1,
		Keys: []issueagentgithub.PublicKey{{
			ID: "key-1", PublicKey: publicKey,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}},
	}
	checkpoint := issueagentcontract.Checkpoint{
		SchemaVersion: 1, Repository: repository, IssueNumber: 42,
		Generation: 1, Sequence: 1, State: issueagentcontract.StateCancelled,
		FrozenInput: issueagentcontract.FrozenInput{
			IssueBodySHA256: digestIssueBody(renderedCompleteBugForm()),
			AffectedVersion: "v2.1.0", AuthorizationEvent: "authorize-1",
			AuthorizedBy: "maintainer",
		},
		Versions: issueagentcontract.Versions{
			ReportedRef:      "v2.1.0",
			DiagnosisBaseSHA: "0123456789abcdef0123456789abcdef01234567",
		},
		NextAction: issueagentcontract.ActionNone,
	}
	store, err := issueagentgithub.NewCheckpointStore(
		repository, "agent[bot]", keySet,
		issueagentgithub.Signer{KeyID: "key-1", PrivateKey: privateKey},
	)
	require.NoError(t, err)
	signed, _, err := store.SignComment(checkpoint, "Cancelled.")
	require.NoError(t, err)

	var requestedLabels []string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/issues/42":
			writeAppJSON(t, writer, map[string]any{
				"number": 42, "state": "open", "title": "bug",
				"body":               renderedCompleteBugForm(),
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels": []map[string]any{
					{"name": "bug"}, {"name": "ready-for-agent"},
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/issues/42/comments":
			writeAppJSON(t, writer, []map[string]any{{
				"id": 100, "body": signed,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at": now.Add(-time.Minute).Format(time.RFC3339),
			}})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/"+repository+"/issues/42/labels":
			var input struct {
				Labels []string `json:"labels"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			requestedLabels = input.Labels
			writeAppJSON(t, writer, []map[string]any{{"name": "bug"}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	dependencies := NewIssueAgentGitHubDependencies(IssueAgentGitHubConfig{
		HTTPClient: server.Client(), GitHubToken: "app-token",
		Now: func() time.Time { return now },
	})
	payload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": repository,
		"app_login": "agent[bot]", "issue_number": 42, "key_set": keySet,
	})
	require.NoError(t, err)
	result, err := dependencies.PublishProjectionRepair(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, []string{"bug"}, requestedLabels)
}

func TestActiveDraftPRExternalHeadDoesNotBlockControlReadAndIsRecorded(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 17, 0, 0, 0, time.UTC)
	const (
		repository   = "WuKongIM/WuKongIM"
		expectedHead = "0123456789abcdef0123456789abcdef01234567"
		externalHead = "89abcdef0123456789abcdef0123456789abcdef"
		baseSHA      = "76543210fedcba9876543210fedcba9876543210"
	)
	keySet := issueagentgithub.KeySet{
		SchemaVersion: 1,
		Keys: []issueagentgithub.PublicKey{{
			ID: "key-1", PublicKey: publicKey,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}},
	}
	checkpoint := readyReviewCheckpointForApp(expectedHead, baseSHA)
	checkpoint.State = issueagentcontract.StateDraftPROpen
	checkpoint.Diagnosis = nil
	checkpoint.Validation = nil
	checkpoint.NextAction = issueagentcontract.ActionDiagnose
	store, err := issueagentgithub.NewCheckpointStore(
		repository, "agent[bot]", keySet,
		issueagentgithub.Signer{KeyID: "key-1", PrivateKey: privateKey},
	)
	require.NoError(t, err)
	signed, _, err := store.SignComment(checkpoint, "Ready.")
	require.NoError(t, err)
	policy, err := os.ReadFile("../../.github/issue-agent/policy.json")
	require.NoError(t, err)
	policy = []byte(strings.Replace(
		string(policy), `"rollout_mode": "shadow"`, `"rollout_mode": "general"`, 1,
	))
	var driftComment string
	var requestedLabels []string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/issues/42":
			writeAppJSON(t, writer, map[string]any{
				"number": 42, "state": "open", "title": "bug",
				"body":               renderedCompleteBugForm(),
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels": []map[string]any{
					{"name": "ready-for-agent"},
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/issues/42/comments":
			writeAppJSON(t, writer, []map[string]any{{
				"id": 100, "body": signed,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at": now.Add(-time.Minute).Format(time.RFC3339),
			}})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/"+repository+"/pulls/9":
			writeAppJSON(t, writer, map[string]any{
				"number": 9, "state": "open", "draft": false,
				"mergeable": true, "merged": false, "merge_commit_sha": "",
				"base": map[string]any{"ref": "main", "sha": baseSHA},
				"head": map[string]any{
					"ref": "agent/issue-42", "sha": externalHead,
				},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/"+repository+"/issues/42/comments":
			var input struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			driftComment = input.Body
			writeAppJSON(t, writer, map[string]any{
				"id": 101, "body": input.Body,
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/"+repository+"/issues/42/labels":
			var input struct {
				Labels []string `json:"labels"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			requestedLabels = input.Labels
			writeAppJSON(t, writer, []map[string]any{
				{"name": "ready-for-agent"}, {"name": "ready-for-human"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	dependencies := NewIssueAgentGitHubDependencies(IssueAgentGitHubConfig{
		HTTPClient: server.Client(), GitHubToken: "app-token",
		CheckpointKeyID:            "key-1",
		CheckpointPrivateKeyBase64: base64.StdEncoding.EncodeToString(privateKey),
		Now:                        func() time.Time { return now },
	})
	payload, err := json.Marshal(map[string]any{
		"base_url": server.URL, "repository": repository,
		"app_login": "agent[bot]", "issue_number": 42, "key_set": keySet,
		"policy_base64": base64.StdEncoding.EncodeToString(policy),
	})
	require.NoError(t, err)
	current, err := dependencies.ReadCurrentCheckpoint(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	result := current.(currentCheckpointResult)
	require.NotNil(t, result.Plan)
	require.Equal(t, "record_branch_drift", string(result.Plan.Operation))
	require.Equal(t, externalHead, result.Plan.ExternalHeadSHA)

	published, err := dependencies.PublishBranchDrift(
		context.Background(),
		issueagentcli.DocumentRequest{SchemaVersion: 1, Payload: payload},
	)
	require.NoError(t, err)
	require.NotNil(t, published)
	require.Contains(t, driftComment, externalHead)
	require.Equal(t,
		[]string{"ready-for-agent", "ready-for-human"},
		requestedLabels,
	)
}

func TestMovingMainDecisionReadsProtectedDefaultBranchHead(t *testing.T) {
	t.Parallel()

	headSHA := "0123456789abcdef0123456789abcdef01234567"
	baseSHA := "1234567890abcdef1234567890abcdef12345678"
	mainSHA := "234567890abcdef1234567890abcdef123456789"
	binarySHA := strings.Repeat("a", 64)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/commits/"+headSHA+"/statuses":
			writeAppJSON(t, writer, []map[string]any{{
				"id": 77, "state": "success",
				"description": "main=" + mainSHA + ";binary=" + binarySHA + ";runs=3",
				"target_url":  server.URL + "/WuKongIM/WuKongIM/actions/runs/800",
				"context":     "Agent Moving Main / PR #9 / Gate #700",
				"creator": map[string]any{
					"login": "github-actions[bot]", "type": "Bot",
				},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			}})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/main":
			writeAppJSON(t, writer, map[string]any{
				"ref": "refs/heads/main",
				"object": map[string]any{
					"type": "commit", "sha": mainSHA,
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(
		issueagentgithub.ClientConfig{
			BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
		},
		server.Client(),
	)
	require.NoError(t, err)
	checkpoint := readyReviewCheckpointForApp(headSHA, baseSHA)
	facts, decision, err := movingMainDecision(
		context.Background(), client, 800, 700, 9, checkpoint,
		issueagentgithub.PullRequestFacts{
			Number: 9, State: "open", Draft: true,
			HeadRef: "agent/issue-42", HeadSHA: headSHA,
			BaseRef: "main", BaseSHA: mainSHA,
		},
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.DriftAlreadyFixedOnMain, decision)
	require.Equal(t, mainSHA, facts.CurrentMainSHA)
	require.Len(t, facts.MainRuns, 3)
}

func TestSweeperFindsExactCompletedWorkerArtifactForCurrentLease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	operationID := "sha256:" + strings.Repeat("a", 64)
	taskDigest := "sha256:" + strings.Repeat("b", 64)
	artifactName := "issue-agent-result-" + strings.Repeat("a", 16)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/actions/workflows/issue-agent-run.yml/runs":
			writeAppJSON(t, writer, map[string]any{
				"total_count": 1,
					"workflow_runs": []map[string]any{{
						"id": 77, "event": "workflow_dispatch",
						"status": "completed", "conclusion": "failure",
						"head_branch": "main",
						"head_sha": "0123456789abcdef0123456789abcdef01234567",
					"name":     "Agent Tool - Issue Worker",
					"path":     ".github/workflows/issue-agent-run.yml@main",
					"display_title": "Issue Agent worker Issue 42 operation " +
						operationID,
					"run_attempt": 1,
					"created_at":  now.Add(-30 * time.Minute).Format(time.RFC3339),
				}},
			})
		case "/repos/WuKongIM/WuKongIM/actions/runs/77/artifacts":
			writeAppJSON(t, writer, map[string]any{
				"total_count": 1,
				"artifacts": []map[string]any{{
					"id": 78, "name": artifactName, "size_in_bytes": 1024,
					"expired":              false,
					"archive_download_url": server.URL + "/artifacts/78/zip",
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(
		issueagentgithub.ClientConfig{
			BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
		},
		server.Client(),
	)
	require.NoError(t, err)
	artifacts, err := readCurrentLeaseArtifacts(
		context.Background(), client,
		issueagentcontract.Checkpoint{
			IssueNumber: 42, Generation: 3,
			Lease: &issueagentcontract.Lease{
				OperationID: operationID, Workflow: "issue-agent-run.yml",
				IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
				TaskSHA256: taskDigest,
			},
		},
		now,
	)
	require.NoError(t, err)
	require.Equal(t, []issueagentusecase.WorkerArtifact{{
		RunID: 77, OperationID: operationID,
		TaskDigest: taskDigest, Generation: 3,
	}}, artifacts)
}

func TestFirstReproductionPublicationRejectsPreexistingAgentBranch(t *testing.T) {
	t.Parallel()

	parentSHA := "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path ==
			"/repos/WuKongIM/WuKongIM/git/ref/heads/agent/issue-42" {
			writeAppJSON(t, writer, map[string]any{
				"ref": "refs/heads/agent/issue-42",
				"object": map[string]any{
					"type": "commit", "sha": parentSHA,
				},
			})
			return
		}
		http.NotFound(writer, request)
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(
		issueagentgithub.ClientConfig{
			BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
		},
		server.Client(),
	)
	require.NoError(t, err)
	_, err = publishOrReuseAgentCommit(
		context.Background(), client, "agent/issue-42",
		"1234567890abcdef1234567890abcdef12345678", parentSHA,
		"test(e2e): reproduce issue #42", "agent[bot]",
		issueagentcontract.ChangeSet{Files: []issueagentcontract.FileChange{{
			Path:      "test/e2e/issue_agent/issue_42/reproduction_test.go",
			Operation: issueagentcontract.FileOperationUpsert,
			Mode:      "100644", ContentBase64: "dGVzdA==",
		}}},
		map[string]bool{}, false,
	)
	require.ErrorIs(t, err, errExternalAgentHead)
}

func readyReviewCheckpointForApp(
	headSHA string,
	baseSHA string,
) issueagentcontract.Checkpoint {
	runs := func(sourceSHA string, outcome string) []issueagentcontract.ReproductionRun {
		result := make([]issueagentcontract.ReproductionRun, 3)
		for index := range result {
			result[index] = issueagentcontract.ReproductionRun{
				RunID: int64(index + 1), SourceSHA: sourceSHA,
				BinarySHA256:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				CommandSHA256:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				AssertionSHA256: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Outcome:         outcome,
			}
		}
		return result
	}
	return issueagentcontract.Checkpoint{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 1,
		State: issueagentcontract.StateReadyForReview,
		FrozenInput: issueagentcontract.FrozenInput{
			IssueBodySHA256: digestIssueBody(renderedCompleteBugForm()),
			AffectedVersion: "v2.1.0", AuthorizationEvent: "authorize-1",
			AuthorizedBy: "maintainer",
		},
		Versions: issueagentcontract.Versions{
			ReportedRef: "v2.1.0", AffectedSHA: headSHA,
			DiagnosisBaseSHA: baseSHA,
		},
		Reproduction: &issueagentcontract.Reproduction{
			TestFiles: []issueagentcontract.TestFile{{
				Path:    "test/e2e/issue_agent/issue_42/reproduction_test.go",
				BlobSHA: headSHA,
			}},
			Assertion:         "delivery succeeds",
			AssertionSHA256:   "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Topology:          "single-node-cluster",
			AffectedRuns:      runs(headSHA, "assertion_failed"),
			DiagnosisBaseRuns: runs(baseSHA, "passed"),
			ArtifactRunID:     1,
			ArtifactName:      "reproduction",
			ArtifactSHA256:    "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		},
		Work: &issueagentcontract.Work{
			Branch: "agent/issue-42", HeadSHA: headSHA, PRNumber: 9,
		},
		Diagnosis: &issueagentcontract.Diagnosis{
			Summary: "summary", ExternalSymptom: "symptom",
			CausalPath: "path", ViolatedInvariant: "invariant",
			ClusterSemantics:   "cluster",
			EvidenceSHA256:     "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			EvidenceReferences: []string{"evidence"},
			IntendedPaths:      []string{"internal/usecase/example"},
			ValidationSuites:   []string{"go-e2e", "go-fast"},
		},
		Validation: &issueagentcontract.Validation{
			HeadSHA: headSHA, TestMergeSHA: baseSHA,
			GateGeneration: 1, RequestRunID: 2, EvidenceRunID: 3,
			RequiredSuites: []string{"go-e2e", "go-fast"},
			LocalPasses:    3, Conclusion: "success",
		},
		NextAction: issueagentcontract.ActionRequestReview,
	}
}

func renderedCompleteBugForm() string {
	return `### Affected version

v2.1.0

### Environment, topology, and client

Linux; three-node-cluster; Go SDK

### Reproduction steps

1. connect
2. send

### Expected and actual result

Expected delivery; observed timeout.
`
}

func writeAppJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	require.NoError(t, json.NewEncoder(writer).Encode(value))
}
