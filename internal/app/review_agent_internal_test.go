package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestCollectOnlyReviewBaselineNeedsNoProcessExecutor(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	ledger, err := verify.NewFileLedger(ledgerPath, workspace)
	require.NoError(t, err)
	generation := contract.GenerationIdentity{
		Repository:     "WuKongIM/WuKongIM",
		PullRequest:    716,
		HeadSHA:        strings.Repeat("1", 40),
		BaseSHA:        strings.Repeat("2", 40),
		TestMergeSHA:   strings.Repeat("3", 40),
		IntentDigest:   "sha256:" + strings.Repeat("4", 64),
		Generation:     17,
		StateParentSHA: strings.Repeat("5", 40),
	}
	require.NoError(t, ledger.Append(generation, contract.CheckEvidence{
		Name:          "go-format",
		CommandDigest: testReviewDigest("command"),
		Outcome:       contract.CheckOutcomePassed,
		DurationMS:    1,
		StdoutDigest:  testReviewDigest(""),
		StderrDigest:  testReviewDigest(""),
	}))
	contextValue := contract.ReviewContext{
		SchemaVersion:      1,
		Generation:         generation,
		PolicyDigest:       testReviewDigest("policy"),
		PromptDigest:       testReviewDigest("prompt"),
		OutputSchemaDigest: testReviewDigest("schema"),
		ReviewReason:       "synchronize",
		Title:              "Collect trusted evidence",
		ChangedFiles: []contract.ChangedFile{{
			Path:          "internal/example.go",
			Status:        contract.FileStatusModified,
			Mode:          "100644",
			Type:          "text",
			Patch:         "@@ -1 +1 @@",
			PatchDigest:   testReviewDigest("@@ -1 +1 @@"),
			ContentDigest: testReviewDigest(""),
		}},
		MandatoryChecks: []string{"go-format"},
	}
	now := time.Date(2026, 7, 31, 13, 42, 20, 0, time.UTC)
	evidence, err := verifyReviewBaseline(
		context.Background(),
		ReviewAgentConfig{
			PolicyPath: filepath.Join(
				"..", "..", ".github", "review-agent", "policy.json",
			),
			WorkspaceDirectory: workspace,
			EvidenceLedgerPath: ledgerPath,
			ExecutorHome:       t.TempDir(),
			ExecutablePath:     os.Getenv("PATH"),
			ProcessSandboxPath: filepath.Join(t.TempDir(), "missing-bwrap"),
			ProcessHelperPath:  filepath.Join(t.TempDir(), "missing-helper"),
		},
		func() time.Time { return now },
		cli.VerifyBaselineRequest{
			Context: contextValue, CollectOnly: true,
		},
	)
	require.NoError(t, err)
	require.Equal(t, generation, evidence.Generation)
	require.Equal(t, []contract.CheckEvidence{{
		Name:          "go-format",
		CommandDigest: testReviewDigest("command"),
		Outcome:       contract.CheckOutcomePassed,
		DurationMS:    1,
		StdoutDigest:  testReviewDigest(""),
		StderrDigest:  testReviewDigest(""),
	}}, evidence.Checks)
}

func testReviewDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestResolveReviewCommandIgnoresOrdinaryStatusComment(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	command, found, err := resolveReviewCommand(
		context.Background(),
		nil,
		reviewagentgithub.PullRequestSnapshot{
			Author: "pull-request-author",
			IssueComments: []reviewagentgithub.IssueComment{{
				ID: 7, Author: "review-agent[bot]",
				Body:      "Review Agent is reviewing this pull request.",
				CreatedAt: now, UpdatedAt: now,
			}},
		},
		7,
	)
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, command)
}

func TestResolveReviewCommandIgnoresMalformedCommandPrefix(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	_, found, err := resolveReviewCommand(
		context.Background(),
		nil,
		reviewagentgithub.PullRequestSnapshot{
			Author: "pull-request-author",
			IssueComments: []reviewagentgithub.IssueComment{{
				ID: 7, Author: "pull-request-author",
				Body:      "@review-agent",
				CreatedAt: now, UpdatedAt: now,
			}},
		},
		7,
	)
	require.NoError(t, err)
	require.False(t, found)
}

func TestReviewDiscussionPreservesEveryGitHubSurface(t *testing.T) {
	t.Parallel()

	headSHA := strings.Repeat("a", 40)
	discussion := reviewDiscussion(reviewagentgithub.PullRequestSnapshot{
		Reviews: []reviewagentgithub.Review{{
			ID: 1, Author: "review-agent[bot]", AuthorType: "Bot",
			State: "CHANGES_REQUESTED", Body: "Fix the race.",
			CommitID: headSHA,
		}},
		IssueComments: []reviewagentgithub.IssueComment{{
			ID: 2, Author: "alice", AuthorType: "User",
			Body: "@review-agent reconsider fixed",
		}},
		ReviewComments: []reviewagentgithub.ReviewComment{{
			ID: 3, Author: "review-agent[bot]", AuthorType: "Bot",
			Body: "The queue is unsynchronized.", Path: "queue.go",
			Line: 7, Side: "RIGHT", InReplyToID: 0,
		}},
	})

	require.Equal(t, []contract.DiscussionItem{
		{
			Kind: contract.DiscussionFormalReview, ID: 1,
			Author: "review-agent[bot]", AuthorType: "Bot",
			Body: "Fix the race.", State: "CHANGES_REQUESTED",
			CommitSHA: headSHA,
		},
		{
			Kind: contract.DiscussionIssueComment, ID: 2,
			Author: "alice", AuthorType: "User",
			Body: "@review-agent reconsider fixed",
		},
		{
			Kind: contract.DiscussionReviewComment, ID: 3,
			Author: "review-agent[bot]", AuthorType: "Bot",
			Body: "The queue is unsynchronized.", Path: "queue.go",
			Line: 7, Side: "RIGHT", InReplyToID: 0,
		},
	}, discussion)
}

func TestSchedulerDigestChangedIgnoresEmptyCollections(t *testing.T) {
	t.Parallel()

	left := reviewagent.SchedulerState{
		SchemaVersion: 1,
		SourceSHA:     strings.Repeat("a", 40),
		Sequence:      1,
		UpdatedAt:     time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC),
	}
	right := left
	right.Queue = []reviewagent.QueueEntry{}
	right.Active = []reviewagent.Lease{}

	require.False(t, schedulerDigestChanged(
		left,
		right,
		reviewagent.SchedulerLimits{
			MaxActive: 3, MaxPerPullRequest: 1,
			MaxFirstTimeExternal: 1,
		},
	))
}
