package issueagentgithub_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestClientReadsAllBoundedIssueCommentPages(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		require.Equal(t, "Bearer read-token", request.Header.Get("Authorization"))
		require.Equal(t, "2022-11-28", request.Header.Get("X-GitHub-Api-Version"))
		require.Equal(t, "/repos/WuKongIM/WuKongIM/issues/42/comments", request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Query().Get("page") {
		case "1":
			writer.Header().Set(
				"Link",
				fmt.Sprintf(`<%s/repos/WuKongIM/WuKongIM/issues/42/comments?per_page=100&page=2>; rel="next"`, serverURLFromRequest(request)),
			)
			require.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{{
				"id": 1,
				"user": map[string]any{
					"login": "user", "type": "User",
				},
				"body":       "first",
				"created_at": now,
				"updated_at": now,
			}}))
		case "2":
			require.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{{
				"id": 2,
				"user": map[string]any{
					"login": "agent[bot]", "type": "Bot",
				},
				"body":       "second",
				"created_at": now,
				"updated_at": now,
			}}))
		default:
			t.Fatalf("unexpected page %q", request.URL.Query().Get("page"))
		}
	}))
	t.Cleanup(server.Close)

	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL:      server.URL,
		Repository:   "WuKongIM/WuKongIM",
		Token:        "read-token",
		MaxPages:     3,
		MaxBodyBytes: 1 << 20,
	}, server.Client())
	require.NoError(t, err)

	comments, err := client.ListIssueComments(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, comments, 2)
	require.Equal(t, int64(1), comments[0].ID)
	require.Equal(t, int64(2), comments[1].ID)
}

func TestClientRejectsMalformedResponsesAndCrossHostRedirects(t *testing.T) {
	t.Parallel()

	t.Run("malformed response", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"id":0,"user":{"login":"","type":"User"},"body":"x","created_at":"invalid"}]`))
		}))
		t.Cleanup(server.Close)
		client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
			BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 1, MaxBodyBytes: 4096,
		}, server.Client())
		require.NoError(t, err)
		_, err = client.ListIssueComments(context.Background(), 42)
		require.Error(t, err)
	})

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		target := httptest.NewServer(http.HandlerFunc(func(
			http.ResponseWriter,
			*http.Request,
		) {
		}))
		t.Cleanup(target.Close)
		source := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			http.Redirect(writer, request, target.URL, http.StatusFound)
		}))
		t.Cleanup(source.Close)
		client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
			BaseURL: source.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 1, MaxBodyBytes: 4096,
		}, source.Client())
		require.NoError(t, err)
		_, err = client.ListIssueComments(context.Background(), 42)
		require.Error(t, err)
	})
}

func TestClientIssueInventoryIsCompleteBoundedAndPRFree(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		require.Equal(t, "open", request.URL.Query().Get("state"))
		require.Equal(t, "ready-for-agent", request.URL.Query().Get("labels"))
		require.Equal(t, "41", request.URL.Query().Get("per_page"))
		require.NoError(t, json.NewEncoder(writer).Encode([]map[string]any{
			{"number": 9},
			{"number": 7, "pull_request": map[string]any{"url": "ignored"}},
			{"number": 3},
		}))
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
		Token: "token", MaxPages: 1, MaxBodyBytes: 4096,
	}, server.Client())
	require.NoError(t, err)
	issues, err := client.ListOpenIssueNumbersByLabel(
		context.Background(), "ready-for-agent",
	)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 9}, issues)
}

func TestClientIssueInventoryRejectsMoreThanFortyTrackedIssues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		payload := make([]map[string]any, 41)
		for index := range payload {
			payload[index] = map[string]any{"number": index + 1}
		}
		require.NoError(t, json.NewEncoder(writer).Encode(payload))
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
		Token: "token", MaxPages: 1, MaxBodyBytes: 1 << 20,
	}, server.Client())
	require.NoError(t, err)
	_, err = client.ListOpenIssueNumbersByLabel(
		context.Background(), "ready-for-agent",
	)
	require.EqualError(t, err,
		"Issue inventory exceeds the global accounting bound")
}

func serverURLFromRequest(request *http.Request) string {
	return "http://" + request.Host
}
