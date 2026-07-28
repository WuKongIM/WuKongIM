package issueagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

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
				"labels":             []map[string]any{{"name": "ready-for-agent"}},
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
	require.Equal(t, []string{"ready-for-agent"}, issue.Labels)
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
				"number": 9, "state": "open", "draft": true, "mergeable": true,
				"base": map[string]any{"ref": "main", "sha": fortyHex("a")},
				"head": map[string]any{"ref": "agent/issue-42", "sha": fortyHex("b")},
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
