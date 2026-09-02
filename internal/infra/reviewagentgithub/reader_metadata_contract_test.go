package reviewagentgithub_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	"github.com/stretchr/testify/require"
)

func TestReaderMetadataPreservesReviewablePatchesAndCompleteChecks(t *testing.T) {
	t.Parallel()

	patch := "@@ -2 +2 @@\n-old ownership\n+new ownership\n"
	client := newMetadataContractClient(t, 1, false, patch)

	snapshot, err := client.ReadPullRequestMetadata(context.Background(), 42)
	require.NoError(t, err)
	require.Empty(t, snapshot.Facts.ContextFailureReason)
	require.True(t, snapshot.Inventory.Complete)
	require.Equal(t, 2, snapshot.Inventory.DeclaredFiles)
	require.Equal(t, int64(len(patch)), snapshot.Inventory.TotalBytes)
	require.Equal(t, int64(7), snapshot.Inventory.TotalLines)
	require.Equal(t, []contract.ChangedFile{
		{
			Path: "internal/runtime/queue.go", Status: contract.FileStatusModified,
			Additions: 3, Deletions: 2,
		},
		{
			Path: "docs/old.md", Status: contract.FileStatusRemoved,
			Additions: 0, Deletions: 2,
		},
	}, snapshot.Inventory.Files)
	require.Equal(t, map[string]string{
		"internal/runtime/queue.go": patch,
	}, snapshot.CommentPatches)
	require.Equal(t, []github.CheckRun{{
		ID: 91, Name: "unit", Status: "completed", Conclusion: "success",
		AppSlug: "github-actions", ExternalID: "unit-main",
	}}, snapshot.Checks)
}

func TestReaderMetadataFailsClosedWhenCheckInventoryIsIncomplete(t *testing.T) {
	t.Parallel()

	client := newMetadataContractClient(t, 2, false, "@@ -1 +1 @@\n-old\n+new\n")

	snapshot, err := client.ReadPullRequestMetadata(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(
		t,
		"GitHub Check pagination is incomplete",
		snapshot.Facts.ContextFailureReason,
	)
	require.Nil(t, snapshot.Checks)
	require.True(t, snapshot.Inventory.Complete)
}

func TestReaderMetadataReadsEveryStableCheckPage(t *testing.T) {
	t.Parallel()

	client := newMetadataContractClient(t, 2, true, "@@ -1 +1 @@\n-old\n+new\n")

	snapshot, err := client.ReadPullRequestMetadata(context.Background(), 42)
	require.NoError(t, err)
	require.Empty(t, snapshot.Facts.ContextFailureReason)
	require.Len(t, snapshot.Checks, 2)
	require.Equal(t, []int64{91, 92}, []int64{
		snapshot.Checks[0].ID,
		snapshot.Checks[1].ID,
	})
}

func newMetadataContractClient(
	t *testing.T,
	checkTotal int,
	paginateChecks bool,
	patch string,
) *github.Client {
	t.Helper()
	headSHA := strings.Repeat("a", 40)
	baseSHA := strings.Repeat("b", 40)
	mergeSHA := strings.Repeat("c", 40)
	fileSHA := strings.Repeat("d", 40)
	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM":
			writeJSON(writer, map[string]any{
				"full_name": "WuKongIM/WuKongIM", "default_branch": "main",
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42":
			writeJSON(writer, map[string]any{
				"number": 42, "state": "open", "draft": false,
				"title": "Keep queue ownership atomic", "body": "",
				"changed_files": 2, "additions": 3, "deletions": 4,
				"mergeable": true, "mergeable_state": "clean",
				"merge_commit_sha":   mergeSHA,
				"user":               map[string]any{"login": "alice", "type": "User"},
				"author_association": "MEMBER",
				"base":               map[string]any{"ref": "main", "sha": baseSHA},
				"head": map[string]any{
					"ref": "queue-fix", "sha": headSHA,
					"repo": map[string]any{"full_name": "WuKongIM/WuKongIM"},
				},
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42/files":
			require.Equal(t, "1", request.URL.Query().Get("page"))
			require.Equal(t, "100", request.URL.Query().Get("per_page"))
			writeJSON(writer, []map[string]any{
				{
					"filename": "internal/runtime/queue.go", "status": "modified",
					"sha": fileSHA, "additions": 3, "deletions": 2,
					"patch": patch,
				},
				{
					"filename": "docs/old.md", "status": "removed",
					"sha": strings.Repeat("e", 40), "additions": 0, "deletions": 2,
				},
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42/reviews",
			"/repos/WuKongIM/WuKongIM/issues/42/comments",
			"/repos/WuKongIM/WuKongIM/pulls/42/comments":
			writeJSON(writer, []any{})
		case "/graphql":
			writeJSON(writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"nameWithOwner": "WuKongIM/WuKongIM",
						"pullRequest": map[string]any{
							"number": 42,
							"reviewThreads": map[string]any{
								"totalCount": 0, "nodes": []any{},
								"pageInfo": map[string]any{
									"hasNextPage": false, "endCursor": "",
								},
							},
						},
					},
				},
			})
		case "/repos/WuKongIM/WuKongIM/commits/" + headSHA + "/check-runs":
			page := request.URL.Query().Get("page")
			if paginateChecks && page == "1" {
				writer.Header().Set(
					"Link",
					"<"+reviewMemoryBaseURL+request.URL.Path+"?page=2&per_page=100>; rel=\"next\"",
				)
			}
			id := int64(91)
			name := "unit"
			externalID := "unit-main"
			if paginateChecks && page == "2" {
				id = 92
				name = "contracts"
				externalID = "contracts-main"
			}
			writeJSON(writer, map[string]any{
				"total_count": checkTotal,
				"check_runs": []map[string]any{{
					"id": id, "name": name, "status": "completed",
					"conclusion": "success", "external_id": externalID,
					"app": map[string]any{"slug": "github-actions"},
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	return newReviewMemoryClient(t, 4, 2<<20, handler)
}
