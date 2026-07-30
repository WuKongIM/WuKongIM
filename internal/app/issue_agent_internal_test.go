package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	issueagent "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestResolveIssueNumberRotatesScheduledTrackedIssues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		require.Equal(t, "/repos/WuKongIM/WuKongIM/issues", request.URL.Path)
		require.Equal(t, "ready-for-agent", request.URL.Query().Get("labels"))
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
			{"number": 9},
			{"number": 3},
		}))
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
	number, err := resolveIssueNumber(
		context.Background(),
		client,
		issueagentcli.ReconcileGitHubRequest{
			EventName: "schedule",
			Now:       time.Unix(int64((5*time.Minute)/time.Second), 0).UTC(),
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(9), number)
}

func TestResolveIssueNumberReadsCredentialFreePRSignal(t *testing.T) {
	t.Parallel()

	eventPath := filepath.Join(t.TempDir(), "event.json")
	require.NoError(t, os.WriteFile(eventPath, []byte(`{
		"workflow_run": {
			"id": 99,
			"name": "Safety Automation - Issue Agent PR Signal",
			"event": "pull_request",
			"conclusion": "success",
			"head_branch": "agent/issue-42",
			"head_repository": {"full_name": "WuKongIM/WuKongIM"},
			"pull_requests": [{
				"number": 77,
				"head": {"ref": "agent/issue-42"}
			}],
			"actor": {"login": "maintainer"}
		}
	}`), 0o600))
	number, err := resolveIssueNumber(
		context.Background(),
		nil,
		issueagentcli.ReconcileGitHubRequest{
			Repository: "WuKongIM/WuKongIM",
			EventName:  "workflow_run",
			EventPath:  eventPath,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(42), number)
}

func TestReviewWakeupUsesTrustedWorkflowRunIdentity(t *testing.T) {
	t.Parallel()

	var event issueAgentEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"workflow_run": {
			"id": 99,
			"name": "Safety Automation - Issue Agent PR Signal",
			"event": "pull_request_review_comment",
			"conclusion": "success",
			"head_branch": "agent/issue-42",
			"head_repository": {"full_name": "WuKongIM/WuKongIM"},
			"pull_requests": [{
				"number": 77,
				"head": {"ref": "agent/issue-42"}
			}],
			"actor": {"login": "maintainer"}
		}
	}`), &event))
	wakeup, relevant, err := reviewWakeupFromEvent("workflow_run", event)
	require.NoError(t, err)
	require.True(t, relevant)
	require.Equal(t, int64(77), wakeup.PullRequest.Number)
	require.Equal(t, "agent/issue-42", wakeup.PullRequest.Head.Ref)
	require.Equal(t, "maintainer", wakeup.Actor)
	require.Equal(t, "workflow_run:99", wakeup.EventID)
}

func TestReviewWakeupRejectsUntrustedWorkflowName(t *testing.T) {
	t.Parallel()

	var event issueAgentEvent
	require.NoError(t, json.Unmarshal([]byte(`{
		"workflow_run": {
			"id": 99,
			"name": "Untrusted Workflow",
			"event": "pull_request_review",
			"conclusion": "success",
			"head_branch": "agent/issue-42",
			"head_repository": {"full_name": "WuKongIM/WuKongIM"},
			"pull_requests": [{
				"number": 77,
				"head": {"ref": "agent/issue-42"}
			}],
			"actor": {"login": "maintainer"}
		}
	}`), &event))
	_, relevant, err := reviewWakeupFromEvent("workflow_run", event)
	require.NoError(t, err)
	require.False(t, relevant)
}

func TestIssueFormValueReadsOneExactAffectedVersionSection(t *testing.T) {
	t.Parallel()

	value, ambiguous := issueagent.IssueFormValue(
		"### Environment\nLinux\n\n### Affected version\n\nv2.1.0\n\n"+
			"### Reproduction steps\n1. Start\n",
		"Affected version",
	)
	require.False(t, ambiguous)
	require.Equal(t, "v2.1.0", value)
}

func TestIssueFormValueRejectsAmbiguousAffectedVersionSections(t *testing.T) {
	t.Parallel()

	_, ambiguous := issueagent.IssueFormValue(
		"### Affected version\nv2.1.0\n### Affected version\nv2.2.0\n",
		"Affected version",
	)
	require.True(t, ambiguous)
}

func TestCurrentAuthorizationKeepsAppliedFixAuthorityWithoutReplayingCommand(
	t *testing.T,
) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path ==
			"/repos/WuKongIM/WuKongIM/collaborators/reporter/permission" {
			http.NotFound(writer, request)
			return
		}
		require.Equal(t,
			"/repos/WuKongIM/WuKongIM/collaborators/maintainer/permission",
			request.URL.Path,
		)
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"permission": "write",
			"user":       map[string]string{"login": "maintainer"},
		}))
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
	current := &contract.IssueAgentState{
		Authorization: &contract.AuthorizationRecord{
			Actor: "maintainer", Permission: "write",
			EventID: "issue_comment:9", Command: "/agent fix",
		},
	}
	authorization, _, err := currentAuthorization(
		context.Background(),
		client,
		issueagentgithub.IssueFacts{
			Number: 42, Author: "reporter", AuthorAssociation: "NONE",
		},
		[]issueagentgithub.IssueComment{{
			ID: 9, Author: "maintainer", Body: "/agent fix",
			UpdatedAt: time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
		}},
		current,
	)
	require.NoError(t, err)
	require.NotNil(t, authorization)
	require.Equal(t, "issue_comment:9", authorization.EventID)
	require.Empty(t, authorization.Command)
}

func TestCurrentAuthorizationLetsLatestMaintainerCommandOverrideTrustedAuthor(
	t *testing.T,
) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		login := "reporter"
		permission := "admin"
		if request.URL.Path ==
			"/repos/WuKongIM/WuKongIM/collaborators/maintainer/permission" {
			login = "maintainer"
			permission = "write"
		}
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"permission": permission,
			"user":       map[string]string{"login": login},
		}))
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
	authorization, _, err := currentAuthorization(
		context.Background(),
		client,
		issueagentgithub.IssueFacts{
			Number: 42, Author: "reporter", AuthorAssociation: "MEMBER",
		},
		[]issueagentgithub.IssueComment{{
			ID: 10, Author: "maintainer", Body: "/agent cancel",
			UpdatedAt: time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
		}},
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, authorization)
	require.Equal(t, "/agent cancel", authorization.Command)
	require.Equal(t, "maintainer", authorization.Actor)
}
