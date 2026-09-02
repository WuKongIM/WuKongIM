package reviewagentgithub_test

import (
	"context"
	"crypto/sha1" // #nosec G505 -- the test computes the Git protocol blob identity.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestClientPublishesAndRereadsExactSignedReviewStateCommit(t *testing.T) {
	t.Parallel()

	parentSHA := strings.Repeat("a", 40)
	commitSHA := strings.Repeat("b", 40)
	treeSHA := strings.Repeat("c", 40)
	branch := "review-state/scheduler"
	statePath := ".review-agent-state/scheduler.json"
	message := "review(scheduler): sequence 2"
	content := []byte("{\"schema_version\":1,\"sequence\":2}\n")
	blobSHA := reviewTestGitBlobSHA(content)
	var refReads atomic.Int32
	var refCreates atomic.Int32
	var mutationCalls atomic.Int32

	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/"+branch:
			if refReads.Add(1) == 1 {
				writer.WriteHeader(http.StatusNotFound)
				writeJSON(writer, map[string]any{"message": "Not Found"})
				return
			}
			writeJSON(writer, map[string]any{
				"ref":    "refs/heads/" + branch,
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/refs":
			refCreates.Add(1)
			var payload struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			require.Equal(t, "refs/heads/"+branch, payload.Ref)
			require.Equal(t, parentSHA, payload.SHA)
			writer.WriteHeader(http.StatusCreated)
			writeJSON(writer, map[string]any{
				"ref": payload.Ref,
				"object": map[string]any{
					"type": "commit", "sha": payload.SHA,
				},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/graphql":
			encoded, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var envelope struct {
				Query     string `json:"query"`
				Variables struct {
					Input struct {
						Branch struct {
							Repository string `json:"repositoryNameWithOwner"`
							Name       string `json:"branchName"`
						} `json:"branch"`
						ExpectedHead string `json:"expectedHeadOid"`
						Message      struct {
							Headline string `json:"headline"`
						} `json:"message"`
						FileChanges struct {
							Additions []struct {
								Path     string `json:"path"`
								Contents string `json:"contents"`
							} `json:"additions"`
						} `json:"fileChanges"`
					} `json:"input"`
				} `json:"variables"`
			}
			require.NoError(t, json.Unmarshal(encoded, &envelope))
			if strings.Contains(envelope.Query, "createCommitOnBranch") {
				mutationCalls.Add(1)
				require.Equal(t, "WuKongIM/WuKongIM", envelope.Variables.Input.Branch.Repository)
				require.Equal(t, branch, envelope.Variables.Input.Branch.Name)
				require.Equal(t, parentSHA, envelope.Variables.Input.ExpectedHead)
				require.Equal(t, message, envelope.Variables.Input.Message.Headline)
				require.Len(t, envelope.Variables.Input.FileChanges.Additions, 1)
				require.Equal(t, statePath, envelope.Variables.Input.FileChanges.Additions[0].Path)
				require.Equal(t, base64.StdEncoding.EncodeToString(content), envelope.Variables.Input.FileChanges.Additions[0].Contents)
				writeJSON(writer, map[string]any{
					"data": map[string]any{
						"createCommitOnBranch": map[string]any{
							"commit": map[string]any{"oid": commitSHA},
						},
					},
					"errors": []any{},
				})
				return
			}
			writeJSON(writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"nameWithOwner": "WuKongIM/WuKongIM",
						"object": map[string]any{
							"oid": commitSHA,
							"signature": map[string]any{
								"isValid": true, "state": "VALID",
								"wasSignedByGitHub": true,
							},
						},
					},
				},
				"errors": []any{},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+commitSHA:
			writeJSON(writer, map[string]any{
				"sha": commitSHA, "message": message,
				"tree":    map[string]any{"sha": treeSHA},
				"parents": []map[string]any{{"sha": parentSHA}},
				"verification": map[string]any{
					"verified": true, "reason": "valid",
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/compare/"+parentSHA+"..."+commitSHA:
			writeJSON(writer, map[string]any{
				"status": "ahead", "ahead_by": 1, "behind_by": 0,
				"total_commits": 1,
				"files": []map[string]any{{
					"filename": statePath, "status": "modified", "sha": blobSHA,
				}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/trees/"+treeSHA:
			require.Equal(t, "1", request.URL.Query().Get("recursive"))
			writeJSON(writer, map[string]any{
				"truncated": false,
				"tree": []map[string]any{{
					"path": statePath, "mode": "100644",
					"type": "blob", "sha": blobSHA,
				}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/blobs/"+blobSHA:
			writeJSON(writer, map[string]any{
				"encoding": "base64", "size": len(content),
				"content": base64.StdEncoding.EncodeToString(content),
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/commits/"+commitSHA:
			writeJSON(writer, map[string]any{
				"sha": commitSHA,
				"author": map[string]any{
					"login": "wukongim-review-state-writer[bot]", "type": "Bot",
				},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newReviewMemoryClient(t, 4, 1<<20, handler)

	result, err := client.PublishStateCommit(context.Background(), github.StateCommitRequest{
		Branch: branch, Path: statePath,
		ExpectedParentSHA: parentSHA, ExistingBranch: false,
		Message: message, Content: content,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), refCreates.Load())
	require.Equal(t, int32(1), mutationCalls.Load())
	require.Equal(t, commitSHA, result.CommitSHA)
	require.Equal(t, parentSHA, result.ParentSHA)
	require.Equal(t, statePath, result.Path)
	require.Equal(t, "wukongim-review-state-writer[bot]", result.AuthorLogin)
	require.Equal(t, "Bot", result.AuthorType)
	require.True(t, result.Verified)
	require.True(t, result.SignedByGitHub)
	sum := sha256.Sum256(content)
	require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), result.ContentDigest)
}

func TestClientStatePublicationRejectsStaleHeadBeforeMutation(t *testing.T) {
	t.Parallel()

	var writes atomic.Int32
	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodGet {
			writes.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		writeJSON(writer, map[string]any{
			"ref": "refs/heads/review-state/pr-42",
			"object": map[string]any{
				"type": "commit", "sha": strings.Repeat("c", 40),
			},
		})
	})
	client := newReviewMemoryClient(t, 4, 1<<20, handler)

	_, err := client.PublishStateCommit(context.Background(), github.StateCommitRequest{
		Branch: "review-state/pr-42", Path: ".review-agent-state/pr-42.json",
		ExpectedParentSHA: strings.Repeat("a", 40), ExistingBranch: true,
		Message: "review(state): pr 42 sequence 2", Content: []byte("state"),
	})
	require.EqualError(t, err, "Review state ref head changed")
	require.Zero(t, writes.Load(), "a stale expected head must prevent every publication write")
}

func TestSchedulerStorePublishesCanonicalInitialCheckpoint(t *testing.T) {
	t.Parallel()

	limits := usecase.SchedulerLimits{
		MaxActive: 3, MaxPerPullRequest: 1, MaxFirstTimeExternal: 1,
	}
	state := schedulerStateFixture(1)
	state.PreviousStateDigest = ""
	content, err := usecase.CanonicalSchedulerState(state, limits)
	require.NoError(t, err)
	digest := sha256.Sum256(content)
	parentSHA := state.SourceSHA
	commitSHA := strings.Repeat("b", 40)
	port := &stateCommitStub{publishResult: github.StateCommitResult{
		CommitSHA: commitSHA, ParentSHA: parentSHA,
		Path:          ".review-agent-state/scheduler.json",
		ContentDigest: "sha256:" + hex.EncodeToString(digest[:]),
		AuthorLogin:   "wukongim-review-state-writer[bot]",
		AuthorType:    "Bot", Verified: true, SignedByGitHub: true,
	}}
	store, err := github.NewSchedulerStore(
		"wukongim-review-state-writer[bot]", port, limits,
	)
	require.NoError(t, err)

	head, err := store.Advance(context.Background(), state, parentSHA, false)
	require.NoError(t, err)
	require.Equal(t, commitSHA, head)
	require.Equal(t, "review-state/scheduler", port.request.Branch)
	require.Equal(t, ".review-agent-state/scheduler.json", port.request.Path)
	require.Equal(t, parentSHA, port.request.ExpectedParentSHA)
	require.False(t, port.request.ExistingBranch)
	require.Equal(t, "review(scheduler): sequence 1", port.request.Message)
	require.Equal(t, content, port.request.Content)
}

func TestReviewStateStorePublishesCanonicalInitialCheckpoint(t *testing.T) {
	t.Parallel()

	state := reviewStateFixture(1)
	state.PreviousStateDigest = ""
	content, err := contract.CanonicalReviewState(state)
	require.NoError(t, err)
	digest := sha256.Sum256(content)
	parentSHA := state.Generation.StateParentSHA
	commitSHA := strings.Repeat("b", 40)
	port := &stateCommitStub{publishResult: github.StateCommitResult{
		CommitSHA: commitSHA, ParentSHA: parentSHA,
		Path:          ".review-agent-state/pr-42.json",
		ContentDigest: "sha256:" + hex.EncodeToString(digest[:]),
		AuthorLogin:   "wukongim-review-state-writer[bot]",
		AuthorType:    "Bot", Verified: true, SignedByGitHub: true,
	}}
	store, err := github.NewReviewStateStore(
		"WuKongIM/WuKongIM", "wukongim-review-state-writer[bot]", port,
	)
	require.NoError(t, err)

	head, err := store.Advance(context.Background(), state, parentSHA, false)
	require.NoError(t, err)
	require.Equal(t, commitSHA, head)
	require.Equal(t, "review-state/pr-42", port.request.Branch)
	require.Equal(t, ".review-agent-state/pr-42.json", port.request.Path)
	require.Equal(t, parentSHA, port.request.ExpectedParentSHA)
	require.False(t, port.request.ExistingBranch)
	require.Equal(t, "review(state): pr 42 sequence 1", port.request.Message)
	require.Equal(t, content, port.request.Content)
}

func reviewTestGitBlobSHA(content []byte) string {
	hasher := sha1.New() // #nosec G401 -- Git blob identity is SHA-1 by protocol.
	_, _ = hasher.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}
