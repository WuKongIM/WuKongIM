package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestPublisherPerformsDeterministicIntakeWithoutModelOrExecution(t *testing.T) {
	t.Parallel()

	body := renderedCompleteBugForm()
	server := httptest.NewServer(http.HandlerFunc(func(
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
