package issueagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestReaderClassifiesMissingGitHubObjects(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(server.Close)
	_, err := newTestClient(t, server).PullRequest(context.Background(), 42)
	require.ErrorIs(t, err, issueagentgithub.ErrNotFound)
}

func TestReaderCollectsRepositoryIssueAndPermissionFacts(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM":
			writeJSON(t, writer, map[string]any{
				"id": 7, "full_name": "WuKongIM/WuKongIM", "default_branch": "main",
			})
		case "/repos/WuKongIM/WuKongIM/issues/42":
			writeJSON(t, writer, map[string]any{
				"number": 42, "state": "open", "title": "broken", "body": "details",
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels": []map[string]any{
					{"name": "ready-for-agent"}, {"name": "bug"},
				},
			})
		case "/repos/WuKongIM/WuKongIM/collaborators/maintainer/permission":
			writeJSON(t, writer, map[string]any{
				"permission": "write",
				"user":       map[string]any{"login": "maintainer"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	reader := newTestClient(t, server)
	repository, err := reader.Repository(context.Background())
	require.NoError(t, err)
	require.Equal(t, "main", repository.DefaultBranch)
	issue, err := reader.Issue(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, []string{"bug", "ready-for-agent"}, issue.Labels)
	permission, err := reader.ActorPermission(context.Background(), "maintainer")
	require.NoError(t, err)
	require.Equal(t, issueagentgithub.PermissionWrite, permission)
}

func TestReaderCollectsPullRequestAndActionsFacts(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/pulls/9":
			writeJSON(t, writer, map[string]any{
				"number": 9, "state": "closed", "draft": false, "merged": true,
				"mergeable":        true,
				"merge_commit_sha": fortyHex("e"),
				"base":             map[string]any{"ref": "main", "sha": fortyHex("a")},
				"head":             map[string]any{"ref": "agent/issue-42", "sha": fortyHex("b")},
			})
		case "/repos/WuKongIM/WuKongIM/pulls/9/reviews":
			writeJSON(t, writer, []map[string]any{{
				"id": 1, "state": "APPROVED", "commit_id": fortyHex("b"),
				"user": map[string]any{"login": "reviewer"},
			}})
		case "/repos/WuKongIM/WuKongIM/pulls/9/files":
			writeJSON(t, writer, []map[string]any{{
				"filename": "pkg/example/fix.go", "status": "modified",
				"sha": fortyHex("c"), "additions": 2, "deletions": 1, "changes": 3,
			}})
		case "/repos/WuKongIM/WuKongIM/git/ref/heads/agent/issue-42":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/agent/issue-42",
				"object": map[string]any{"type": "commit", "sha": fortyHex("b")},
			})
		case "/repos/WuKongIM/WuKongIM/git/commits/" + fortyHex("b"):
			writeJSON(t, writer, map[string]any{
				"sha":          fortyHex("b"),
				"tree":         map[string]any{"sha": fortyHex("d")},
				"parents":      []map[string]any{{"sha": fortyHex("a")}},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case "/repos/WuKongIM/WuKongIM/actions/runs/11":
			writeJSON(t, writer, map[string]any{
				"id": 11, "event": "workflow_dispatch", "status": "completed",
				"conclusion": "success", "head_sha": fortyHex("b"),
				"name":          "Agent PR Validation Gate",
				"path":          ".github/workflows/gate.yml",
				"display_title": "bounded gate", "run_attempt": 1,
			})
		case "/repos/WuKongIM/WuKongIM/actions/runs/11/artifacts":
			writeJSON(t, writer, map[string]any{
				"total_count": 1,
				"artifacts": []map[string]any{{
					"id": 12, "name": "issue-agent-result", "size_in_bytes": 100,
					"expired": false, "archive_download_url": server.URL + "/artifact",
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	reader := newTestClient(t, server)
	pull, err := reader.PullRequest(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, "agent/issue-42", pull.HeadRef)
	require.True(t, pull.Merged)
	require.Equal(t, fortyHex("e"), pull.MergeCommit)
	reviews, err := reader.PullRequestReviews(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	files, err := reader.PullRequestFiles(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, files, 1)
	ref, err := reader.Ref(context.Background(), "agent/issue-42")
	require.NoError(t, err)
	commit, err := reader.Commit(context.Background(), ref.SHA)
	require.NoError(t, err)
	require.True(t, commit.Verified)
	run, err := reader.WorkflowRun(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, "success", run.Conclusion)
	artifacts, err := reader.RunArtifacts(context.Background(), 11)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
}

func TestReaderFindsCompleteBoundedWorkerRunInventorySinceLease(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 7, 28, 12, 0, 0, 500_000_000, time.UTC)
	lowerBound := since.Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		require.Equal(t,
			"/repos/WuKongIM/WuKongIM/actions/workflows/issue-agent-run.yml/runs",
			request.URL.Path,
		)
		require.Equal(t, ">="+lowerBound.Format(time.RFC3339),
			request.URL.Query().Get("created"))
		require.Equal(t, "workflow_dispatch", request.URL.Query().Get("event"))
		require.Equal(t, "completed", request.URL.Query().Get("status"))
		require.Equal(t, "100", request.URL.Query().Get("per_page"))
		require.Equal(t, "1", request.URL.Query().Get("page"))
		writeJSON(t, writer, map[string]any{
			"total_count": 1,
			"workflow_runs": []map[string]any{{
				"id": 11, "event": "workflow_dispatch", "status": "completed",
				"conclusion": "failure", "head_branch": "main",
				"head_sha": fortyHex("b"),
				"name":     "Agent Tool - Issue Worker",
				"path":     ".github/workflows/issue-agent-run.yml@main",
				"display_title": "Issue Agent worker Issue 42 operation sha256:" +
					strings.Repeat("a", 64),
				"run_attempt": 1, "created_at": lowerBound,
			}},
		})
	}))
	t.Cleanup(server.Close)

	runs, err := newTestClient(t, server).CompletedWorkflowRunsSince(
		context.Background(), "issue-agent-run.yml", since,
	)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, int64(11), runs[0].ID)
	require.Equal(t, lowerBound, runs[0].CreatedAt)
}

func TestReaderRejectsRecoverableWorkerRunOutsideProtectedMainIdentity(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		writeJSON(t, writer, map[string]any{
			"total_count": 1,
			"workflow_runs": []map[string]any{{
				"id": 11, "event": "workflow_dispatch", "status": "completed",
				"conclusion": "failure", "head_branch": "main",
				"head_sha": fortyHex("b"), "name": "Agent Tool - Issue Worker",
				"path": ".github/workflows/issue-agent-run.yml@evil",
				"display_title": "Issue Agent worker Issue 42 operation sha256:" +
					strings.Repeat("a", 64),
				"run_attempt": 1, "created_at": since.Add(time.Minute),
			}},
		})
	}))
	t.Cleanup(server.Close)

	_, err := newTestClient(t, server).CompletedWorkflowRunsSince(
		context.Background(), "issue-agent-run.yml", since,
	)
	require.Error(t, err)
}

func TestReaderRejectsScopeAndCountMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM":
			writeJSON(t, writer, map[string]any{
				"id": 7, "full_name": "attacker/repository", "default_branch": "main",
			})
		case "/repos/WuKongIM/WuKongIM/actions/runs/11/artifacts":
			writeJSON(t, writer, map[string]any{
				"total_count": 2,
				"artifacts":   []map[string]any{{"id": 12, "name": "one", "size_in_bytes": 1}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	reader := newTestClient(t, server)
	_, err := reader.Repository(context.Background())
	require.Error(t, err)
	_, err = reader.RunArtifacts(context.Background(), 11)
	require.Error(t, err)
}

func newTestClient(t *testing.T, server *httptest.Server) *issueagentgithub.Client {
	t.Helper()
	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
		Token: "token", MaxPages: 3, MaxBodyBytes: 1 << 20,
	}, server.Client())
	require.NoError(t, err)
	return client
}

func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	require.NoError(t, json.NewEncoder(writer).Encode(value))
}

func fortyHex(value string) string {
	result := ""
	for len(result) < 40 {
		result += value
	}
	return result
}
