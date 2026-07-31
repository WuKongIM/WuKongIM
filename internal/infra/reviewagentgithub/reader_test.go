package reviewagentgithub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
)

func TestReaderBuildsCompleteFreshPullRequestSnapshot(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	merge := strings.Repeat("c", 40)
	blobOne := strings.Repeat("1", 40)
	blobTwo := strings.Repeat("2", 40)
	var pullReads atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/graphql":
			writeJSON(writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"nameWithOwner": "WuKongIM/WuKongIM",
						"pullRequest": map[string]any{
							"number": 42,
							"reviewThreads": map[string]any{
								"totalCount": 1,
								"nodes": []map[string]any{{
									"id": "PRRT_1", "isResolved": false,
									"path": "internal/app/a.go", "line": 1,
								}},
								"pageInfo": map[string]any{
									"hasNextPage": false, "endCursor": "",
								},
							},
						},
					},
				},
			})
		case "/repos/WuKongIM/WuKongIM":
			writeJSON(writer, map[string]any{
				"full_name":      "WuKongIM/WuKongIM",
				"default_branch": "main",
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42":
			mergeable := any(true)
			mergeCommitSHA := merge
			if pullReads.Add(1) == 1 {
				mergeable = nil
				mergeCommitSHA = ""
			}
			writeJSON(writer, map[string]any{
				"number": 42, "state": "open", "draft": false,
				"title": "Fix queue", "body": "Fixes #17",
				"changed_files": 2, "additions": 5, "deletions": 2,
				"mergeable": mergeable, "mergeable_state": "blocked",
				"merge_commit_sha":   mergeCommitSHA,
				"user":               map[string]any{"login": "alice", "type": "User"},
				"author_association": "CONTRIBUTOR",
				"base":               map[string]any{"ref": "main", "sha": base},
				"head": map[string]any{
					"ref": "feature", "sha": head,
					"repo": map[string]any{"full_name": "alice/WuKongIM"},
				},
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42/files":
			if request.URL.Query().Get("page") == "1" {
				writer.Header().Set(
					"Link",
					fmt.Sprintf(
						`<%s/repos/WuKongIM/WuKongIM/pulls/42/files?page=2&per_page=100>; rel="next"`,
						server.URL,
					),
				)
				writeJSON(writer, []map[string]any{{
					"filename": "internal/app/a.go", "status": "modified",
					"sha": blobOne, "additions": 3, "deletions": 1,
					"patch": "@@ -1 +1 @@\n-package old\n+package app\n",
				}})
			} else {
				writeJSON(writer, []map[string]any{{
					"filename": "docs/new.md", "previous_filename": "README.md",
					"status": "renamed", "sha": blobTwo,
					"additions": 2, "deletions": 1,
					"patch": "@@ -1 +1 @@\n-old\n+new\n",
				}})
			}
		case "/repos/WuKongIM/WuKongIM/git/trees/" + head:
			writeJSON(writer, map[string]any{
				"truncated": false,
				"tree": []map[string]any{
					{"path": "internal/app/a.go", "mode": "100644", "type": "blob", "sha": blobOne},
					{"path": "docs/new.md", "mode": "100644", "type": "blob", "sha": blobTwo},
				},
			})
		case "/repos/WuKongIM/WuKongIM/git/trees/" + base:
			writeJSON(writer, map[string]any{
				"truncated": false,
				"tree": []map[string]any{
					{"path": "internal/app/a.go", "mode": "100644", "type": "blob", "sha": blobOne},
					{"path": "README.md", "mode": "100644", "type": "blob", "sha": blobTwo},
				},
			})
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + blobOne:
			writeBlob(writer, "package app\n")
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + blobTwo:
			writeBlob(writer, "new\n")
		case "/repos/WuKongIM/WuKongIM/pulls/42/reviews":
			writeJSON(writer, []map[string]any{{
				"id": 9, "state": "CHANGES_REQUESTED", "body": "Fix race",
				"commit_id":    head,
				"user":         map[string]any{"login": "bob", "type": "User"},
				"submitted_at": "2026-07-30T10:00:00Z",
			}})
		case "/repos/WuKongIM/WuKongIM/issues/42/comments":
			writeJSON(writer, []map[string]any{{
				"id": 10, "body": "@review-agent status",
				"user":       map[string]any{"login": "alice", "type": "User"},
				"created_at": "2026-07-30T10:01:00Z",
				"updated_at": "2026-07-30T10:01:00Z",
			}})
		case "/repos/WuKongIM/WuKongIM/pulls/42/comments":
			writeJSON(writer, []map[string]any{
				{
					"id": 11, "body": "inline", "path": "internal/app/a.go",
					"line": 1, "side": "RIGHT", "in_reply_to_id": 0,
					"user":       map[string]any{"login": "bob", "type": "User"},
					"created_at": "2026-07-30T10:02:00Z",
					"updated_at": "2026-07-30T10:02:00Z",
				},
				{
					"id": 13, "body": "file level", "path": "docs/new.md",
					"line": 0, "side": "", "in_reply_to_id": 0,
					"user":       map[string]any{"login": "carol", "type": "User"},
					"created_at": "2026-07-30T10:03:00Z",
					"updated_at": "2026-07-30T10:03:00Z",
				},
			})
		case "/repos/WuKongIM/WuKongIM/commits/" + head + "/check-runs":
			writeJSON(writer, map[string]any{
				"total_count": 1,
				"check_runs": []map[string]any{{
					"id": 12, "name": "unit", "status": "completed",
					"conclusion":  "success",
					"app":         map[string]any{"slug": "github-actions"},
					"external_id": "unit-1",
				}},
			})
		case "/repos/WuKongIM/WuKongIM/issues/17":
			writeJSON(writer, map[string]any{
				"number": 17, "state": "open", "title": "Queue race",
				"body": "Preserve ordering.", "pull_request": nil,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := github.NewClient(github.ClientConfig{
		BaseURL: server.URL, GraphQLURL: server.URL + "/graphql",
		Repository: "WuKongIM/WuKongIM", Token: "token",
		MaxPages: 10, MaxBodyBytes: 2 << 20,
	}, server.Client())
	require.NoError(t, err)
	snapshot, err := client.ReadPullRequest(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, head, snapshot.Facts.HeadSHA)
	require.Equal(t, merge, snapshot.Facts.TestMergeSHA)
	require.False(t, snapshot.Facts.Draft)
	require.Equal(
		t,
		"clean",
		string(snapshot.Facts.Mergeability),
	)
	require.True(t, snapshot.Facts.HumanChangesRequested)
	require.Equal(t, int64(17), snapshot.LinkedIssues[0].Number)
	require.True(t, snapshot.Inventory.Complete)
	require.Equal(t, 2, snapshot.Inventory.DeclaredFiles)
	require.Contains(t, snapshot.Inventory.Files[1].Patch, "@@ -1,1 +1,1 @@")
	require.Equal(t, "package app\n", snapshot.Inventory.Files[1].Content)
	require.Equal(
		t,
		contract.FileStatusRenamed,
		snapshot.Inventory.Files[0].Status,
	)
	require.Equal(t, "README.md", snapshot.Inventory.Files[0].PreviousPath)
	require.Len(t, snapshot.Reviews, 1)
	require.Len(t, snapshot.IssueComments, 1)
	require.Len(t, snapshot.ReviewComments, 2)
	require.Zero(t, snapshot.ReviewComments[1].Line)
	require.Empty(t, snapshot.ReviewComments[1].Side)
	require.Len(t, snapshot.ReviewThreads, 1)
	require.Len(t, snapshot.Checks, 1)
	require.Equal(t, int32(2), pullReads.Load())
}

func TestReaderReturnsPersistentlyUnknownMergeabilityForFailClosedPolicy(
	t *testing.T,
) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	var pullReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/graphql":
			writeJSON(writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"nameWithOwner": "WuKongIM/WuKongIM",
						"pullRequest": map[string]any{
							"number": 42,
							"reviewThreads": map[string]any{
								"totalCount": 0,
								"nodes":      []any{},
								"pageInfo": map[string]any{
									"hasNextPage": false,
									"endCursor":   "",
								},
							},
						},
					},
				},
			})
		case "/repos/WuKongIM/WuKongIM":
			writeJSON(writer, map[string]any{
				"full_name":      "WuKongIM/WuKongIM",
				"default_branch": "main",
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42":
			pullReads.Add(1)
			writeJSON(writer, map[string]any{
				"number": 42, "state": "open", "draft": false,
				"title": "Fix queue", "body": "",
				"changed_files": 1, "additions": 1, "deletions": 0,
				"mergeable": nil, "mergeable_state": "unknown",
				"merge_commit_sha":   "",
				"user":               map[string]any{"login": "alice", "type": "User"},
				"author_association": "CONTRIBUTOR",
				"base":               map[string]any{"ref": "main", "sha": base},
				"head": map[string]any{
					"ref": "feature", "sha": head,
					"repo": map[string]any{"full_name": "alice/WuKongIM"},
				},
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42/files",
			"/repos/WuKongIM/WuKongIM/pulls/42/reviews",
			"/repos/WuKongIM/WuKongIM/issues/42/comments",
			"/repos/WuKongIM/WuKongIM/pulls/42/comments":
			writeJSON(writer, []any{})
		case "/repos/WuKongIM/WuKongIM/commits/" + head + "/check-runs":
			writeJSON(writer, map[string]any{
				"total_count": 0,
				"check_runs":  []any{},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client, err := github.NewClient(github.ClientConfig{
		BaseURL: server.URL, GraphQLURL: server.URL + "/graphql",
		Repository: "WuKongIM/WuKongIM", Token: "token",
		MaxPages: 10, MaxBodyBytes: 2 << 20,
	}, server.Client())
	require.NoError(t, err)
	snapshot, err := client.ReadPullRequestMetadata(context.Background(), 42)
	require.NoError(t, err)
	require.Empty(t, snapshot.Facts.TestMergeSHA)
	require.Equal(
		t,
		"unknown",
		string(snapshot.Facts.Mergeability),
	)
	require.Equal(t, int32(5), pullReads.Load())
}

func TestReaderFailsClosedWhenFilePaginationIsIncomplete(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	merge := strings.Repeat("c", 40)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/graphql":
			writeJSON(writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"nameWithOwner": "WuKongIM/WuKongIM",
						"pullRequest": map[string]any{
							"number": 42,
							"reviewThreads": map[string]any{
								"totalCount": 0,
								"nodes":      []any{},
								"pageInfo": map[string]any{
									"hasNextPage": false,
									"endCursor":   "",
								},
							},
						},
					},
				},
			})
		case "/repos/WuKongIM/WuKongIM":
			writeJSON(writer, map[string]any{
				"full_name": "WuKongIM/WuKongIM", "default_branch": "main",
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42":
			writeJSON(writer, map[string]any{
				"number": 42, "state": "open", "draft": false,
				"title": "Fix", "body": "", "changed_files": 2,
				"additions": 1, "deletions": 0, "mergeable": true,
				"mergeable_state": "clean", "merge_commit_sha": merge,
				"user":               map[string]any{"login": "alice", "type": "User"},
				"author_association": "MEMBER",
				"base":               map[string]any{"ref": "main", "sha": base},
				"head": map[string]any{
					"ref": "feature", "sha": head,
					"repo": map[string]any{"full_name": "WuKongIM/WuKongIM"},
				},
			})
		case "/repos/WuKongIM/WuKongIM/pulls/42/files":
			writeJSON(writer, []map[string]any{})
		case "/repos/WuKongIM/WuKongIM/pulls/42/reviews",
			"/repos/WuKongIM/WuKongIM/issues/42/comments",
			"/repos/WuKongIM/WuKongIM/pulls/42/comments":
			writeJSON(writer, []any{})
		case "/repos/WuKongIM/WuKongIM/commits/" + head + "/check-runs":
			writeJSON(writer, map[string]any{
				"total_count": 0,
				"check_runs":  []any{},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	client, err := github.NewClient(github.ClientConfig{
		BaseURL: server.URL, GraphQLURL: server.URL + "/graphql",
		Repository: "WuKongIM/WuKongIM", Token: "token",
		MaxPages: 10, MaxBodyBytes: 2 << 20,
	}, server.Client())
	require.NoError(t, err)
	snapshot, err := client.ReadPullRequest(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(
		t,
		"pull-request file pagination is incomplete",
		snapshot.Facts.ContextFailureReason,
	)
}

func writeJSON(writer http.ResponseWriter, value any) {
	_ = json.NewEncoder(writer).Encode(value)
}

func writeBlob(writer http.ResponseWriter, content string) {
	writeJSON(writer, map[string]any{
		"encoding": "base64",
		"content":  base64.StdEncoding.EncodeToString([]byte(content)),
		"size":     len(content),
	})
}
