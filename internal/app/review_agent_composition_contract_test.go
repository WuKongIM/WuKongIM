package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestReviewAgentCompositionBuildsFirstSignedGenerationAndFrozenContextFromFreshGitHubFacts(
	t *testing.T,
) {
	t.Parallel()

	headSHA := strings.Repeat("a", 40)
	baseSHA := strings.Repeat("b", 40)
	mergeSHA := strings.Repeat("c", 40)
	headBlobSHA := strings.Repeat("d", 40)
	baseBlobSHA := strings.Repeat("e", 40)
	controlSHA := strings.Repeat("f", 40)
	agentsBlobSHA := strings.Repeat("1", 40)
	headContent := "package queue\n\nconst owner = \"new\"\n"
	baseContent := "package queue\n\nconst owner = \"old\"\n"
	agentsContent := "# Repository test instructions\n"
	now := time.Date(2026, 9, 2, 4, 5, 6, 0, time.UTC)
	var pullReads atomic.Int32
	var stateRefReads atomic.Int32
	var schedulerRefReads atomic.Int32
	var permissionReads atomic.Int32
	var agentsBlobReads atomic.Int32

	httpClient := &http.Client{Transport: reviewCompositionRoundTrip(func(
		request *http.Request,
	) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer read-token" {
			t.Errorf("Authorization = %q, want bounded read credential", request.Header.Get("Authorization"))
			return reviewCompositionResponse(
				request,
				http.StatusUnauthorized,
				map[string]any{"message": "Bad credentials"},
			), nil
		}
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM":
			require.Equal(t, http.MethodGet, request.Method)
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
				"full_name": "WuKongIM/WuKongIM", "default_branch": "main",
			}), nil
		case "/repos/WuKongIM/WuKongIM/pulls/42":
			pullReads.Add(1)
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
				"number": 42, "state": "open", "draft": false,
				"title": "Preserve queue ownership", "body": "",
				"changed_files": 1, "additions": 1, "deletions": 1,
				"mergeable": true, "mergeable_state": "clean",
				"merge_commit_sha":   mergeSHA,
				"user":               map[string]any{"login": "maintainer", "type": "User"},
				"author_association": "MEMBER",
				"base":               map[string]any{"ref": "main", "sha": baseSHA},
				"head": map[string]any{
					"ref": "queue-fix", "sha": headSHA,
					"repo": map[string]any{"full_name": "WuKongIM/WuKongIM"},
				},
			}), nil
		case "/repos/WuKongIM/WuKongIM/pulls/42/files":
			require.Equal(t, "1", request.URL.Query().Get("page"))
			require.Equal(t, "100", request.URL.Query().Get("per_page"))
			return reviewCompositionResponse(request, http.StatusOK, []map[string]any{{
				"filename": "internal/runtime/queue.go", "status": "modified",
				"sha": headBlobSHA, "additions": 1, "deletions": 1,
				"patch": "@@ -7 +7 @@\n-old\n+new\n",
			}}), nil
		case "/repos/WuKongIM/WuKongIM/git/trees/" + headSHA:
			require.Equal(t, "1", request.URL.Query().Get("recursive"))
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
				"truncated": false,
				"tree": []map[string]any{
					{"path": "AGENTS.md", "mode": "100644", "type": "blob", "sha": agentsBlobSHA},
					{"path": "internal/runtime/queue.go", "mode": "100644", "type": "blob", "sha": headBlobSHA},
				},
			}), nil
		case "/repos/WuKongIM/WuKongIM/git/trees/" + baseSHA:
			require.Equal(t, "1", request.URL.Query().Get("recursive"))
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
				"truncated": false,
				"tree": []map[string]any{
					{"path": "AGENTS.md", "mode": "100644", "type": "blob", "sha": agentsBlobSHA},
					{"path": "internal/runtime/queue.go", "mode": "100644", "type": "blob", "sha": baseBlobSHA},
				},
			}), nil
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + headBlobSHA:
			return reviewCompositionBlobResponse(request, headContent), nil
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + baseBlobSHA:
			return reviewCompositionBlobResponse(request, baseContent), nil
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + agentsBlobSHA:
			agentsBlobReads.Add(1)
			return reviewCompositionBlobResponse(request, agentsContent), nil
		case "/repos/WuKongIM/WuKongIM/pulls/42/reviews",
			"/repos/WuKongIM/WuKongIM/pulls/42/comments":
			return reviewCompositionResponse(request, http.StatusOK, []any{}), nil
		case "/repos/WuKongIM/WuKongIM/issues/42/comments":
			return reviewCompositionResponse(request, http.StatusOK, []map[string]any{{
				"id": 77, "body": "@review-agent review",
				"user":       map[string]any{"login": "maintainer", "type": "User"},
				"created_at": now.Format(time.RFC3339Nano),
				"updated_at": now.Format(time.RFC3339Nano),
			}}), nil
		case "/graphql":
			require.Equal(t, http.MethodPost, request.Method)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var envelope struct {
				Query     string `json:"query"`
				Variables struct {
					Number int64 `json:"number"`
				} `json:"variables"`
			}
			require.NoError(t, json.Unmarshal(body, &envelope))
			require.Contains(t, envelope.Query, "reviewThreads")
			require.Equal(t, int64(42), envelope.Variables.Number)
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
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
			}), nil
		case "/repos/WuKongIM/WuKongIM/commits/" + headSHA + "/check-runs":
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
				"total_count": 0, "check_runs": []any{},
			}), nil
		case "/repos/WuKongIM/WuKongIM/git/ref/heads/review-state/pr-42":
			stateRefReads.Add(1)
			return reviewCompositionResponse(
				request,
				http.StatusNotFound,
				map[string]any{"message": "Not Found"},
			), nil
		case "/repos/WuKongIM/WuKongIM/git/ref/heads/review-state/scheduler":
			schedulerRefReads.Add(1)
			return reviewCompositionResponse(
				request,
				http.StatusNotFound,
				map[string]any{"message": "Not Found"},
			), nil
		case "/repos/WuKongIM/WuKongIM/collaborators/maintainer/permission":
			permissionReads.Add(1)
			return reviewCompositionResponse(request, http.StatusOK, map[string]any{
				"permission": "admin", "user": map[string]any{"login": "maintainer"},
			}), nil
		default:
			t.Errorf("unexpected GitHub request: %s %s", request.Method, request.URL.String())
			return reviewCompositionResponse(
				request,
				http.StatusNotFound,
				map[string]any{"message": "Not Found"},
			), nil
		}
	})}
	operations := NewReviewAgentOperations(ReviewAgentConfig{
		HTTPClient: httpClient, APIBaseURL: "https://api.github.test",
		GraphQLURL: "https://api.github.test/graphql",
		Repository: "WuKongIM/WuKongIM", GitHubReadToken: "read-token",
		ControlSHA: controlSHA,
		PolicyPath: filepath.Join("..", "..", ".github", "review-agent", "policy.json"),
		PromptPath: filepath.Join(
			"..", "..", ".github", "review-agent", "prompts", "review.md",
		),
		ResultSchemaPath: filepath.Join(
			"..", "..", ".github", "review-agent", "review-result.schema.json",
		),
		Now: func() time.Time { return now },
	})

	response, err := operations.ReconcileGitHub(
		context.Background(),
		cli.ReconcileGitHubRequest{
			PullRequest: 42, SignalKind: usecase.SignalCommand,
			RunID: 9001, CommentID: 77,
		},
	)
	require.NoError(t, err)
	require.Equal(t, usecase.ActionAcquireAndDispatch, response.Plan.Action)
	require.False(t, response.StateFound)
	require.Empty(t, response.StateHeadSHA)
	require.False(t, response.SchedulerFound)
	require.Empty(t, response.SchedulerHeadSHA)
	require.True(t, response.SchedulerChanged)
	require.True(t, response.StateChanged)
	require.NotNil(t, response.NextState)
	require.Equal(t, contract.PhaseReviewing, response.NextState.Phase)
	require.Equal(t, controlSHA, response.NextState.Generation.StateParentSHA)
	require.Equal(t, headSHA, response.NextState.Generation.HeadSHA)
	require.Equal(t, baseSHA, response.NextState.Generation.BaseSHA)
	require.Equal(t, mergeSHA, response.NextState.Generation.TestMergeSHA)

	contextResponse, err := operations.BuildContext(
		context.Background(),
		cli.BuildContextRequest{
			PullRequest:  42,
			Generation:   response.NextState.Generation,
			ReviewReason: "administrator requested the initial review",
		},
	)
	require.NoError(t, err)
	require.Equal(t, response.NextState.Generation, contextResponse.Context.Generation)
	require.Equal(t, "Preserve queue ownership", contextResponse.Context.Title)
	require.Equal(t, headContent, contextResponse.Context.ChangedFiles[0].Content)
	require.Contains(t, contextResponse.Context.ChangedFiles[0].Patch, "-const owner = \"old\"")
	require.Contains(t, contextResponse.Context.ChangedFiles[0].Patch, "+const owner = \"new\"")
	require.Equal(t, []string{
		"go-format", "go-mod-tidy", "go-unit", "go-vet",
	}, contextResponse.Context.MandatoryChecks)
	require.Len(t, contextResponse.Context.ContextDocuments, 1)
	require.Equal(t, []contract.ContextDocumentBlob{{
		Path:       "AGENTS.md",
		Scope:      ".",
		BlobSHA:    agentsBlobSHA,
		Content:    agentsContent,
		BlobDigest: "sha256:aba6cdb94069ad03353a87ac2a7b05bfdcdfec55825b32667185a79435bdcb75",
	}}, contextResponse.Context.ContextDocuments)
	digest, err := contract.ReviewContextDigest(contextResponse.Context)
	require.NoError(t, err)
	require.Equal(t, digest, contextResponse.Digest)
	require.Equal(t, int32(1), agentsBlobReads.Load())

	staleGeneration := response.NextState.Generation
	staleGeneration.HeadSHA = strings.Repeat("9", 40)
	staleContext, err := operations.BuildContext(
		context.Background(),
		cli.BuildContextRequest{
			PullRequest:  42,
			Generation:   staleGeneration,
			ReviewReason: "stale worker must not construct model input",
		},
	)
	require.ErrorContains(t, err, "Review context generation is stale")
	require.Equal(t, cli.BuildContextResponse{}, staleContext)
	require.Equal(t, int32(1), agentsBlobReads.Load(),
		"stale generation must not freeze base-tree control documents")
	require.Equal(t, int32(3), pullReads.Load())
	require.Equal(t, int32(1), stateRefReads.Load())
	require.Equal(t, int32(1), schedulerRefReads.Load())
	require.Equal(t, int32(1), permissionReads.Load())
}

type reviewCompositionRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip reviewCompositionRoundTrip) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func reviewCompositionResponse(
	request *http.Request,
	statusCode int,
	value any,
) *http.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:    io.NopCloser(bytes.NewReader(body)),
		Request: request,
	}
}

func reviewCompositionBlobResponse(
	request *http.Request,
	content string,
) *http.Response {
	return reviewCompositionResponse(request, http.StatusOK, map[string]any{
		"encoding": "base64",
		"content":  base64.StdEncoding.EncodeToString([]byte(content)),
		"size":     len(content),
	})
}
