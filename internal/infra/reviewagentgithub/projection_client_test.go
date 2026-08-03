package reviewagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
)

func TestClientMergesOnlyTheExactPullRequestHead(t *testing.T) {
	t.Parallel()

	headSHA := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		require.Equal(t, http.MethodPut, request.Method)
		require.Equal(
			t,
			"/repos/WuKongIM/WuKongIM/pulls/42/merge",
			request.URL.Path,
		)
		var body struct {
			SHA         string `json:"sha"`
			MergeMethod string `json:"merge_method"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		require.Equal(t, headSHA, body.SHA)
		require.Equal(t, "merge", body.MergeMethod)
		writeJSON(writer, map[string]any{
			"sha":     strings.Repeat("b", 40),
			"merged":  true,
			"message": "Pull Request successfully merged",
		})
	}))
	t.Cleanup(server.Close)
	client, err := github.NewClient(
		github.ClientConfig{
			BaseURL: server.URL, GraphQLURL: server.URL + "/graphql",
			Repository: "WuKongIM/WuKongIM", Token: "token",
			MaxPages: 100, MaxBodyBytes: 16 << 20,
		},
		server.Client(),
	)
	require.NoError(t, err)

	require.NoError(t, client.MergePullRequest(
		context.Background(),
		42,
		headSHA,
	))
}
