package issueagentgithub_test

import (
	"context"
	"crypto/sha1" // #nosec G505 -- test verifies Git blob identity.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
				"sha": commit, "message": "fix(agent): issue #42",
				"tree":         map[string]any{"sha": tree},
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
			ExpectedResultTreeSHA: tree,
			ExistingBranch:        false,
			ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
				Path: "pkg/example/fix.go", Operation: issueagent.FileOperationUpsert,
				Mode:          issueagent.FileModeRegular,
				ContentBase64: issueagent.EncodeFileContent(content),
			}}},
		},
	)
	require.NoError(t, err)
	require.Equal(t, commit, published.CommitSHA)
	require.True(t, issueagentgithub.ExactRebasedIntegration(
		issueagentgithub.CommitFacts{
			SHA: commit, TreeSHA: tree, Parents: []string{parent},
			Message:  "fix(agent): issue #42",
			Verified: true, VerificationReason: "valid",
		},
		issueagentgithub.CommitAttributionFacts{
			SHA: commit, AuthorLogin: "issue-agent[bot]", AuthorType: "Bot",
			SignatureValid: true, SignatureState: "VALID",
			WasSignedByGitHub: true,
		},
		parent, tree, "fix(agent): issue #42", "issue-agent[bot]",
	))
	require.False(t, issueagentgithub.ExactAppCommit(
		issueagentgithub.CommitFacts{
			SHA: commit, TreeSHA: tree, Parents: []string{parent},
			Message:  "externally selected message",
			Verified: true, VerificationReason: "valid",
		},
		issueagentgithub.CommitAttributionFacts{
			SHA: commit, AuthorLogin: "issue-agent[bot]", AuthorType: "Bot",
			SignatureValid: true, SignatureState: "VALID",
			WasSignedByGitHub: true,
		},
		parent, "fix(agent): issue #42", "issue-agent[bot]",
	))
	require.False(t, issueagentgithub.ExactRebasedIntegration(
		issueagentgithub.CommitFacts{
			SHA: commit, TreeSHA: tree, Parents: []string{parent},
			Message:  "fix(agent): issue #42",
			Verified: false, VerificationReason: "unsigned",
		},
		issueagentgithub.CommitAttributionFacts{
			SHA: commit, AuthorLogin: "issue-agent[bot]", AuthorType: "Bot",
			SignatureValid: true, SignatureState: "VALID",
			WasSignedByGitHub: true,
		},
		parent, tree, "fix(agent): issue #42", "issue-agent[bot]",
	))
	require.False(t, issueagentgithub.ExactRebasedIntegration(
		issueagentgithub.CommitFacts{
			SHA: commit, TreeSHA: tree, Parents: []string{parent},
			Message:  "fix(agent): issue #42",
			Verified: true, VerificationReason: "valid",
		},
		issueagentgithub.CommitAttributionFacts{
			SHA: commit, AuthorLogin: "other-writer", AuthorType: "User",
			SignatureValid: true, SignatureState: "VALID",
			WasSignedByGitHub: true,
		},
		parent, tree, "fix(agent): issue #42", "issue-agent[bot]",
	))
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
						"sha": commit, "message": "fix",
						"tree":    map[string]any{"sha": fortyHex("d")},
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

func TestMechanicalRebaseUsesAppSignedMainParentAndAtomicRefSwap(t *testing.T) {
	t.Parallel()

	oldHead := fortyHex("a")
	mainSHA := fortyHex("b")
	mainTree := fortyHex("c")
	resultTree := fortyHex("d")
	candidate := fortyHex("e")
	content := []byte("package exact\n")
	blob := testGitBlobSHA(content)
	plan := issueagentgithub.RebasePlan{
		Branch: "agent/issue-42", ExpectedOldHeadSHA: oldHead,
		CurrentMainSHA: mainSHA, ExpectedResultTreeSHA: resultTree,
		Message:             "chore(agent): rebase issue #42",
		ExpectedAuthorLogin: "issue-agent[bot]",
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path: "pkg/exact/fix.go", Operation: issueagent.FileOperationUpsert,
			Mode:          issueagent.FileModeRegular,
			ContentBase64: issueagent.EncodeFileContent(content),
		}}},
	}
	encodedPlan, err := json.Marshal(plan)
	require.NoError(t, err)
	planDigest := sha256.Sum256(encodedPlan)
	stageBranch := "agent/issue-42-rebase-" + hex.EncodeToString(planDigest[:])
	agentSHA := oldHead
	stageSHA := ""
	updateInputs := make([]map[string]any, 0, 2)

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/graphql":
			var body map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			query := body["query"].(string)
			switch {
			case strings.Contains(query, "object(oid:$oid)"):
				writeJSON(t, writer, map[string]any{"data": map[string]any{
					"repository": map[string]any{
						"nameWithOwner": "WuKongIM/WuKongIM",
						"object": map[string]any{
							"oid": candidate,
							"signature": map[string]any{
								"isValid": true, "state": "VALID",
								"wasSignedByGitHub": true,
							},
						},
					},
				}})
			case strings.HasPrefix(query, "query("):
				writeJSON(t, writer, map[string]any{"data": map[string]any{
					"repository": map[string]any{
						"id": "R_repo", "nameWithOwner": "WuKongIM/WuKongIM",
					},
				}})
			case strings.Contains(query, "updateRefs"):
				input := body["variables"].(map[string]any)["input"].(map[string]any)
				updateInputs = append(updateInputs, input)
				updates := input["refUpdates"].([]any)
				if len(updates) == 1 {
					stageSHA = mainSHA
				} else {
					require.Len(t, updates, 3)
					agentSHA = candidate
					stageSHA = ""
				}
				writeJSON(t, writer, map[string]any{"data": map[string]any{
					"updateRefs": map[string]any{},
				}})
			case strings.Contains(query, "createCommitOnBranch"):
				stageSHA = candidate
				writeJSON(t, writer, map[string]any{"data": map[string]any{
					"createCommitOnBranch": map[string]any{
						"commit": map[string]any{"oid": candidate},
					},
				}})
			default:
				t.Fatalf("unexpected GraphQL operation: %s", query)
			}
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/"+stageBranch:
			if stageSHA == "" {
				writer.WriteHeader(http.StatusNotFound)
				writeJSON(t, writer, map[string]any{})
				return
			}
			writeJSON(t, writer, map[string]any{
				"ref": "refs/heads/" + stageBranch,
				"object": map[string]any{
					"type": "commit", "sha": stageSHA,
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/agent/issue-42":
			writeJSON(t, writer, map[string]any{
				"ref": "refs/heads/agent/issue-42",
				"object": map[string]any{
					"type": "commit", "sha": agentSHA,
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+mainSHA:
			writeJSON(t, writer, map[string]any{
				"sha": mainSHA, "tree": map[string]any{"sha": mainTree},
				"parents":      []map[string]any{{"sha": fortyHex("0")}},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+candidate:
			writeJSON(t, writer, map[string]any{
				"sha": candidate, "message": "chore(agent): rebase issue #42",
				"tree":         map[string]any{"sha": resultTree},
				"parents":      []map[string]any{{"sha": mainSHA}},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/commits/"+candidate:
			writeJSON(t, writer, map[string]any{
				"sha": candidate,
				"author": map[string]any{
					"login": "issue-agent[bot]", "type": "Bot",
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/compare/"+mainSHA+"..."+candidate:
			writeJSON(t, writer, map[string]any{
				"status": "ahead", "ahead_by": 1, "behind_by": 0,
				"total_commits": 1,
				"files": []map[string]any{{
					"filename": "pkg/exact/fix.go",
					"status":   "added", "sha": blob,
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	published, err := client.PublishRebasedCommit(context.Background(), plan)
	require.NoError(t, err)
	require.Equal(t, candidate, published.CommitSHA)
	require.Len(t, updateInputs, 2)
	create := updateInputs[0]["refUpdates"].([]any)[0].(map[string]any)
	require.Equal(t, zeroOIDForTest, create["beforeOid"])
	require.Equal(t, mainSHA, create["afterOid"])
	require.Equal(t, false, create["force"])
	swap := updateInputs[1]["refUpdates"].([]any)
	require.Len(t, swap, 3)
	require.Equal(t, oldHead, swap[0].(map[string]any)["beforeOid"])
	require.Equal(t, candidate, swap[0].(map[string]any)["afterOid"])
	require.Equal(t, true, swap[0].(map[string]any)["force"])
	require.Equal(t, candidate, swap[1].(map[string]any)["beforeOid"])
	require.Equal(t, zeroOIDForTest, swap[1].(map[string]any)["afterOid"])
	require.Equal(t, "refs/heads/main", swap[2].(map[string]any)["name"])
	require.Equal(t, mainSHA, swap[2].(map[string]any)["beforeOid"])
	require.Equal(t, mainSHA, swap[2].(map[string]any)["afterOid"])
	require.Equal(t, false, swap[2].(map[string]any)["force"])
}

func TestGitDatabaseBuildsExactResultTreeFromMainAndChangeSet(t *testing.T) {
	t.Parallel()

	baseTree := fortyHex("a")
	resultTree := fortyHex("b")
	content := []byte("package exact\n")
	blob := testGitBlobSHA(content)
	var treeInput map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/trees":
			require.NoError(t, json.NewDecoder(request.Body).Decode(&treeInput))
			writeJSON(t, writer, map[string]any{
				"sha": resultTree,
				"tree": []map[string]any{{
					"path": "fix.go", "mode": "100644",
					"type": "blob", "sha": blob,
				}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/trees/"+resultTree:
			writeJSON(t, writer, map[string]any{
				"sha": resultTree, "truncated": false,
				"tree": []map[string]any{{
					"path": "fix.go", "mode": "100644",
					"type": "blob", "sha": blob,
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	tree, err := client.BuildResultTree(
		context.Background(),
		baseTree,
		issueagent.ChangeSet{Files: []issueagent.FileChange{
			{
				Path: "fix.go", Operation: issueagent.FileOperationUpsert,
				Mode:          issueagent.FileModeRegular,
				ContentBase64: issueagent.EncodeFileContent(content),
			},
			{
				Path: "obsolete.go", Operation: issueagent.FileOperationDelete,
			},
		}},
	)
	require.NoError(t, err)
	require.Equal(t, resultTree, tree)
	require.Equal(t, baseTree, treeInput["base_tree"])
	entries := treeInput["tree"].([]any)
	require.Len(t, entries, 2)
	require.Equal(t, blob, entries[0].(map[string]any)["sha"])
	require.Equal(t, "obsolete.go", entries[1].(map[string]any)["path"])
	require.Nil(t, entries[1].(map[string]any)["sha"])
	require.NotContains(t, entries[1].(map[string]any), "mode")
	require.NotContains(t, entries[1].(map[string]any), "type")
}

func TestCompareCandidateRejectsAmbiguousAggregateResponse(t *testing.T) {
	t.Parallel()

	base := fortyHex("1")
	head := fortyHex("2")
	for _, test := range []struct {
		name       string
		aheadBy    int
		status     string
		fileStatus string
		duplicate  bool
	}{
		{
			name:    "commit count below exact chain",
			aheadBy: 1, status: "ahead", fileStatus: "modified",
		},
		{
			name:    "unsupported renamed path",
			aheadBy: 2, status: "ahead", fileStatus: "renamed",
		},
		{
			name:    "duplicate changed path",
			aheadBy: 2, status: "ahead", fileStatus: "modified",
			duplicate: true,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				writer.Header().Set("Content-Type", "application/json")
				require.Equal(t,
					"/repos/WuKongIM/WuKongIM/compare/"+base+"..."+head,
					request.URL.Path,
				)
				files := []map[string]any{{
					"filename": "docs/fix.md",
					"status":   test.fileStatus, "sha": fortyHex("3"),
				}}
				if test.duplicate {
					files = append(files, files[0])
				}
				writeJSON(t, writer, map[string]any{
					"status": test.status, "ahead_by": test.aheadBy,
					"behind_by": 0, "total_commits": test.aheadBy,
					"files": files,
				})
			}))
			t.Cleanup(server.Close)

			client := newTestClient(t, server)
			_, err := client.CompareCandidate(
				context.Background(), base, head, 2,
			)
			require.Error(t, err)
			var rejection issueagentgithub.CandidateComparisonRejection
			require.ErrorAs(t, err, &rejection)
		})
	}
}

const zeroOIDForTest = "0000000000000000000000000000000000000000"

func testGitBlobSHA(content []byte) string {
	hasher := sha1.New() // #nosec G401 -- test verifies Git blob identity.
	_, _ = hasher.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}
