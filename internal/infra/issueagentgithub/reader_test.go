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

func TestReaderCollectsIssueAndPermissionFacts(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
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
	issue, err := reader.Issue(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, []string{"bug", "ready-for-agent"}, issue.Labels)
	permission, err := reader.ActorPermission(context.Background(), "maintainer")
	require.NoError(t, err)
	require.Equal(t, issueagentgithub.PermissionWrite, permission)
}

func TestReaderCollectsPullRequestAndGitFacts(t *testing.T) {
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
	ref, err := reader.Ref(context.Background(), "agent/issue-42")
	require.NoError(t, err)
	commit, err := reader.Commit(context.Background(), ref.SHA)
	require.NoError(t, err)
	require.True(t, commit.Verified)
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
