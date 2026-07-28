package issueagentgithub_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestPublisherWritesBoundedIssueAndDraftPRProjections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			writeJSON(t, writer, map[string]any{
				"id": 51, "body": "checkpoint",
				"user":       map[string]any{"login": "agent[bot]", "type": "Bot"},
				"created_at": "2026-07-28T12:00:00Z",
				"updated_at": "2026-07-28T12:00:00Z",
			})
		case request.Method == http.MethodPut &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/labels":
			writeJSON(t, writer, []map[string]any{
				{"name": "agent:reproduced"}, {"name": "bug"},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/pulls":
			writer.WriteHeader(http.StatusCreated)
			writeJSON(t, writer, map[string]any{
				"number": 9, "state": "open", "draft": true, "mergeable": nil,
				"base": map[string]any{"ref": "main", "sha": fortyHex("a")},
				"head": map[string]any{"ref": "agent/issue-42", "sha": fortyHex("b")},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/pulls/9/ready_for_review":
			writeJSON(t, writer, map[string]any{
				"number": 9, "state": "open", "draft": false, "mergeable": true,
				"base": map[string]any{"ref": "main", "sha": fortyHex("a")},
				"head": map[string]any{"ref": "agent/issue-42", "sha": fortyHex("b")},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	comment, err := client.CreateIssueComment(context.Background(), 42, "checkpoint")
	require.NoError(t, err)
	require.Equal(t, int64(51), comment.ID)
	require.NoError(t, client.SetIssueLabels(
		context.Background(), 42, []string{"agent:reproduced", "bug"},
	))
	pull, err := client.CreateDraftPullRequest(context.Background(), issueagentgithub.DraftPullRequest{
		Title: "fix(agent): issue #42", Body: "summary",
		Head: "agent/issue-42", Base: "main",
	})
	require.NoError(t, err)
	require.True(t, pull.Draft)
	ready, err := client.MarkPullRequestReady(context.Background(), 9)
	require.NoError(t, err)
	require.False(t, ready.Draft)
}
