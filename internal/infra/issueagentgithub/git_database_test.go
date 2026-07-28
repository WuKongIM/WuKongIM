package issueagentgithub_test

import (
	"context"
	"crypto/sha1" // #nosec G505 -- test verifies Git blob identity.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestGitDatabasePublishesGitHubSignedExpectedHeadCommit(t *testing.T) {
	t.Parallel()

	parent := fortyHex("a")
	baseTree := fortyHex("b")
	tree := fortyHex("d")
	commit := fortyHex("e")
	content := []byte("package example\n")
	blob := testGitBlobSHA(content)
	var mutationInput map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		require.Equal(t, "Bearer token", request.Header.Get("Authorization"))
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+parent:
			writeJSON(t, writer, map[string]any{
				"sha": parent, "tree": map[string]any{"sha": baseTree},
				"parents":      []map[string]any{{"sha": fortyHex("0")}},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/refs":
			writer.WriteHeader(http.StatusCreated)
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/agent/issue-42",
				"object": map[string]any{"type": "commit", "sha": parent},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/graphql":
			require.NoError(t, json.NewDecoder(request.Body).Decode(&mutationInput))
			writeJSON(t, writer, map[string]any{
				"data": map[string]any{
					"createCommitOnBranch": map[string]any{
						"commit": map[string]any{"oid": commit},
					},
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/agent/issue-42":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/agent/issue-42",
				"object": map[string]any{"type": "commit", "sha": commit},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+commit:
			writeJSON(t, writer, map[string]any{
				"sha": commit, "tree": map[string]any{"sha": tree},
				"parents":      []map[string]any{{"sha": parent}},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/compare/"+parent+"..."+commit:
			writeJSON(t, writer, map[string]any{
				"status": "ahead", "ahead_by": 1, "behind_by": 0,
				"total_commits": 1,
				"files": []map[string]any{{
					"filename": "pkg/example/fix.go",
					"status":   "added", "sha": blob,
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	published, err := client.PublishCommit(
		context.Background(),
		issueagentgithub.CommitPlan{
			Branch: "agent/issue-42", ExpectedParentSHA: parent,
			BaseTreeSHA: baseTree, Message: "fix(agent): issue #42",
			ExistingBranch: false,
			ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
				Path: "pkg/example/fix.go", Operation: issueagent.FileOperationUpsert,
				Mode:          issueagent.FileModeRegular,
				ContentBase64: issueagent.EncodeFileContent(content),
			}}},
		},
	)
	require.NoError(t, err)
	require.Equal(t, commit, published.CommitSHA)
	input := mutationInput["variables"].(map[string]any)["input"].(map[string]any)
	require.Equal(t, parent, input["expectedHeadOid"])
	require.Equal(t, "agent/issue-42", input["branch"].(map[string]any)["branchName"])
}

func TestGitDatabaseRejectsGraphQLErrorAndUnverifiedCommit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		graphError bool
	}{
		{name: "GraphQL error", graphError: true},
		{name: "unverified commit"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parent := fortyHex("a")
			commit := fortyHex("e")
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet &&
					request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+parent:
					writeJSON(t, writer, map[string]any{
						"sha": parent, "tree": map[string]any{"sha": fortyHex("b")},
						"parents":      []map[string]any{{"sha": fortyHex("0")}},
						"verification": map[string]any{"verified": true, "reason": "valid"},
					})
				case request.Method == http.MethodPost && request.URL.Path == "/graphql":
					if test.graphError {
						writeJSON(t, writer, map[string]any{
							"errors": []map[string]any{{"message": "stale head"}},
						})
					} else {
						writeJSON(t, writer, map[string]any{
							"data": map[string]any{
								"createCommitOnBranch": map[string]any{
									"commit": map[string]any{"oid": commit},
								},
							},
						})
					}
				case request.Method == http.MethodGet &&
					request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/agent/issue-42":
					writeJSON(t, writer, map[string]any{
						"ref":    "refs/heads/agent/issue-42",
						"object": map[string]any{"type": "commit", "sha": commit},
					})
				case request.Method == http.MethodGet &&
					request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+commit:
					writeJSON(t, writer, map[string]any{
						"sha": commit, "tree": map[string]any{"sha": fortyHex("d")},
						"parents": []map[string]any{{"sha": parent}},
						"verification": map[string]any{
							"verified": false, "reason": "unsigned",
						},
					})
				default:
					http.NotFound(writer, request)
				}
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.PublishCommit(
				context.Background(),
				issueagentgithub.CommitPlan{
					Branch: "agent/issue-42", ExpectedParentSHA: parent,
					BaseTreeSHA: fortyHex("b"), Message: "fix",
					ExistingBranch: true,
					ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
						Path: "fix.go", Operation: issueagent.FileOperationUpsert,
						Mode:          issueagent.FileModeRegular,
						ContentBase64: issueagent.EncodeFileContent([]byte("fix")),
					}}},
				},
			)
			require.Error(t, err)
		})
	}
}

func testGitBlobSHA(content []byte) string {
	hasher := sha1.New() // #nosec G401 -- test verifies Git blob identity.
	_, _ = hasher.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}
