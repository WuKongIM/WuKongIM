package reviewagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestProjectionClientPublishesExactReviewedGeneration(t *testing.T) {
	t.Parallel()

	headSHA := strings.Repeat("a", 40)
	type checkPayload struct {
		Name       string `json:"name"`
		HeadSHA    string `json:"head_sha"`
		ExternalID string `json:"external_id"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		Output     struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"output"`
	}
	var mu sync.Mutex
	checkCreates := 0

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			var input struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, "review in progress", input.Body)
			writer.WriteHeader(http.StatusCreated)
			writeJSON(writer, map[string]any{"id": 101})
		case request.Method == http.MethodPatch &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/comments/101":
			var input struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, "review complete", input.Body)
			writeJSON(writer, map[string]any{"id": 101})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/pulls/42/reviews":
			var input struct {
				CommitID string `json:"commit_id"`
				Event    string `json:"event"`
				Body     string `json:"body"`
				Comments []struct {
					Path string `json:"path"`
					Line int    `json:"line"`
					Side string `json:"side"`
					Body string `json:"body"`
				} `json:"comments"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, headSHA, input.CommitID)
			require.Equal(t, "REQUEST_CHANGES", input.Event)
			require.Equal(t, "One current-head finding remains.", input.Body)
			require.Equal(t, []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Side string `json:"side"`
				Body string `json:"body"`
			}{{
				Path: "pkg/db/message.go", Line: 27, Side: "RIGHT",
				Body: "Keep the ownership fence atomic.",
			}}, input.Comments)
			writeJSON(writer, map[string]any{"id": 102})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/check-runs":
			checkCreates++
			var input checkPayload
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, "Review Agent Verdict", input.Name)
			require.Equal(t, headSHA, input.HeadSHA)
			if checkCreates == 1 {
				require.Equal(t, "review:42:generation:7", input.ExternalID)
				require.Equal(t, "completed", input.Status)
				require.Equal(t, "failure", input.Conclusion)
				require.Equal(t, "Changes requested", input.Output.Title)
				require.Equal(t, "One blocking finding.", input.Output.Summary)
				writer.WriteHeader(http.StatusCreated)
				writeJSON(writer, map[string]any{"id": 103})
				return
			}
			require.Equal(t, "review:42:lifecycle:7", input.ExternalID)
			require.Equal(t, "in_progress", input.Status)
			require.Empty(t, input.Conclusion)
			require.Equal(t, "Reviewing", input.Output.Title)
			require.Equal(t, "Fresh inputs are being verified.", input.Output.Summary)
			writer.WriteHeader(http.StatusCreated)
			writeJSON(writer, map[string]any{"id": 104})
		case request.Method == http.MethodPatch &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/check-runs/103":
			var input checkPayload
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, "Review Agent Verdict", input.Name)
			require.Empty(t, input.HeadSHA)
			require.Empty(t, input.ExternalID)
			require.Equal(t, "completed", input.Status)
			require.Equal(t, "success", input.Conclusion)
			require.Equal(t, "Approved", input.Output.Title)
			require.Equal(t, "All findings resolved.", input.Output.Summary)
			writeJSON(writer, map[string]any{"id": 103})
		case request.Method == http.MethodPatch &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/check-runs/104":
			var input checkPayload
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, "Review Agent Verdict", input.Name)
			require.Empty(t, input.HeadSHA)
			require.Empty(t, input.ExternalID)
			require.Equal(t, "completed", input.Status)
			require.Equal(t, "action_required", input.Conclusion)
			require.Equal(t, "Human action required", input.Output.Title)
			require.Equal(t, "The exact generation could not be verified.", input.Output.Summary)
			writeJSON(writer, map[string]any{"id": 104})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newReviewMemoryClient(t, 10, 2<<20, handler)

	commentID, err := client.CreateIssueComment(context.Background(), 42, "review in progress")
	require.NoError(t, err)
	require.Equal(t, int64(101), commentID)
	require.NoError(t, client.UpdateIssueComment(context.Background(), commentID, "review complete"))

	reviewID, err := client.CreateReview(
		context.Background(), 42, headSHA,
		usecase.FormalReviewRequestChanges,
		"One current-head finding remains.",
		[]github.InlineReviewComment{{
			Path: "pkg/db/message.go", Line: 27,
			Body: "Keep the ownership fence atomic.",
		}},
	)
	require.NoError(t, err)
	require.Equal(t, int64(102), reviewID)

	checkID, err := client.CreateCheckRun(
		context.Background(), headSHA, "review:42:generation:7",
		usecase.CheckFailure, "Changes requested", "One blocking finding.",
	)
	require.NoError(t, err)
	require.Equal(t, int64(103), checkID)
	require.NoError(t, client.UpdateCheckRun(
		context.Background(), checkID, usecase.CheckSuccess,
		"Approved", "All findings resolved.",
	))

	lifecycleID, err := client.CreateLifecycleCheckRun(
		context.Background(), headSHA, "review:42:lifecycle:7",
		"in_progress", nil, "Reviewing", "Fresh inputs are being verified.",
	)
	require.NoError(t, err)
	require.Equal(t, int64(104), lifecycleID)
	conclusion := usecase.CheckActionRequired
	require.NoError(t, client.UpdateLifecycleCheckRun(
		context.Background(), lifecycleID, "completed", &conclusion,
		"Human action required", "The exact generation could not be verified.",
	))
}

func TestMergeProjectionDelegatesStaleHeadCASAndRejectsInconsistentSuccess(t *testing.T) {
	t.Parallel()

	staleHead := strings.Repeat("b", 40)
	currentHead := strings.Repeat("c", 40)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPut, request.Method)
		require.Equal(t, "/repos/WuKongIM/WuKongIM/pulls/42/merge", request.URL.Path)
		var input struct {
			SHA         string `json:"sha"`
			MergeMethod string `json:"merge_method"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
		require.Equal(t, "merge", input.MergeMethod)
		if input.SHA == staleHead {
			writer.WriteHeader(http.StatusConflict)
			return
		}
		require.Equal(t, currentHead, input.SHA)
		writer.Header().Set("Content-Type", "application/json")
		writeJSON(writer, map[string]any{
			"sha": strings.Repeat("d", 40), "merged": false,
			"message": "merge rejected",
		})
	})
	client := newReviewMemoryClient(t, 10, 2<<20, handler)

	err := client.MergePullRequest(context.Background(), 42, staleHead)
	require.ErrorContains(t, err, "status 409")
	err = client.MergePullRequest(context.Background(), 42, currentHead)
	require.ErrorContains(t, err, "merge response is invalid")
}

func TestActorPermissionFailsClosedOnUnknownOrMismatchedIdentity(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		actor := strings.TrimPrefix(
			request.URL.Path,
			"/repos/WuKongIM/WuKongIM/collaborators/",
		)
		actor = strings.TrimSuffix(actor, "/permission")
		switch actor {
		case "maintainer":
			writeJSON(writer, map[string]any{
				"permission": "maintain", "user": map[string]any{"login": actor},
			})
		case "mismatch":
			writeJSON(writer, map[string]any{
				"permission": "write", "user": map[string]any{"login": "someone-else"},
			})
		case "mystery":
			writeJSON(writer, map[string]any{
				"permission": "owner", "user": map[string]any{"login": actor},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newReviewMemoryClient(t, 10, 2<<20, handler)

	permission, err := client.ActorPermission(context.Background(), "maintainer")
	require.NoError(t, err)
	require.Equal(t, github.PermissionMaintain, permission)
	_, err = client.ActorPermission(context.Background(), "mismatch")
	require.ErrorContains(t, err, "identity mismatch")
	_, err = client.ActorPermission(context.Background(), "mystery")
	require.ErrorContains(t, err, "permission is unknown")
}

func TestBaseContextDocumentsReadOnlyApplicableBlobsFromExactTree(t *testing.T) {
	t.Parallel()

	baseSHA := strings.Repeat("a", 40)
	rootAgentsSHA := strings.Repeat("1", 40)
	pkgAgentsSHA := strings.Repeat("2", 40)
	pkgFlowSHA := strings.Repeat("3", 40)
	internalAgentsSHA := strings.Repeat("4", 40)
	readBlobs := make(map[string]int)
	var mu sync.Mutex

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/trees/" + baseSHA:
			require.Equal(t, "1", request.URL.Query().Get("recursive"))
			writeJSON(writer, map[string]any{
				"truncated": false,
				"tree": []map[string]any{
					{"path": "AGENTS.md", "mode": "100644", "type": "blob", "sha": rootAgentsSHA},
					{"path": "pkg/AGENTS.md", "mode": "100644", "type": "blob", "sha": pkgAgentsSHA},
					{"path": "pkg/FLOW.md", "mode": "100644", "type": "blob", "sha": pkgFlowSHA},
					{"path": "internal/AGENTS.md", "mode": "100644", "type": "blob", "sha": internalAgentsSHA},
					{"path": "README.md", "mode": "100644", "type": "blob", "sha": strings.Repeat("5", 40)},
				},
			})
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + rootAgentsSHA:
			readBlobs[rootAgentsSHA]++
			writeBlob(writer, "root rules")
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + pkgAgentsSHA:
			readBlobs[pkgAgentsSHA]++
			writeBlob(writer, "pkg rules")
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + pkgFlowSHA:
			readBlobs[pkgFlowSHA]++
			writeBlob(writer, "---\nscope: subtree\nsummary: pkg navigation\n---\n")
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + internalAgentsSHA:
			readBlobs[internalAgentsSHA]++
			writeBlob(writer, "unrelated internal rules")
		default:
			http.NotFound(writer, request)
		}
	})
	client := newReviewMemoryClient(t, 10, 2<<20, handler)

	documents, err := client.ReadBaseContextDocuments(
		context.Background(), baseSHA, []string{"pkg/db/message/storage.go"},
	)
	require.NoError(t, err)
	require.Len(t, documents, 3)
	require.Equal(t, []string{"AGENTS.md", "pkg/AGENTS.md", "pkg/FLOW.md"}, []string{
		documents[0].Path, documents[1].Path, documents[2].Path,
	})
	require.Equal(t, rootAgentsSHA, documents[0].BlobSHA)
	require.Equal(t, "pkg rules", documents[1].Content)
	require.Equal(t, map[string]int{
		rootAgentsSHA: 1, pkgAgentsSHA: 1, pkgFlowSHA: 1,
	}, readBlobs, "unrelated context blobs must not be fetched")
}
