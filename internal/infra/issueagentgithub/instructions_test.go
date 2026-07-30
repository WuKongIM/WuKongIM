package issueagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestInstructionFileDigestsReadExactRecursiveSourceTree(t *testing.T) {
	t.Parallel()

	commitSHA := "1111111111111111111111111111111111111111"
	treeSHA := "2222222222222222222222222222222222222222"
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/commits/" + commitSHA:
			require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"sha":     commitSHA,
				"tree":    map[string]any{"sha": treeSHA},
				"message": "source",
				"parents": []map[string]any{{
					"sha": "3333333333333333333333333333333333333333",
				}},
			}))
		case "/repos/WuKongIM/WuKongIM/git/trees/" + treeSHA:
			require.Equal(t, "1", request.URL.Query().Get("recursive"))
			require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"sha": treeSHA, "truncated": false,
				"tree": []map[string]any{
					{"path": "internal/example/FLOW.md", "mode": "100644",
						"type": "blob", "sha": "5555555555555555555555555555555555555555"},
					{"path": "README.md", "mode": "100644",
						"type": "blob", "sha": "6666666666666666666666666666666666666666"},
					{"path": "AGENTS.md", "mode": "100644",
						"type": "blob", "sha": "4444444444444444444444444444444444444444"},
				},
			}))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
		Token: "token", MaxPages: 1, MaxBodyBytes: 1 << 20,
	}, server.Client())
	require.NoError(t, err)

	digests, err := client.InstructionFileDigests(
		context.Background(),
		commitSHA,
	)
	require.NoError(t, err)
	require.Len(t, digests, 2)
	require.Equal(t, "AGENTS.md", digests[0].Path)
	require.Equal(t,
		"4444444444444444444444444444444444444444",
		digests[0].GitBlobSHA,
	)
	require.Equal(t, "internal/example/FLOW.md", digests[1].Path)
}
