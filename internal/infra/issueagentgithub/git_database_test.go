package issueagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestGitDatabasePublishesVerifiedNonForceCommit(t *testing.T) {
	t.Parallel()

	parent := fortyHex("a")
	baseTree := fortyHex("b")
	blob := fortyHex("c")
	tree := fortyHex("d")
	commit := fortyHex("e")
	var refForce any
	var refSHA string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		require.Equal(t, "Bearer token", request.Header.Get("Authorization"))
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/repos/WuKongIM/WuKongIM/git/blobs":
			writeJSON(t, writer, map[string]any{"sha": blob})
		case request.Method == http.MethodPost && request.URL.Path == "/repos/WuKongIM/WuKongIM/git/trees":
			writeJSON(t, writer, map[string]any{"sha": tree})
		case request.Method == http.MethodPost && request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits":
			writeJSON(t, writer, map[string]any{
				"sha": commit, "tree": map[string]any{"sha": tree},
				"parents":      []map[string]any{{"sha": parent}},
				"verification": map[string]any{"verified": true, "reason": "valid"},
			})
		case request.Method == http.MethodPatch &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/refs/heads/agent/issue-42":
			var body map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			refForce = body["force"]
			refSHA, _ = body["sha"].(string)
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/agent/issue-42",
				"object": map[string]any{"type": "commit", "sha": commit},
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
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	published, err := client.PublishCommit(context.Background(), issueagentgithub.CommitPlan{
		Branch: "agent/issue-42", ExpectedParentSHA: parent, BaseTreeSHA: baseTree,
		Message: "fix(agent): issue #42", ExistingBranch: true,
		ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
			Path: "pkg/example/fix.go", Operation: issueagent.FileOperationUpsert,
			Mode: issueagent.FileModeRegular, ContentBase64: issueagent.EncodeFileContent([]byte("package example\n")),
		}}},
	})
	require.NoError(t, err)
	require.Equal(t, commit, published.CommitSHA)
	require.Equal(t, false, refForce)
	require.Equal(t, commit, refSHA)
}

func TestGitDatabaseRejectsUnverifiedCommitAndStaleRef(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		verified  bool
		reason    string
		refStatus int
	}{
		{name: "unverified", reason: "unsigned", refStatus: http.StatusOK},
		{name: "stale ref", verified: true, reason: "valid", refStatus: http.StatusUnprocessableEntity},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.URL.Path == "/repos/WuKongIM/WuKongIM/git/blobs":
					writeJSON(t, writer, map[string]any{"sha": fortyHex("c")})
				case request.URL.Path == "/repos/WuKongIM/WuKongIM/git/trees":
					writeJSON(t, writer, map[string]any{"sha": fortyHex("d")})
				case request.URL.Path == "/repos/WuKongIM/WuKongIM/git/commits":
					writeJSON(t, writer, map[string]any{
						"sha": fortyHex("e"), "tree": map[string]any{"sha": fortyHex("d")},
						"parents": []map[string]any{{"sha": fortyHex("a")}},
						"verification": map[string]any{
							"verified": test.verified, "reason": test.reason,
						},
					})
				case stringsHasSuffix(request.URL.Path, "/git/refs/heads/agent/issue-42"):
					writer.WriteHeader(test.refStatus)
					_, _ = writer.Write([]byte(`{}`))
				default:
					http.NotFound(writer, request)
				}
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.PublishCommit(context.Background(), issueagentgithub.CommitPlan{
				Branch: "agent/issue-42", ExpectedParentSHA: fortyHex("a"),
				BaseTreeSHA: fortyHex("b"), Message: "fix", ExistingBranch: true,
				ChangeSet: issueagent.ChangeSet{Files: []issueagent.FileChange{{
					Path: "fix.go", Operation: issueagent.FileOperationUpsert,
					Mode: issueagent.FileModeRegular, ContentBase64: issueagent.EncodeFileContent([]byte("fix")),
				}}},
			})
			require.Error(t, err)
		})
	}
}

func stringsHasSuffix(value, suffix string) bool {
	return len(value) >= len(suffix) && value[len(value)-len(suffix):] == suffix
}
