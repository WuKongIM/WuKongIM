package issueagentgithub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestClientReadsOnlyExactSignedStateCommitAndVerifiedBlob(t *testing.T) {
	t.Parallel()

	state := stateStoreRecoveryState()
	content, err := contract.CanonicalIssueAgentState(state)
	require.NoError(t, err)
	commitSHA := fortyHex("b")
	parentSHA := fortyHex("a")
	rootTreeSHA := fortyHex("c")
	stateTreeSHA := fortyHex("d")
	blobSHA := testGitBlobSHA(content)
	path := ".issue-agent-state/issue-42.json"
	corruptBlob := false

	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/ref/heads/agent-state/issue-42":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/agent-state/issue-42",
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			})
		case "/repos/WuKongIM/WuKongIM/git/commits/" + commitSHA:
			writeJSON(t, writer, map[string]any{
				"sha": commitSHA, "message": "agent(state): issue 42 sequence 1",
				"tree":    map[string]any{"sha": rootTreeSHA},
				"parents": []map[string]any{{"sha": parentSHA}},
				"verification": map[string]any{
					"verified": true, "reason": "valid",
				},
			})
		case "/repos/WuKongIM/WuKongIM/compare/" + parentSHA + "..." + commitSHA:
			writeJSON(t, writer, map[string]any{
				"status": "ahead", "ahead_by": 1, "behind_by": 0,
				"total_commits": 1,
				"files": []map[string]any{{
					"filename": path, "status": "added", "sha": blobSHA,
				}},
			})
		case "/repos/WuKongIM/WuKongIM/git/trees/" + rootTreeSHA:
			writeJSON(t, writer, map[string]any{
				"sha": rootTreeSHA, "truncated": false,
				"tree": []map[string]any{{
					"path": ".issue-agent-state", "mode": "040000",
					"type": "tree", "sha": stateTreeSHA,
				}},
			})
		case "/repos/WuKongIM/WuKongIM/git/trees/" + stateTreeSHA:
			writeJSON(t, writer, map[string]any{
				"sha": stateTreeSHA, "truncated": false,
				"tree": []map[string]any{{
					"path": "issue-42.json", "mode": "100644",
					"type": "blob", "sha": blobSHA,
				}},
			})
		case "/repos/WuKongIM/WuKongIM/git/blobs/" + blobSHA:
			body := content
			if corruptBlob {
				body = []byte("different state")
			}
			writeJSON(t, writer, map[string]any{
				"sha": blobSHA, "size": len(body), "encoding": "base64",
				"content": base64.StdEncoding.EncodeToString(body),
			})
		case "/repos/WuKongIM/WuKongIM/commits/" + commitSHA:
			writeJSON(t, writer, map[string]any{
				"sha": commitSHA,
				"author": map[string]any{
					"login": "wukongim-issue-agent[bot]", "type": "Bot",
				},
			})
		case "/graphql":
			writeJSON(t, writer, signedCommitAttributionResponse(commitSHA))
		default:
			http.NotFound(writer, request)
		}
	})
	client := newIssueMemoryClient(t, handler)

	head, found, err := client.StateRefHead(
		context.Background(), "agent-state/issue-42",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, commitSHA, head)
	record, err := client.ReadStateCommit(context.Background(), commitSHA, path)
	require.NoError(t, err)
	require.Equal(t, parentSHA, record.ParentSHA)
	require.Equal(t, content, record.Content)
	require.Equal(t, "wukongim-issue-agent[bot]", record.AuthorLogin)
	require.True(t, record.Verified)
	require.True(t, record.SignedByGitHub)

	corruptBlob = true
	_, err = client.ReadGitBlob(context.Background(), blobSHA, 256<<10)
	require.EqualError(t, err, "Git blob content is inconsistent")
}

func TestClientPublishesCanonicalStateThroughExpectedHeadSignedCommit(t *testing.T) {
	t.Parallel()

	state := stateStoreRecoveryState()
	content, err := contract.CanonicalIssueAgentState(state)
	require.NoError(t, err)
	parentSHA := state.SourceSHA
	baseTreeSHA := fortyHex("a")
	commitSHA := fortyHex("b")
	resultTreeSHA := fortyHex("c")
	blobSHA := testGitBlobSHA(content)
	branch := "agent-state/issue-42"
	path := ".issue-agent-state/issue-42.json"
	message := "agent(state): issue 42 sequence 1"
	var mutationCalls int

	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+parentSHA:
			writeJSON(t, writer, map[string]any{
				"sha": parentSHA, "tree": map[string]any{"sha": baseTreeSHA},
				"parents":      []any{},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/graphql":
			encoded, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
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
				mutationCalls++
				require.Equal(t, "WuKongIM/WuKongIM", envelope.Variables.Input.Branch.Repository)
				require.Equal(t, branch, envelope.Variables.Input.Branch.Name)
				require.Equal(t, parentSHA, envelope.Variables.Input.ExpectedHead)
				require.Equal(t, message, envelope.Variables.Input.Message.Headline)
				require.Equal(t, path, envelope.Variables.Input.FileChanges.Additions[0].Path)
				require.Equal(t, contract.EncodeFileContent(content), envelope.Variables.Input.FileChanges.Additions[0].Contents)
				writeJSON(t, writer, map[string]any{
					"data": map[string]any{
						"createCommitOnBranch": map[string]any{
							"commit": map[string]any{"oid": commitSHA},
						},
					},
					"errors": []any{},
				})
				return
			}
			writeJSON(t, writer, signedCommitAttributionResponse(commitSHA))
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/"+branch:
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/" + branch,
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits/"+commitSHA:
			writeJSON(t, writer, map[string]any{
				"sha": commitSHA, "message": message,
				"tree":    map[string]any{"sha": resultTreeSHA},
				"parents": []map[string]any{{"sha": parentSHA}},
				"verification": map[string]any{
					"verified": true, "reason": "valid",
				},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/compare/"+parentSHA+"..."+commitSHA:
			writeJSON(t, writer, map[string]any{
				"status": "ahead", "ahead_by": 1, "behind_by": 0,
				"total_commits": 1,
				"files": []map[string]any{{
					"filename": path, "status": "modified", "sha": blobSHA,
				}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/commits/"+commitSHA:
			writeJSON(t, writer, map[string]any{
				"sha": commitSHA,
				"author": map[string]any{
					"login": "wukongim-issue-agent[bot]", "type": "Bot",
				},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newIssueMemoryClient(t, handler)

	result, err := client.PublishStateCommit(
		context.Background(),
		issueagentgithub.StateCommitRequest{
			Branch: branch, Path: path,
			ExpectedParentSHA: parentSHA, BaseTreeSHA: baseTreeSHA,
			ExistingBranch: true, Message: message, Content: content,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, mutationCalls)
	require.Equal(t, commitSHA, result.CommitSHA)
	require.Equal(t, parentSHA, result.ParentSHA)
	require.Equal(t, "wukongim-issue-agent[bot]", result.AuthorLogin)
	require.True(t, result.Verified)
	require.True(t, result.SignedByGitHub)
}

func signedCommitAttributionResponse(commitSHA string) map[string]any {
	return map[string]any{
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
	}
}
